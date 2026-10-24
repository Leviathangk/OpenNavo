//go:build integration

package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The page-state job stores each versions page's notes facts; they must equal what PopulatePageNotes counts live,
// and the backfill must restore them for rows written before the columns existed.
func TestVersionsPageNotesFactsMatchLiveCountsAndBackfill(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	var id int64
	require.NoError(t, st.DB.Raw(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale,first_seen_at)
 VALUES('cask','notes-fixture','notes-fixture','homebrew/cask','Notes Fixture','30.0','30.0','{}','fixture','en-US','2020-01-01') RETURNING id`).Scan(&id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale)
 VALUES(?,'en-US','Notes Fixture','An editor','A useful editor','source','human','en-US')`, id).Error)
	// Thirty versions span two versions pages; version 30 has a German translation that only German counts.
	var latest int64
	for v := 1; v <= 30; v++ {
		version := fmt.Sprintf("%d.0", v)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base,first_seen_at,brew_committed_at) VALUES(?,?,?,?,?)`,
			id, version, version, time.Date(2026, 1, 1, v, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, v, 0, 0, 0, time.UTC)).Error)
		require.NoError(t, st.DB.Raw(`INSERT INTO releases(package_id,source,source_key,version,body_markdown,source_url,source_locale,published_at)
 VALUES(?,'manual',?,?,?,'https://vendor.example/notes','en-US',?) RETURNING id`, id, version, version,
			strings.Repeat("Fixed a crash when opening large files. ", v), time.Date(2026, 1, 1, v, 0, 0, 0, time.UTC)).Scan(&latest).Error)
	}
	require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,body_markdown,status,source_locale,source_hash)
 VALUES(?,'de-DE',?,'machine','en-US',repeat('b',64))`, latest, strings.Repeat("Absturz beim Öffnen großer Dateien behoben. ", 12)).Error)

	count, err := st.RefreshPackagePageStates(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	stored := func() map[string]map[int]seo.NotesFacts {
		var states []store.PageState
		require.NoError(t, st.DB.Raw(`SELECT package_id,locale,page_kind,page_number,content_hash,content_modified_at,notes_chars,notes_entries
 FROM package_page_state WHERE package_id=? AND page_kind='versions'`, id).Scan(&states).Error)
		out := map[string]map[int]seo.NotesFacts{}
		for _, state := range states {
			require.NotNil(t, state.NotesChars, "%s page %d", state.Locale, state.PageNumber)
			require.NotNil(t, state.NotesEntries)
			if out[state.Locale] == nil {
				out[state.Locale] = map[int]seo.NotesFacts{}
			}
			out[state.Locale][state.PageNumber] = seo.NotesFacts{Chars: *state.NotesChars, Entries: *state.NotesEntries}
		}
		return out
	}
	facts := stored()
	require.Len(t, facts, len(seo.Locales))
	row, found, err := st.IndexRowByID(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	for page := 1; page <= 2; page++ {
		rows := []store.IndexRow{row}
		require.NoError(t, st.PopulatePageNotes(ctx, rows, page))
		for _, locale := range seo.Locales {
			require.Equal(t, rows[0].NotesPages[page][locale], facts[locale][page], "%s page %d", locale, page)
		}
	}
	require.Equal(t, seo.VersionsPageSize, facts["en-US"][1].Entries)
	require.Equal(t, 6, facts["en-US"][2].Entries)
	require.Greater(t, facts["en-US"][1].Chars, facts["en-US"][2].Chars, "page 1 holds the newest, longest notes")
	require.Equal(t, 1, facts["de-DE"][1].Entries, "the German translation counts in German")
	require.Equal(t, seo.NotesFacts{}, facts["ja-JP"][1], "an English fallback is never a translation")

	// Rows written before notes facts were stored are filled by the backfill, then nothing is left to do.
	// notes_entries is NULL for every row written before it replaced the source-URL flag (migration 00104).
	require.NoError(t, st.DB.Exec(`UPDATE package_page_state SET notes_entries=NULL WHERE package_id=?`, id).Error)
	filled, err := st.BackfillPageNotes(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, filled)
	require.Equal(t, facts, stored())
	filled, err = st.BackfillPageNotes(ctx, 10)
	require.NoError(t, err)
	require.Zero(t, filled)

	// Fewer notes after a release disappears: the next refresh rewrites the facts, and a page past the end is empty.
	require.NoError(t, st.DB.Exec(`DELETE FROM releases WHERE package_id=? AND version IN ('1.0','2.0','3.0','4.0','5.0','6.0')`, id).Error)
	require.NoError(t, st.DB.Exec(`DELETE FROM package_versions WHERE package_id=? AND version IN ('1.0','2.0','3.0','4.0','5.0','6.0')`, id).Error)
	count, err = st.RefreshPackagePageStates(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	after := stored()
	require.Equal(t, seo.NotesFacts{}, after["en-US"][2], "24 entries fit on page 1; the old page 2 is empty")
	require.Equal(t, facts["en-US"][1], after["en-US"][1])
}

// Editorial notes keep their text in release_i18n and never carry a source URL. They count like any other
// notes, except notes that only say the version's changes are not known, which count in no language.
func TestEditorialNotesCountUnlessTheyOnlySayNothingIsKnown(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	var id int64
	require.NoError(t, st.DB.Raw(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale,first_seen_at)
 VALUES('cask','editorial-notes','editorial-notes','homebrew/cask','Editorial Notes','7.0','7.0','{}','fixture','en-US','2020-01-01') RETURNING id`).Scan(&id).Error)
	notes := map[string]struct{ summary, body, sections string }{
		"1.0": {"Adds tabs", "Adds tabs to the main window and remembers the last open tab between launches.", `[{"area":"New","items":["Tabs in the main window"]}]`},
		"2.0": {"Faster sync", "Syncing large libraries is about twice as fast, and interrupted syncs resume where they stopped.", `[]`},
		"3.0": {"Fixes crashes", "Fixes a crash when importing damaged files and a hang when the network drops during upload.", `[]`},
		"4.0": {"No details", "Detailed official release notes are unavailable. No changes are inferred from the version number.", `[]`},
		"5.0": {"Changes", "### Changes\n\n- Not provided: exact changes for this version are unverified.", `[{"area":"Changes","items":["Not provided: exact changes for this version are unverified."]}]`},
		// A change stated mid-sentence beside an aside counts; links under a placeholder do not.
		"6.0": {"Vertical tabs", "Separate version details were not provided.\n\nThis release adds vertical tabs and fixes a crash when importing large files.", `[]`},
		"7.0": {"No details", "Official release notes are unavailable.\n\n- [View the release history](https://example.com/history)\n- [Read the version details](https://example.com/details)", `[]`},
	}
	for _, version := range []string{"1.0", "2.0", "3.0", "4.0", "5.0", "6.0", "7.0"} {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base,first_seen_at) VALUES(?,?,?,'2026-01-01')`, id, version, version).Error)
		var release int64
		require.NoError(t, st.DB.Raw(`INSERT INTO releases(package_id,source,source_key,version,source_locale) VALUES(?,'editorial',?,?,'en-US') RETURNING id`,
			id, "editorial:"+version, version).Scan(&release).Error)
		note := notes[version]
		require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,summary,body_markdown,sections,status) VALUES(?,'en-US','en-US',?,?,?::jsonb,'source')`,
			release, note.summary, note.body, note.sections).Error)
	}
	row, found, err := st.IndexRowByID(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	rows := []store.IndexRow{row}
	require.NoError(t, st.PopulatePageNotes(ctx, rows, 1))
	english := rows[0].NotesPages[1]["en-US"]
	require.Equal(t, 4, english.Entries, "the three notes that only say nothing is known do not count")
	require.GreaterOrEqual(t, english.Chars, 200)
	require.Equal(t, seo.NotesFacts{}, rows[0].NotesPages[1]["ja-JP"], "no Japanese notes exist")
}
