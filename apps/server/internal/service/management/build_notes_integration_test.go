//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Casks such as Amore keep 0.1 while every build ships new notes. A build owns its notes; the shared
// note of the base version stays with the builds that have none, and neither write touches the other.
func TestBuildReleaseNotesLeaveOlderBuildsUntouched(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	older, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"amore","version":"0.1,48","name":["Amore"]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: older}}))
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	current, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"amore","version":"0.1,49","name":["Amore"]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: current, Previous: rows["amore"]}}))
	pid := rows["amore"].ID
	require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at) VALUES(?,'0.1,47','0.1','2026-08-07T00:43:22Z')`, pid).Error)
	builds := map[string]int64{}
	var recorded []struct {
		ID      int64
		Version string
	}
	require.NoError(t, st.DB.Raw("SELECT id,version FROM package_versions WHERE package_id=?", pid).Scan(&recorded).Error)
	for _, row := range recorded {
		builds[row.Version] = row.ID
	}
	call := func(op string, in Input) (any, error) { t.Helper(); return svc.Execute(ctx, op, in) }
	write := func(version, summary string) {
		t.Helper()
		_, err := call("UpsertReleaseNotes", Input{ID: pid, Version: version, Body: map[string]any{"sourceLocale": "en-US", "summary": summary, "sections": []any{}}})
		require.NoError(t, err)
	}
	clear := func(version string) {
		t.Helper()
		_, err := call("UpsertReleaseNotes", Input{ID: pid, Version: version, Body: map[string]any{"clear": true}})
		require.NoError(t, err)
	}
	records := func(op string, in Input) []map[string]any {
		t.Helper()
		page, err := call(op, in)
		require.NoError(t, err)
		// Pages are read as their JSON: catalog activity adds nextSeq to the common page shape.
		encoded, err := json.Marshal(page)
		require.NoError(t, err)
		var decoded struct{ Records []map[string]any }
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		return decoded.Records
	}
	describe := func(row map[string]any) string {
		note := "-"
		if id, ok := row["editorialReleaseId"].(float64); ok {
			note = strconv.FormatInt(int64(id), 10)
		}
		return row["editorialScope"].(string) + ":" + note
	}
	// scopes maps each build to the scope and note it shows; both version lists agree.
	scopes := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, row := range records("ListAdminVersions", Input{Params: map[string]any{strings.ToLower("packageId"): float64(pid)}}) {
			out[row["version"].(string)] = describe(row)
			require.Equal(t, row["editorialScope"] != "none", row["hasEditorial"])
		}
		for _, row := range records("ListPackageVersions", Input{ID: pid}) {
			require.Equal(t, out[row["version"].(string)], describe(row))
		}
		return out
	}
	gapped := func(gap string) bool {
		t.Helper()
		return len(records("ListAdminPackages", Input{Params: map[string]any{"gaps": []any{gap}}})) == 1
	}
	editorial := func(scope string, args ...any) int64 {
		t.Helper()
		var id int64
		require.NoError(t, st.DB.Raw("SELECT id FROM releases WHERE package_id=? AND source='editorial' AND NOT hidden AND "+scope, append([]any{pid}, args...)...).Scan(&id).Error)
		return id
	}

	require.True(t, gapped("latestEditorial"))
	require.True(t, gapped("latestBuildEditorial"), "0.1,49 is one of several builds of 0.1 without notes of its own")
	// A shared note written before build notes existed.
	write("0.1", "Shared notes")
	shared := editorial("package_version_id IS NULL")
	require.Positive(t, shared)
	sharedState, err := st.HistorySnapshot(ctx, content.Ref{Entity: "release", ID: shared})
	require.NoError(t, err)
	require.False(t, gapped("latestEditorial"))
	require.True(t, gapped("latestBuildEditorial"), "the current build only shows the shared note of 0.1")
	s := strconv.FormatInt(shared, 10)
	require.Equal(t, map[string]string{"0.1,49": "base:" + s, "0.1,48": "base:" + s, "0.1,47": "base:" + s}, scopes())

	// Notes for the newest build never reach the shared note or the older builds.
	preview, err := call("UpsertReleaseNotes", Input{ID: pid, Version: "0.1,49", Params: map[string]any{"dryrun": true}, Body: map[string]any{"sourceLocale": "en-US", "summary": "Preview", "sections": []any{}}})
	require.NoError(t, err)
	encoded, err := json.Marshal(preview)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"objectKey":"`+s+`"`, "the dry run only changes the build's own notes")
	write("0.1,49", "Build 49 notes")
	own := editorial("package_version_id=?", builds["0.1,49"])
	require.Positive(t, own)
	require.Equal(t, map[string]string{"0.1,49": "build:" + strconv.FormatInt(own, 10), "0.1,48": "base:" + s, "0.1,47": "base:" + s}, scopes())
	require.False(t, gapped("latestBuildEditorial"))
	write("0.1,49", "Build 49 notes, edited")
	require.Equal(t, own, editorial("package_version_id=?", builds["0.1,49"]), "rewriting a build updates its own row")
	after, err := st.HistorySnapshot(ctx, content.Ref{Entity: "release", ID: shared})
	require.NoError(t, err)
	require.Equal(t, sharedState, after, "the shared note is unchanged")

	// The update event names the exact build, so its notes are tracked per build.
	activity := records("ListAdminCatalogChanges", Input{Params: map[string]any{"type": "updated"}})
	require.Len(t, activity, 1)
	require.Equal(t, "0.1,49", activity[0]["version"])
	require.Equal(t, "0.1", activity[0]["versionBase"])
	require.EqualValues(t, builds["0.1,49"], activity[0]["packageVersionId"])
	require.Equal(t, "build", activity[0]["editorialScope"])

	// Clearing stays in its scope: the build's clear keeps the shared note, and the reverse.
	write("0.1,48", "Build 48 notes")
	clear("0.1,49")
	require.Zero(t, editorial("package_version_id=?", builds["0.1,49"]))
	require.Equal(t, shared, editorial("package_version_id IS NULL"))
	after, err = st.HistorySnapshot(ctx, content.Ref{Entity: "release", ID: shared})
	require.NoError(t, err)
	require.Equal(t, sharedState, after)
	clear("0.1")
	require.Zero(t, editorial("package_version_id IS NULL"))
	require.Positive(t, editorial("package_version_id=?", builds["0.1,48"]), "clearing the shared note keeps build notes")
	write("0.1,49", "Build 49 notes again")
	require.Equal(t, own, editorial("package_version_id=?", builds["0.1,49"]), "a cleared build note comes back in place")
	got := scopes()
	require.Equal(t, "none:-", got["0.1,47"])
	require.Equal(t, "build:"+strconv.FormatInt(own, 10), got["0.1,49"])

	// Only recorded builds and base versions can hold notes.
	for _, version := range []string{"0.1,50", "0.2"} {
		_, err := call("UpsertReleaseNotes", Input{ID: pid, Version: version, Body: map[string]any{"sourceLocale": "en-US", "summary": "Missing"}})
		var appErr *domain.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, domain.CodeNotFound, appErr.Code)
	}
	var visible int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM releases WHERE package_id=? AND source='editorial' AND NOT hidden AND package_version_id IS NOT NULL", pid).Scan(&visible).Error)
	require.EqualValues(t, 2, visible)
}

func TestRevertLegacyReleaseRequestAfterBuildMigration(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	pkg, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"legacy","version":"1","name":["Legacy"]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	packages, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	_, err = svc.Execute(ctx, "UpsertReleaseNotes", Input{ID: packages["legacy"].ID, Version: "1", Body: map[string]any{"sourceLocale": "en-US", "summary": "Legacy notes", "sections": []any{}}})
	require.NoError(t, err)
	var revision struct {
		ID        int64
		RequestID string
		AfterData json.RawMessage
	}
	require.NoError(t, st.DB.Raw(`SELECT id,request_id,after_data FROM content_revisions WHERE entity='release' ORDER BY id DESC LIMIT 1`).Scan(&revision).Error)
	require.Positive(t, revision.ID)
	var legacy store.HistoryState
	require.NoError(t, json.Unmarshal(revision.AfterData, &legacy))
	for _, row := range legacy["releases"] {
		delete(row, "package_version_id")
	}
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`UPDATE content_revisions SET after_data=?::jsonb,after_hash=? WHERE id=?`, string(raw), store.HistoryHash(legacy), revision.ID).Error)
	_, err = svc.Execute(ctx, "RevertRequest", Input{RequestID: revision.RequestID})
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("releases").Count(&count).Error)
	require.Zero(t, count)
}
