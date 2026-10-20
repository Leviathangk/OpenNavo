//go:build integration

package sourcefacts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type nvdStub struct {
	calls int
	fail  bool
}

func (n *nvdStub) ExactMatches(_ context.Context, cpe string) ([]facts.Vulnerability, error) {
	n.calls++
	if n.fail {
		return nil, errors.New("fixture provider unavailable")
	}
	return []facts.Vulnerability{{ID: "CVE-2026-12345", ProductCPE: cpe, AppVersion: "1.0", SourceURL: "https://nvd.nist.gov/vuln/detail/CVE-2026-12345"}, {ID: "CVE-2026-99999", ProductCPE: cpe, AppVersion: "2.0", SourceURL: "https://nvd.nist.gov/vuln/detail/CVE-2026-99999"}}, nil
}
func TestNVDChecksExactVersionCachesAndPreservesEvidenceOnFailure(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC()
	cpe := "cpe:2.3:a:example:editor:1.0:*:*:*:*:macos:*:*"
	addCask(t, st, `{"token":"nvd-example","name":["Example"],"version":"1.0"}`)
	var id int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='nvd-example'").Scan(&id).Error)
	value, _ := json.Marshal(facts.SecurityObservation{AppVersion: "1.0", Channels: []string{"vendor"}, OpenSource: "unknown", ProductCPE: cpe})
	require.NoError(t, st.UpsertFactRecords(ctx, id, []facts.Record{{Kind: "security", Key: "identity", Value: value, Evidence: facts.NewProof("https://vendor.example/security", now, 72*time.Hour, "verified", now)}}, now))
	provider := &nvdStub{}
	svc := &Service{Store: st, NVD: provider, Now: func() time.Time { return now }, Sleep: func(context.Context, time.Duration) error { return nil }}
	require.NoError(t, svc.runNVD(ctx, now.Add(time.Minute)))
	require.Equal(t, 1, provider.calls)
	matches, err := st.PublicCVEs(ctx, id, cpe, "1.0", now)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	matches, err = st.PublicCVEs(ctx, id, cpe, "2.0", now)
	require.NoError(t, err)
	require.Empty(t, matches)
	require.NoError(t, svc.runNVD(ctx, now.Add(time.Minute)))
	require.Equal(t, 1, provider.calls)
	now = now.Add(24 * time.Hour)
	provider.fail = true
	require.NoError(t, svc.runNVD(ctx, now.Add(time.Minute)))
	require.Equal(t, 2, provider.calls)
	matches, err = st.PublicCVEs(ctx, id, cpe, "1.0", now)
	require.NoError(t, err)
	require.Empty(t, matches, "failed refresh must not extend old vulnerability evidence")
	require.NoError(t, st.DB.Exec("UPDATE packages SET version='2.0' WHERE id=?", id).Error)
	require.NoError(t, svc.runNVD(ctx, now.Add(time.Minute)))
	require.Equal(t, 2, provider.calls, "new app version needs an exact new mapping")
}
