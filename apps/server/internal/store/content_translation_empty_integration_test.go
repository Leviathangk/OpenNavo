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

func TestEmptySourceRemovesPublishedTranslations(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO article_authors(id,slug,name,source_locale) VALUES(991,'empty-source','Author','en-US')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO article_author_i18n(author_id,locale,role,bio,status,source_locale) VALUES(991,'en-US','Editor','Biography','source','en-US'),(991,'zh-CN','编辑','旧介绍','manual','en-US')`).Error)
	schedule := func() {
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, content.Ref{Entity: "article_author", ID: 991})
		}))
	}
	schedule()
	require.NoError(t, st.DB.Exec(`UPDATE article_author_i18n SET role='',bio='' WHERE author_id=991 AND locale='en-US'`).Error)
	schedule()
	var count int64
	require.NoError(t, st.DB.Table("article_author_i18n").Where("author_id=991 AND locale<>'en-US'").Count(&count).Error)
	require.Zero(t, count)
	schedule()
	require.NoError(t, st.DB.Exec(`UPDATE article_author_i18n SET bio='New biography' WHERE author_id=991 AND locale='en-US'`).Error)
	schedule()
	source, err := st.ReadContentSource(ctx, content.Ref{Entity: "article_author", ID: 991})
	require.NoError(t, err)
	require.Contains(t, source.Targets, "zh-CN", "a new source needs its translations again")
	var recorded string
	require.NoError(t, st.DB.Raw("SELECT source_hash FROM content_translation_sources WHERE entity='article_author' AND object_key='991'").Scan(&recorded).Error)
	require.Equal(t, source.Hash, recorded)
}
