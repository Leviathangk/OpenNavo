//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// GitHub release sources take part in the same lifecycle as Homebrew commit sources: a new catalog version
// enqueues them, restoring an excluded package reschedules them and the admin source list shows them.
func TestGitHubReleaseSourceLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"iina","version":"1.4.4"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, brew, releases int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='iina'").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{"repo":"Homebrew/homebrew-cask","path":"Casks/i/iina.rb"}',90,'auto') RETURNING id`, pid).Scan(&brew).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'github_releases','{"repo":"iina/iina"}',20,'auto') RETURNING id`, pid).Scan(&releases).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data) VALUES(?,'github','homepage','iina/iina','found','{"repo":"iina/iina"}')`, pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("DELETE FROM job_outbox").Error)

	next, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"iina","version":"1.5.0"}`))
	require.NoError(t, err)
	catalogRows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: next, Previous: catalogRows["iina"]}}))
	var queued []int64
	require.NoError(t, st.DB.WithContext(ctx).Table("job_outbox").Where("job_type='changelog:fetch'").Pluck("(payload->>'sourceId')::bigint", &queued).Error)
	require.ElementsMatch(t, []int64{brew, releases}, queued)

	_, err = st.SetPackageChangelogSettings(ctx, pid, true, store.AuditActor{})
	require.NoError(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET next_fetch_at=now()+interval '2 days' WHERE package_id=?", pid).Error)
	_, err = st.SetPackageChangelogSettings(ctx, pid, false, store.AuditActor{})
	require.NoError(t, err)
	var rescheduled int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT count(*) FROM changelog_sources WHERE package_id=? AND next_fetch_at<=now()", pid).Scan(&rescheduled).Error)
	require.Equal(t, int64(2), rescheduled)

	svc := &Service{Store: st}
	listed, err := svc.Execute(ctx, "ListChangelogSources", Input{ID: pid})
	require.NoError(t, err)
	data, err := json.Marshal(listed)
	require.NoError(t, err)
	var rows []struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(data, &rows))
	require.Len(t, rows, 2)
	require.Equal(t, "github_releases", rows[0].Type, "ordered by priority")
	require.Equal(t, releases, rows[0].ID)
	require.Equal(t, "homebrew_commits", rows[1].Type)
}

// A version bump used to enqueue every enabled GitHub source. An auto source whose repository
// the facts job no longer verifies is outside the fetch scope, so the bump must leave it alone.
// A manual source stays an editor's choice and is still read immediately.
func TestVersionChangeSkipsUnverifiedGitHubReleaseSource(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	dead, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"dead-repo","version":"1.0.0"}`))
	require.NoError(t, err)
	manual, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"manual-repo","version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: dead}, {Package: manual}}))
	var deadID, manualID, brew, gone, kept int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='dead-repo'").Scan(&deadID).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='manual-repo'").Scan(&manualID).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{"repo":"Homebrew/homebrew-cask"}',90,'auto') RETURNING id`, deadID).Scan(&brew).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'github_releases','{"repo":"overtake/TelegramSwift"}',20,'auto') RETURNING id`, deadID).Scan(&gone).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status) VALUES(?,'github','homepage','','none')`, deadID).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'github_releases','{"repo":"editor/app"}',20,'manual') RETURNING id`, manualID).Scan(&kept).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("DELETE FROM job_outbox").Error)

	nextDead, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"dead-repo","version":"1.1.0"}`))
	require.NoError(t, err)
	nextManual, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"manual-repo","version":"1.1.0"}`))
	require.NoError(t, err)
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{
		{Package: nextDead, Previous: rows["dead-repo"]},
		{Package: nextManual, Previous: rows["manual-repo"]},
	}))
	var queued []int64
	require.NoError(t, st.DB.WithContext(ctx).Table("job_outbox").Where("job_type='changelog:fetch'").Pluck("(payload->>'sourceId')::bigint", &queued).Error)
	require.ElementsMatch(t, []int64{brew, kept}, queued)
}
