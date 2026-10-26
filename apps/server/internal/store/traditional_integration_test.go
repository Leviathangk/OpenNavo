//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTraditionalDerivationAndBackfill(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	ref := content.Ref{Entity: "category", ID: 901}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(901,'derived-fixture','lucide:box','zh-CN')").Error)
	write := func(name string) error {
		return st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "zh-CN", map[string]any{"name": name}, true)
		})
	}
	require.NoError(t, write("软件"))
	value, err := st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "軟體", value.Fields["name"])
	require.NoError(t, write("鼠标"))
	value, err = st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "滑鼠", value.Fields["name"])
	require.Error(t, st.WriteContentText(ctx, ref, "zh-Hant", map[string]any{"name": "override"}, false))
	counts, err := st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	require.Empty(t, counts)
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.NotContains(t, source.Targets, "zh-Hant", "Traditional Chinese is derived, never translated")
	// Existing manually edited Traditional rows are backed up; preview never persists.
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(902,'existing-fixture','lucide:box','zh-CN'); INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(902,'zh-CN','软件','zh-CN','source'),(902,'zh-Hant','原有文字','zh-CN','manual')").Error)
	counts, err = st.BackfillTraditional(ctx, true)
	require.NoError(t, err)
	require.Equal(t, 1, counts["category"])
	old, err := st.TranslationValue(ctx, content.Ref{Entity: "category", ID: 902}, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "原有文字", old.Fields["name"])
	counts, err = st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 1, counts["category"])
	var backup string
	require.NoError(t, st.DB.Raw("SELECT before_data->>'name' FROM derived_traditional WHERE entity='category' AND object_key='902'").Scan(&backup).Error)
	require.Equal(t, "原有文字", backup)
	// Rollback restores retained manual text, but must not overwrite later edits.
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='后续修改' WHERE category_id=901 AND locale='zh-Hant'").Error)
	restored, err := st.RollbackTraditional(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 1, restored["restored"])
	require.Equal(t, 1, restored["conflict"])
	old, err = st.TranslationValue(ctx, content.Ref{Entity: "category", ID: 902}, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "原有文字", old.Fields["name"])
	// A new upgrade cycle must back up edits made after the previous rollback.
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='新人工譯文' WHERE category_id=902 AND locale='zh-Hant'").Error)
	_, err = st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	_, err = st.RollbackTraditional(ctx, false)
	require.NoError(t, err)
	old, err = st.TranslationValue(ctx, content.Ref{Entity: "category", ID: 902}, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "新人工譯文", old.Fields["name"])

}
func TestTraditionalWaitsForFreshSimplified(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	ref := content.Ref{Entity: "category", ID: 903}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(903,'english-fixture','lucide:box','en-US');INSERT INTO category_i18n(category_id,locale,name,source_locale,status,source_hash) VALUES(903,'en-US','Software','en-US','source',''),(903,'zh-CN','软件','en-US','machine','outdated')").Error)
	counts, err := st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	require.Empty(t, counts)
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET source_hash=? WHERE category_id=903 AND locale='zh-CN'", source.Hash).Error)
	counts, err = st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 1, counts["category"])
	value, err := st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "軟體", value.Fields["name"])
	// A Simplified correction must regenerate even though the English source hash is unchanged.
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "zh-CN", map[string]any{"name": "鼠标"}, false)
	}))
	value, err = st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "滑鼠", value.Fields["name"])
	require.Equal(t, source.Hash, value.SourceHash)

}

func TestTraditionalWithdrawsInvalidDependency(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	ref := content.Ref{Entity: "category", ID: 904}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(904,'withdraw-fixture','lucide:box','en-US'); INSERT INTO category_i18n(category_id,locale,name,source_locale,status,source_hash) VALUES(904,'en-US','Software','en-US','source',''),(904,'zh-CN','软件','en-US','pending',''),(904,'zh-Hant','舊文字','en-US','manual','')").Error)
	counts, err := st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 1, counts["category"])
	value, err := st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "missing", value.Status)
	restored, err := st.RollbackTraditional(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 1, restored["restored"])
	value, err = st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "舊文字", value.Fields["name"])
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='新人工譯文' WHERE category_id=904 AND locale='zh-Hant'").Error)
	_, err = st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	_, err = st.RollbackTraditional(ctx, false)
	require.NoError(t, err)
	value, err = st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "新人工譯文", value.Fields["name"])
}

func TestTraditionalBackfillContinuesAfterInvalidContent(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(910,'overflow-fixture','lucide:box','zh-CN'),(911,'following-fixture','lucide:box','zh-CN')").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO category_i18n(category_id,locale,name,description,source_locale,status) VALUES(910,'zh-CN','软件',?,'zh-CN','source'),(911,'zh-CN','鼠标','','zh-CN','source')", strings.Repeat("内存", 400)).Error)
	for _, preview := range []bool{true, false, false} {
		counts, err := st.BackfillTraditional(ctx, preview)
		require.NoError(t, err)
		require.Equal(t, 1, counts["category:failed"])
		if preview {
			require.Equal(t, 1, counts["category"])
		}
	}
	value, err := st.TranslationValue(ctx, content.Ref{Entity: "category", ID: 911}, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "滑鼠", value.Fields["name"])
}

func TestTraditionalRollbackContinuesAfterDeletedParent(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(912,'deleted-fixture','lucide:box','en-US'),(913,'remaining-fixture','lucide:box','en-US')").Error)
	for _, id := range []int{912, 913} {
		require.NoError(t, st.DB.Exec("INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(?,'en-US','Software','en-US','source'),(?,'zh-CN','软件','en-US','pending'),(?,'zh-Hant','舊文字','en-US','manual')", id, id, id).Error)
	}
	_, err := st.BackfillTraditional(ctx, false)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("DELETE FROM categories WHERE id=912").Error)
	for _, preview := range []bool{true, false} {
		counts, err := st.RollbackTraditional(ctx, preview)
		require.NoError(t, err)
		require.Equal(t, 1, counts["conflict"])
		require.Equal(t, 1, counts["restored"])
	}
}

func TestTraditionalRollbackCompletesAbsentRowCycle(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	ref := content.Ref{Entity: "category", ID: 920}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(920,'empty-cycle','lucide:box','en-US')").Error)
	write := func(locale, value string, source bool) {
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).WriteContentText(ctx, ref, locale, map[string]any{"name": value}, source)
		}))
	}
	write("en-US", "Software", true)
	write("zh-CN", "软件", false)
	write("en-US", "New software", true) // Stale Simplified withdraws derived row.
	var missing bool
	require.NoError(t, st.DB.Raw("SELECT before_data IS NULL AND after_data='null'::jsonb FROM derived_traditional WHERE entity='category' AND object_key='920'").Scan(&missing).Error)
	require.True(t, missing)
	// A row appearing after withdrawal is a real conflict, even for null/null receipts.
	require.NoError(t, st.DB.Exec("INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(920,'zh-Hant','並行修改','en-US','manual')").Error)
	conflicts, err := st.RollbackTraditional(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 1, conflicts["conflict"])
	var stillActive bool
	require.NoError(t, st.DB.Raw("SELECT restored_at IS NULL FROM derived_traditional WHERE entity='category' AND object_key='920'").Scan(&stillActive).Error)
	require.True(t, stillActive)
	require.NoError(t, st.DB.Exec("DELETE FROM category_i18n WHERE category_id=920 AND locale='zh-Hant'").Error)
	for _, preview := range []bool{true, false} {
		counts, err := st.RollbackTraditional(ctx, preview)
		require.NoError(t, err)
		require.Equal(t, 1, counts["restored"])
		require.Zero(t, counts["conflict"])
		var finished bool
		require.NoError(t, st.DB.Raw("SELECT restored_at IS NOT NULL FROM derived_traditional WHERE entity='category' AND object_key='920'").Scan(&finished).Error)
		require.Equal(t, !preview, finished)
	}
	// Old version permits an independent manual Traditional translation.
	require.NoError(t, st.DB.Exec("INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(920,'zh-Hant','新人工譯文','en-US','manual')").Error)
	write("zh-CN", "新软件", false)
	_, err = st.RollbackTraditional(ctx, false)
	require.NoError(t, err)
	value, err := st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "新人工譯文", value.Fields["name"])
}
