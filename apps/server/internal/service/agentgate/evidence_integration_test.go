//go:build integration

package agentgate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEvidenceCaptureLeaseAndProvenance(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,homepage) VALUES(701,'cask','example-editor','example-editor','homebrew/cask','Example Editor','1.0','1.0','{}','fixture','https://vendor.example.test')`).Error)
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "fill_fact", EntityKey: "pkg:701", DedupeKey: "evidence-test", RuleVersion: "test", Evidence: map[string]any{}})
	require.NoError(t, err)
	claimed, err := tasks.Claim(ctx, "test-owner", 1, nil)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	service := NewEvidenceService(st.DB, "fixture")
	calls := 0
	service.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(`<h1>Mac editor</h1><p>Professional costs 19.99 USD in US.</p><script>It is free</script>`)), Request: r}, nil
	})}
	in := Capture{TaskID: claimed[0].ID, Fencing: claimed[0].Fencing, Owner: "test-owner", IdempotencyKey: uuid.NewString(), EntityKey: "pkg:701", URL: "https://vendor.example.test/pricing", Excerpt: "Professional costs 19.99 USD in US."}
	first, err := service.Capture(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "official", first.Authority)
	require.Len(t, first.BodyHash, 64)
	second, err := service.Capture(ctx, in)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, calls)
	in.Excerpt = "It is free"
	_, err = service.Capture(ctx, in)
	require.ErrorIs(t, err, seotasks.ErrConflict)
	in.IdempotencyKey = uuid.NewString()
	_, err = service.Capture(ctx, in)
	require.ErrorContains(t, err, "excerpt_not_found")
	in.Excerpt = first.Excerpt
	in.IdempotencyKey = uuid.NewString()
	in.Fencing--
	_, err = service.Capture(ctx, in)
	require.ErrorIs(t, err, seotasks.ErrLease)
	in.Fencing++
	service.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("injected timeout") })
	_, err = service.Capture(ctx, in)
	require.ErrorContains(t, err, "evidence_fetch_failed")
	var count int64
	require.NoError(t, st.DB.Table("seo_evidence_snapshots").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Exec("UPDATE seo_evidence_snapshots SET valid_until=now()-interval '1 second' WHERE id=?", first.ID).Error)
	err = st.DB.Transaction(func(tx *gorm.DB) error { _, e := LoadEvidence(ctx, tx, []int64{first.ID}, "pkg:701", true); return e })
	require.ErrorContains(t, err, "evidence_expired_or_wrong_entity")
}

func TestLegacyAlternativesRequireSpecificEvidence(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(718,'cask','fixture-editor','fixture-editor','homebrew/cask','Fixture Editor','1.0','1.0','{}','fixture')`).Error)
	body := map[string]any{"items": []any{map[string]any{"packageId": 718, "reasons": []string{"open_source"}}}}
	require.Error(t, validateAlternatives(ctx, st.DB, body, []Evidence{{Authority: "official", Excerpt: "Unrelated Mac editor"}}))
	require.Error(t, validateAlternatives(ctx, st.DB, body, []Evidence{{Authority: "official", Excerpt: "Fixture Editor has paid licenses"}}))
	require.NoError(t, validateAlternatives(ctx, st.DB, body, []Evidence{{Authority: "official", Excerpt: "Fixture Editor is an open source alternative"}}))
}
