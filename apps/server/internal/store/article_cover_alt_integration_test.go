//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestArticleCoverAltTranslationFallbackAndRetention(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	cover, err := st.SaveAsset(ctx, store.Asset{Kind: "cover", StorageKey: "fixture/cover.png", URL: "https://cdn.example.test/cover.png", MIME: "image/png", SHA256: strings.Repeat("a", 64), Bytes: 10}, nil)
	require.NoError(t, err)
	var articleID, revisionID int64
	sourceAlt := "源语言封面说明"
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		local := &store.Store{DB: tx, Redis: st.Redis}
		var err error
		articleID, err = local.CreateArticleRecord(ctx, "cover-fixture", nil)
		if err != nil {
			return err
		}
		revisionID, _, err = local.InsertArticleRevision(ctx, articleID, store.ArticleDraft{SourceLocale: "zh-CN", Type: "tutorial", Title: "源语言标题", CoverAssetID: &cover.ID, CoverAlt: &sourceAlt})
		if err != nil {
			return err
		}
		return tx.Exec("UPDATE articles SET working_revision_id=?,published_revision_id=?,status='published',first_published_at=now() WHERE id=?", revisionID, revisionID, articleID).Error
	}))
	ref := content.Ref{Entity: "article", ID: revisionID}
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, sourceAlt, source.Fields["cover_alt"])
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx, Redis: st.Redis}).WriteContentText(ctx, ref, "en-US", map[string]any{"title": "Cover title", "coverAlt": "English cover description"}, false)
	}))
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx, Redis: st.Redis}).WriteContentText(ctx, ref, "ja-JP", map[string]any{"title": "表紙の見出し", "coverAlt": "日本語の表紙説明"}, false)
	}))
	read := func(locale, want string) {
		t.Helper()
		row, found, err := st.PublicArticle(ctx, "cover-fixture", locale)
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, row.CoverAlt)
		require.Equal(t, want, *row.CoverAlt)
		rows, n, err := st.PublicArticles(ctx, locale, "", 0, 1, 20)
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
		require.Equal(t, want, *rows[0].CoverAlt)
	}
	read("ja-JP", "日本語の表紙説明")
	read("de-DE", "English cover description")
	read("zh-CN", sourceAlt)
	require.NoError(t, st.DB.Exec("UPDATE article_revision_i18n SET withheld=true WHERE revision_id=? AND locale='ja-JP'", revisionID).Error)
	read("ja-JP", "English cover description")
	require.NoError(t, st.DB.Exec("UPDATE article_revision_i18n SET cover_alt='  ' WHERE revision_id=? AND locale='en-US'", revisionID).Error)
	read("ja-JP", sourceAlt)
	require.NoError(t, store.ValidateContentFields("article", map[string]any{"coverAlt": "Localized description"}))
	require.Error(t, store.ValidateContentFields("article", map[string]any{"coverAlt": strings.Repeat("x", 301)}))
	require.NoError(t, st.DB.Exec("UPDATE assets SET created_at=now()-interval '60 days' WHERE id=?", cover.ID).Error)
	n, err := st.PruneAssets(ctx, time.Now().AddDate(0, 0, -30), func(context.Context, store.Asset) error { t.Fatal("article cover was pruned"); return nil })
	require.NoError(t, err)
	require.Zero(t, n)
}
