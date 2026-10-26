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
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEvidenceCaptureReplayAndProvenance(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,homepage) VALUES(701,'cask','example-editor','example-editor','homebrew/cask','Example Editor','1.0','1.0','{}','fixture','https://vendor.example.test')`).Error)
	service := NewEvidenceService(st.DB, "fixture")
	calls := 0
	service.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(`<h1>Mac editor</h1><p>Professional costs 19.99 USD in US.</p><script>It is free</script>`)), Request: r}, nil
	})}
	allowed := func(*gorm.DB) error { return nil }
	in := Capture{Owner: "admin:1", IdempotencyKey: uuid.NewString(), EntityKey: "package:701", URL: "https://vendor.example.test/pricing", Excerpt: "Professional costs 19.99 USD in US."}
	first, err := service.capture(ctx, in, allowed)
	require.NoError(t, err)
	require.Equal(t, "official", first.Authority)
	require.Equal(t, "pkg:701", first.EntityKey)
	require.Len(t, first.BodyHash, 64)
	second, err := service.capture(ctx, in, allowed)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, calls)
	in.Excerpt = "It is free"
	_, err = service.capture(ctx, in, allowed)
	require.ErrorIs(t, err, ErrIdempotencyKeyReused)
	in.IdempotencyKey = uuid.NewString()
	_, err = service.capture(ctx, in, allowed)
	require.ErrorContains(t, err, "excerpt_not_found")
	in.Excerpt = first.Excerpt
	_, err = service.capture(ctx, in, func(*gorm.DB) error { return reject("evidence_entity_mismatch") })
	require.ErrorContains(t, err, "evidence_entity_mismatch")
	service.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("injected timeout") })
	_, err = service.capture(ctx, in, allowed)
	require.ErrorContains(t, err, "evidence_fetch_failed")
	var count int64
	require.NoError(t, st.DB.Table("evidence_snapshots").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Exec("UPDATE evidence_snapshots SET valid_until=now()-interval '1 second' WHERE id=?", first.ID).Error)
	err = st.DB.Transaction(func(tx *gorm.DB) error { _, e := LoadEvidence(ctx, tx, []int64{first.ID}, "pkg:701", true); return e })
	require.ErrorContains(t, err, "evidence_expired_or_wrong_entity")
}
