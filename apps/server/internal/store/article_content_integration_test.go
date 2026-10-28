//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestArticlePathsMissingArticle(t *testing.T) {
	st := testutil.NewSEOStore(t)
	for _, id := range []int64{0, 1} {
		paths, err := st.ArticlePaths(context.Background(), id)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		require.Nil(t, paths)
	}
}

func TestManualArticleContentRefreshesPages(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`
 BEGIN;
 INSERT INTO article_authors(id,slug,name) OVERRIDING SYSTEM VALUE VALUES(10,'test-author','Test Author');
 INSERT INTO article_author_i18n(author_id,locale,role,bio,status) VALUES(10,'en-US','Editor','Test biography','source');
 INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) OVERRIDING SYSTEM VALUE VALUES(20,'test-article','published',30,30,now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type,author_id) OVERRIDING SYSTEM VALUE VALUES(30,20,1,'en-US','news',10);
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status) VALUES(30,'en-US','Test article','en-US','source');
 COMMIT;
 `).Error)
	for _, entity := range []string{"article", "article_author"} {
		t.Run(entity, func(t *testing.T) {
			require.NoError(t, st.DB.Exec("DELETE FROM content_url_changes").Error)
			require.NoError(t, st.DB.Exec("DELETE FROM job_outbox").Error)
			ref := content.Ref{Entity: entity, ID: 30}
			fields := map[string]any{"title": "修订后的文章标题"}
			if entity == "article_author" {
				ref.ID = 10
				fields = map[string]any{"role": "编辑", "bio": "修订后的作者介绍"}
			}
			require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
				return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "zh-CN", fields, false)
			}))
			var paths []string
			// The derived Traditional Chinese write queues the same language-neutral
			// paths again; the URL feed merges duplicates into one plan.
			require.NoError(t, st.DB.Raw("SELECT DISTINCT unnest(paths) FROM content_url_changes WHERE announce").Scan(&paths).Error)
			expected := []string{"/authors/test-author", "/articles/test-article"}
			if entity == "article" {
				expected = append(expected, "/articles")
			}
			require.ElementsMatch(t, expected, paths)
			var feeds int64
			require.NoError(t, st.DB.Raw("SELECT count(DISTINCT paths) FROM content_url_changes WHERE paths=ARRAY['/articles/feed.xml'] AND NOT announce").Scan(&feeds).Error)
			require.EqualValues(t, 1, feeds)
			var payload string
			require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE job_type='cache:invalidate'").Scan(&payload).Error)
			require.Contains(t, payload, "c:art:*")
			var saved string
			if entity == "article" {
				require.NoError(t, st.DB.Raw("SELECT title FROM article_revision_i18n WHERE revision_id=30 AND locale='zh-CN'").Scan(&saved).Error)
				require.Equal(t, "修订后的文章标题", saved)
			} else {
				require.NoError(t, st.DB.Raw("SELECT bio FROM article_author_i18n WHERE author_id=10 AND locale='zh-CN'").Scan(&saved).Error)
				require.Equal(t, "修订后的作者介绍", saved)
			}
		})
	}
}
