//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Notes stored while a repository was public must not outlive the verified-public fact. The fetcher
// already refuses new notes once the repository turns private; this pins the read path, which used to
// show the rows it had already stored regardless of the source scope.
func TestTimelineHidesGitHubReleasesOnceTheRepositoryIsNoLongerVerified(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	pkg, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"scope","name":["Scope"],"version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	var pid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='scope'").Scan(&pid).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data)
 VALUES(?,'github','homepage','owner/app','found','{"repo":"owner/app"}')`, pid).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO changelog_sources(package_id,type,config,resolved_by)
 VALUES(?,'github_releases','{"repo":"owner/app"}','auto')`, pid).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,title,source_locale)
 VALUES(?,'github_release','v1.0.0','1.0.0','One','en-US')`, pid).Error)

	releases, err := st.TimelineReleases(ctx, pid, "en-US")
	require.NoError(t, err)
	require.Len(t, releases, 1, "a verified public repository keeps its notes")

	// The facts job clears the subject when the repository turns private.
	require.NoError(t, st.DB.Exec(`UPDATE package_source_facts SET status='none', subject='' WHERE package_id=? AND source='github'`, pid).Error)
	releases, err = st.TimelineReleases(ctx, pid, "en-US")
	require.NoError(t, err)
	require.Empty(t, releases, "stored notes may not outlive the verified public repository")

	// A manual source is an editor's decision and is not gated by the facts job.
	require.NoError(t, st.DB.Exec(`UPDATE changelog_sources SET resolved_by='manual' WHERE package_id=? AND type='github_releases'`, pid).Error)
	releases, err = st.TimelineReleases(ctx, pid, "en-US")
	require.NoError(t, err)
	require.Len(t, releases, 1)
}
