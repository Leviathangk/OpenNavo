//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTraditionalExpansionCommitsValidLocalizedSource(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	ref := content.Ref{Entity: "category", ID: 901}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(901,'expanding-text','lucide:box','zh-CN')").Error)
	fields := map[string]any{
		"name": strings.Repeat("内存", 15), "description": strings.Repeat("内存", 100),
		"seoTitle": strings.Repeat("内存", 60), "seoDescription": strings.Repeat("内存", 150),
	}
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteLocalized(ctx, "category", ref.ID, 0,
			map[string]any{"sourceLocale": "zh-CN", "i18n": map[string]any{"zh-CN": fields}}, true)
	}))
	derived, err := st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, 45, utf8.RuneCountInString(derived.Fields["name"].(string)))
	for column, count := range map[string]int{"name": 15, "description": 100, "seoTitle": 60, "seoDescription": 150} {
		require.Equal(t, strings.Repeat("記憶體", count), derived.Fields[column])
	}
	source, err := st.TranslationValue(ctx, ref, "zh-CN")
	require.NoError(t, err)
	require.Equal(t, fields["name"], source.Fields["name"])
	// Authoring remains bounded and zh-Hant remains read-only.
	err = st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "zh-CN", map[string]any{"name": strings.Repeat("字", 31)}, true)
	})
	require.ErrorContains(t, err, "field_too_long")
	require.Error(t, st.WriteContentText(ctx, ref, "zh-Hant", map[string]any{"name": "override"}, false))
	require.Error(t, st.DB.Exec("INSERT INTO category_i18n(category_id,locale,name,seo_title,source_locale,status) VALUES(901,'de-DE','Name',?,'zh-CN','manual')", strings.Repeat("x", 121)).Error)
	after, err := st.TranslationValue(ctx, ref, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, derived.Fields, after.Fields)
}
