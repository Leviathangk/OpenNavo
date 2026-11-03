//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func followupPackage(t *testing.T, token string) (context.Context, *store.Store, int64) {
	t.Helper()
	ctx := context.Background()
	st := testutil.NewStore(t)
	pkg, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","name":["Fixture"],"version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	var pid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token=?", token).Scan(&pid).Error)
	return ctx, st, pid
}

func TestUpstreamRenameKeepsAnEditorsReleaseTitle(t *testing.T) {
	ctx, st, pid := followupPackage(t, "titles")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data)
 VALUES(?,'github','homepage','owner/app','found','{"repo":"owner/app"}')`, pid).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO changelog_sources(package_id,type,config,resolved_by)
 VALUES(?,'github_releases','{"repo":"owner/app"}','auto')`, pid).Error)
	var sourceID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM changelog_sources WHERE package_id=?", pid).Scan(&sourceID).Error)
	state, err := st.ChangelogState(ctx, sourceID)
	require.NoError(t, err)
	now := time.Now().UTC()
	result := changelog.FetchResult{ContentHash: "hash-1", Releases: []changelog.Release{
		{Tag: "v1.0.0", Version: "1.0.0", Title: "Upstream one", SourceURL: "https://github.com/owner/app/releases/tag/v1.0.0"},
		{Tag: "v1.1.0", Version: "1.1.0", Title: "Upstream two", SourceURL: "https://github.com/owner/app/releases/tag/v1.1.0"},
	}}
	_, _, err = st.SaveFetchedChangelogWithLifecycle(ctx, state, result, now.Add(3*time.Hour), now)
	require.NoError(t, err)

	// An editor renames the first release; the second keeps the upstream title.
	require.NoError(t, st.DB.Exec(`UPDATE releases SET title='Editor title', title_edited=true
 WHERE package_id=? AND source_key='v1.0.0'`, pid).Error)
	result.ContentHash = "hash-2"
	result.Releases[0].Title = "Upstream renamed one"
	result.Releases[1].Title = "Upstream renamed two"
	_, _, err = st.SaveFetchedChangelogWithLifecycle(ctx, state, result, now.Add(6*time.Hour), now.Add(time.Hour))
	require.NoError(t, err)

	titles := map[string]string{}
	rows, err := st.DB.Raw(`SELECT source_key,title FROM releases WHERE package_id=? AND source='github_release'`, pid).Rows()
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var key, title string
		require.NoError(t, rows.Scan(&key, &title))
		titles[key] = title
	}
	require.Equal(t, "Editor title", titles["v1.0.0"], "an editor's title survives an upstream rename")
	require.Equal(t, "Upstream renamed two", titles["v1.1.0"])
}

func TestRepositoryChangeDropsReleasesFromTheOldRepository(t *testing.T) {
	ctx, st, pid := followupPackage(t, "repochange")
	source := func(repo string) []changelog.Source {
		return []changelog.Source{{Type: "github_releases", Priority: 100, Config: map[string]any{"repo": repo}}}
	}
	require.NoError(t, st.SaveChangelogSources(ctx, pid, source("owner/old")))
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_url,source_locale)
 VALUES(?,'github_release','v1.0.0','1.0.0','https://github.com/owner/old/releases/tag/v1.0.0','en-US')`, pid).Error)
	// Re-resolving the same repository keeps its notes.
	require.NoError(t, st.SaveChangelogSources(ctx, pid, source("owner/old")))
	var count int
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM releases WHERE package_id=?`, pid).Scan(&count).Error)
	require.Equal(t, 1, count)

	// A new verified repository must not keep showing the old repository's notes.
	require.NoError(t, st.SaveChangelogSources(ctx, pid, source("owner/new")))
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM releases WHERE package_id=?`, pid).Scan(&count).Error)
	require.Zero(t, count)

	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_url,source_locale)
 VALUES(?,'github_release','v2.0.0','2.0.0','https://github.com/owner/new/releases/tag/v2.0.0','en-US')`, pid).Error)
	require.NoError(t, st.SaveChangelogSources(ctx, pid, source("owner/new")))
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM releases WHERE package_id=?`, pid).Scan(&count).Error)
	require.Equal(t, 1, count, "notes from the current repository stay")
}

func TestUnresolvedSourcesSkipAnInvalidVerifiedRepository(t *testing.T) {
	ctx, st, pid := followupPackage(t, "badsubject")
	// The facts job can leave a subject that the GitHub repository pattern rejects. Re-resolving it
	// every schedule run can never create a source, so it must not be a candidate at all.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data)
 VALUES(?,'github','homepage','../bad','found','{"repo":"x"}')`, pid).Error)
	candidates, err := st.UnresolvedHomebrewSources(ctx)
	require.NoError(t, err)
	for _, candidate := range candidates {
		require.NotEqual(t, pid, candidate.ID, "an invalid subject is not re-resolved on every run")
	}
}
