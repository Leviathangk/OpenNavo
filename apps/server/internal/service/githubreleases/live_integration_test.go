//go:build integration && live

package githubreleases

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The real credential stays in memory; even the isolated test DB never stores it.
type liveCredentialStore struct{ *store.Store }

func (*liveCredentialStore) GitHubToken(context.Context, config.Config) (string, error) {
	return os.Getenv("OPENNAVO_LIVE_GITHUB_TOKEN"), nil
}

func TestLiveGitHubReleaseSnapshot(t *testing.T) {
	if os.Getenv("OPENNAVO_LIVE_GITHUB_TOKEN") == "" {
		t.Skip("explicit live credential required")
	}
	st := testutil.NewStore(t) // Always fresh, disposable local containers, never a configured external DB.
	ctx := context.Background()
	stats, err := (&Service{Store: &liveCredentialStore{st}, Config: config.Config{FetchUserAgent: "OpenNavo-release-stats-check"}}).Run(ctx)
	require.NoError(t, err)
	overview, err := st.GitHubReleaseOverview(ctx, time.Now())
	require.NoError(t, err)
	require.NotNil(t, overview.Downloads.Total)
	require.NotNil(t, overview.Updates.Total)
	require.GreaterOrEqual(t, *overview.Downloads.Total, int64(953))
	require.GreaterOrEqual(t, *overview.Updates.Total, int64(582))
	require.Nil(t, overview.Downloads.Today)
	require.Nil(t, overview.DailyStartDate)
	t.Logf("Live GitHub snapshot: releases=%v assets=%v downloads=%d updates=%d capturedAt=%s", stats["releases"], stats["assets"], *overview.Downloads.Total, *overview.Updates.Total, overview.LastSnapshotAt.Format(time.RFC3339))
}
