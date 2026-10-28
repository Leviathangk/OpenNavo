//go:build integration

package management

import (
	"context"
	"strconv"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReleaseNotesTranslationBatchRejectsWikitextIndependently(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ref := content.Ref{Entity: "release", ID: 420}
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(992,'cask','wikitext-fixture','wikitext-fixture','homebrew/cask','Fixture','1.0','1.0','{}','fixture');
 INSERT INTO releases(id,package_id,source,source_key,version,source_locale) VALUES(420,992,'editorial','editorial:1.0','1.0','en-US')`).Error)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"summary": "Fixed export.", "bodyMarkdown": "## Fixes\n\n- Fixed export."}, true)
	}))
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,source_hash,summary)
 VALUES(420,'ja-JP','en-US','manual',?,'保存済みの翻訳')`, source.Hash).Error)
	previous, err := st.TranslationValue(ctx, ref, "ja-JP")
	require.NoError(t, err)
	request := translationBatch{Ref: ref, Model: "test-model", Translations: []translationItem{
		{Locale: "ja-JP", SourceHash: source.Hash, ExpectedRevision: previous.Revision, Fields: map[string]any{"summary": "[[Category:Release]]"}},
		{Locale: "de-DE", SourceHash: source.Hash, Fields: map[string]any{"summary": "Export korrigiert."}},
	}}
	result, err := svc.Execute(hermesContext("translation:review", "changelog:release:edit"), "WriteTranslations", Input{Body: hermesMap(request)})
	require.NoError(t, err, "a rejected translation must not abort the batch")
	results := hermesMap(result)["results"].([]any)
	require.Equal(t, "failed", results[0].(map[string]any)["status"])
	require.Contains(t, results[0].(map[string]any)["reason"], "release_notes_wikitext_convert_to_markdown:category_link:summary")
	require.Equal(t, "written", results[1].(map[string]any)["status"])
	after, err := st.TranslationValue(ctx, ref, "ja-JP")
	require.NoError(t, err)
	require.Equal(t, previous, after, "a rejected translation leaves the existing row untouched")
	clean, err := st.TranslationValue(ctx, ref, "de-DE")
	require.NoError(t, err)
	require.Equal(t, "Export korrigiert.", clean.Fields["summary"])
	var logged int64
	require.NoError(t, st.DB.Table("agent_write_events").Where("operation='WriteTranslations' AND result='rejected' AND reason LIKE ?", "ja-JP:%release_notes_wikitext_convert_to_markdown%").Count(&logged).Error)
	require.EqualValues(t, 1, logged)
}

func TestReleaseNotesHistoryRestoreValidatesOnlyChangedText(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}

	t.Run("unchanged legacy parent body survives hidden revert", func(t *testing.T) {
		insertReleaseHistoryFixture(t, st, 993, 421, "legacy-hidden", "{{TopMenu}}")
		_, err := svc.Execute(ctx, "UpdateRelease", Input{ID: 421, Body: map[string]any{"hidden": true}})
		require.NoError(t, err)
		requestID := latestReleaseRequestID(t, st, 421)
		_, err = svc.Execute(ctx, "RevertRequest", Input{RequestID: requestID})
		require.NoError(t, err)
		var row struct {
			Hidden       bool
			BodyMarkdown string
		}
		require.NoError(t, st.DB.Raw("SELECT hidden,body_markdown FROM releases WHERE id=?", 421).Scan(&row).Error)
		require.False(t, row.Hidden)
		require.Equal(t, "{{TopMenu}}", row.BodyMarkdown)
	})

	t.Run("unchanged legacy locale does not block clean locale revert", func(t *testing.T) {
		insertReleaseHistoryFixture(t, st, 994, 422, "legacy-locale", "Clean release body")
		require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,summary,sections,body_markdown)
 VALUES(422,'en-US','en-US','source','Clean summary','[]','Clean locale body'),
       (422,'ja-JP','en-US','manual','{{TopMenu}}','[]','Legacy locale body')`).Error)
		_, err := svc.Execute(ctx, "UpdateReleaseI18n", Input{ID: 422, Locale: "en-US", Body: map[string]any{
			"summary":      "Changed summary",
			"sections":     []any{},
			"bodyMarkdown": "Clean locale body",
		}})
		require.NoError(t, err)
		requestID := latestReleaseRequestID(t, st, 422)
		_, err = svc.Execute(ctx, "RevertRequest", Input{RequestID: requestID})
		require.NoError(t, err)
		var rows []struct {
			Locale  string
			Summary string
		}
		require.NoError(t, st.DB.Raw("SELECT locale,summary FROM release_i18n WHERE release_id=? ORDER BY locale", 422).Scan(&rows).Error)
		require.Equal(t, []struct {
			Locale  string
			Summary string
		}{{"en-US", "Clean summary"}, {"ja-JP", "{{TopMenu}}"}}, rows)
	})

	t.Run("contaminated text is rejected without writes", func(t *testing.T) {
		insertReleaseHistoryFixture(t, st, 995, 423, "reject-contaminated", "Clean release body")
		require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,summary,body_markdown)
 VALUES(423,'en-US','en-US','source','Clean summary','Clean locale body')`).Error)
		ref := content.Ref{Entity: "release", ID: 423}
		before, err := st.HistorySnapshot(ctx, ref)
		require.NoError(t, err)
		for _, tc := range []struct {
			table, field string
			value        any
			newLocale    bool
		}{
			{"releases", "title", "{{TopMenu}}", false},
			{"releases", "body_markdown", "{{TopMenu}}", false},
			{"release_i18n", "title", "{{TopMenu}}", false},
			{"release_i18n", "summary", "{{TopMenu}}", false},
			{"release_i18n", "sections", []any{map[string]any{"area": "{{TopMenu}}", "items": []any{}}}, false},
			{"release_i18n", "body_markdown", "{{TopMenu}}", false},
			{"release_i18n", "body_markdown", "{{TopMenu}}", true},
		} {
			t.Run(tc.table+"/"+tc.field+"/newLocale="+strconv.FormatBool(tc.newLocale), func(t *testing.T) {
				target, err := st.HistorySnapshot(ctx, ref)
				require.NoError(t, err)
				target["releases"][0]["hidden"] = true
				target[tc.table][0][tc.field] = tc.value
				if tc.newLocale {
					target[tc.table][0]["locale"] = "de-DE"
				}
				err = st.RestoreHistoryState(ctx, ref, target)
				require.ErrorContains(t, err, "release_notes_wikitext_convert_to_markdown:navigation_template:"+tc.field)
				require.NotContains(t, err.Error(), "{{TopMenu}}")
				after, err := st.HistorySnapshot(ctx, ref)
				require.NoError(t, err)
				require.Equal(t, before, after, "rejection must precede deletes and updates, even without a transaction")
			})
		}
		target, err := st.HistorySnapshot(ctx, ref)
		require.NoError(t, err)
		target["releases"][0]["body_markdown"] = "{{TopMenu}}"
		require.NoError(t, st.RestoreHistoryState(ctx, ref, nil), "deleting needs no text validation")
		require.ErrorContains(t, st.RestoreHistoryState(ctx, ref, target), "navigation_template:body_markdown")
		after, err := st.HistorySnapshot(ctx, ref)
		require.NoError(t, err)
		require.Nil(t, after, "recreating a contaminated release must leave it deleted")
	})
}

func insertReleaseHistoryFixture(t *testing.T, st *store.Store, packageID, releaseID int64, token, body string) {
	t.Helper()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(?, 'cask', ?, ?, 'homebrew/cask', ?, '1.0', '1.0', '{}', 'history-fixture')`, packageID, token, token, token).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(id,package_id,source,source_key,version,source_locale,body_markdown,hidden)
 VALUES(?,?,'manual',?,'1.0','en-US',?,false)`, releaseID, packageID, token, body).Error)
}

func latestReleaseRequestID(t *testing.T, st *store.Store, releaseID int64) string {
	t.Helper()
	var requestID string
	require.NoError(t, st.DB.Raw("SELECT request_id FROM content_revisions WHERE entity='release' AND object_key=? ORDER BY id DESC LIMIT 1", strconv.FormatInt(releaseID, 10)).Scan(&requestID).Error)
	require.NotEmpty(t, requestID)
	return requestID
}
