//go:build integration

package githubreleases

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSnapshotUsesEncryptedSavedTokenAndPreservesDataOnFailure(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32)}
	at := time.Date(2026, 10, 29, 8, 10, 0, 0, time.UTC)
	expected := "saved-test-credential"
	failed := false
	calls := 0
	svc := &Service{Store: st, Config: cfg, Now: func() time.Time { return at }, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "Bearer "+expected, r.Header.Get("Authorization"))
		if failed {
			return reply(t, 503, "upstream unavailable"), nil
		}
		if r.URL.Path == "/repos/opennavo/OpenNavo/releases" {
			return reply(t, 200, []release{{ID: 1, Tag: "desktop-v0.1.3"}}), nil
		}
		return reply(t, 200, []map[string]any{{"id": 1, "name": "OpenNavo_0.1.3_universal.dmg", "download_count": 953}}), nil
	})}}
	_, err := svc.Run(ctx)
	require.Error(t, err)
	require.Zero(t, calls)
	save := func(value string) {
		t.Helper()
		encrypted, err := cfg.EncryptGitHubToken(value)
		require.NoError(t, err)
		require.NoError(t, st.DB.Exec("UPDATE github_settings SET token_ciphertext=? WHERE id=true", encrypted).Error)
	}
	save(expected)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	initial, err := st.GitHubReleaseOverview(ctx, at)
	require.NoError(t, err)
	require.EqualValues(t, 953, *initial.Downloads.Total)
	expected = "rotated-test-credential"
	save(expected)
	at = at.Add(time.Hour)
	failed = true
	_, err = svc.Run(ctx)
	require.Error(t, err)
	unchanged, err := st.GitHubReleaseOverview(ctx, at)
	require.NoError(t, err)
	require.Equal(t, initial, unchanged)
	save("")
	before := calls
	_, err = svc.Run(ctx)
	require.Error(t, err)
	require.Equal(t, before, calls)
	unchanged, err = st.GitHubReleaseOverview(ctx, at)
	require.NoError(t, err)
	require.Equal(t, initial, unchanged)
}
