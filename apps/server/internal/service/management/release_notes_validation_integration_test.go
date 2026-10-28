//go:build integration

package management

import (
	"context"
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
