//go:build integration

package urlnotify

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTraditionalBackfillOnlyPlansTraditionalPages(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','locale-app','locale-app','homebrew/cask','Locale app','1','1','{}','fixture')`).Error)
	ref := content.Ref{Entity: "package", ID: 701}
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "zh-CN", map[string]any{"summary": "需要管理员权限"}, true)
	}))
	require.NoError(t, st.DB.Exec(`DELETE FROM package_i18n WHERE package_id=701 AND locale='zh-Hant'; DELETE FROM derived_traditional; DELETE FROM catalog_changes`).Error)
	counts, err := st.RunTraditionalBackfill(ctx, store.TraditionalBackfill{})
	require.NoError(t, err)
	require.Equal(t, 1, counts["package"])
	n := &Notifier{Store: st, Config: config.Config{WebBaseURL: "https://site.example"}, Batch: 2000}
	changes, err := st.PendingURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, []string{"zh-Hant"}, changes[0].Locales)
	plan, count, err := n.catalogPlan(ctx, 0, changes[0].Seq, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, []string{"/zh-hant/apps/locale-app", "/zh-hant/recent"}, plan.Paths)
	require.Equal(t, []string{"site.example/zh-hant/apps/locale-app", "site.example/zh-hant/recent"}, plan.PurgePrefixes)
	require.Empty(t, plan.IndexNowURLs)
	notes, err := n.notifications(ctx, plan)
	require.NoError(t, err)
	require.Len(t, notes, 2)
	for _, note := range notes {
		require.False(t, note.Announce)
		require.Equal(t, "zh-Hant", note.Locale)
	}
	// A single old writer in the same batch restores the original all-language behavior.
	require.NoError(t, st.AppendContentChange(ctx, 701))
	changes, err = st.PendingURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	require.Nil(t, changes[1].Locales)
	plan, _, err = n.catalogPlan(ctx, 0, changes[1].Seq, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, plan.Paths, 2*len(seo.Locales))
	require.Contains(t, plan.Paths, "/apps/locale-app")
}

func TestSingleTranslationAndContentFeedLocales(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'link','translated','translated','manual','Translated','1','1','{}','fixture')`).Error)
	ref := content.Ref{Entity: "package", ID: 701}
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"summary": "A text editor for developers"}, true)
	}))
	require.NoError(t, st.DB.Exec("DELETE FROM catalog_changes").Error)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "ja-JP", map[string]any{"summary": "開発者向けのテキストエディタ"}, false)
	}))
	changes, err := st.PendingURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, []string{"ja-JP"}, changes[0].Locales)
	require.NoError(t, st.EnqueueArticlePages(ctx, []string{"/articles/example"}, "zh-Hant"))
	contentChanges, err := st.PendingContentURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	require.NotEmpty(t, contentChanges)
	for _, change := range contentChanges {
		require.Equal(t, []string{"zh-Hant"}, change.Locales)
	}
	plan := ContentPlan(contentChanges, "https://site.example")
	require.Contains(t, plan.Paths, "/zh-hant/articles/example")
	require.Equal(t, []string{"site.example/zh-hant/articles/example", "site.example/zh-hant/articles/feed.xml"}, plan.PurgePrefixes)
}

func TestCollectionTriggerScopesTranslatedRows(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO collections(id,slug,source_locale) VALUES(701,'locale-collection','en-US'); DELETE FROM content_url_changes`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO collection_i18n(collection_id,locale,title,source_locale,status) VALUES(701,'zh-Hant','軟體','en-US','machine')`).Error)
	changes, err := st.PendingContentURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, []string{"zh-Hant"}, changes[0].Locales)
	plan := ContentPlan(changes, "https://site.example")
	require.ElementsMatch(t, []string{"site.example/zh-hant/collections/locale-collection", "site.example/zh-hant/discover"}, plan.PurgePrefixes)
	require.NoError(t, st.DB.Exec(`DELETE FROM content_url_changes; INSERT INTO collection_i18n(collection_id,locale,title,source_locale,status) VALUES(701,'en-US','Software','en-US','source')`).Error)
	changes, err = st.PendingContentURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Nil(t, changes[0].Locales)
}
