//go:build integration

package store_test

import (
	"context"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBrowseEligibleLanguageFontAndPagination(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,source_locale) SELECT 'cask','item-'||n,'item-'||n,'homebrew/cask','Åpp '||lpad(n::text,3,'0'),'1','1','{}','fixture','Useful app','en-US' FROM generate_series(1,302) n`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET stale_disabled=true WHERE token='item-1'`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET disabled=true WHERE token='item-2'`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET is_font=true WHERE token='item-3'`).Error)
	policy := seo.Policy{Mode: "shadow", TranslationMode: "shadow"}
	root, err := st.Browse(ctx, "apps", "", "en-US", 1, policy)
	require.NoError(t, err)
	require.Len(t, root.Buckets, 1)
	require.Equal(t, 300, root.Buckets[0].Count)
	require.Empty(t, root.Items)
	page, err := st.Browse(ctx, "apps", "a", "en-US", 1, policy)
	require.NoError(t, err)
	require.Len(t, page.Items, 300)
	require.Equal(t, "item-1", page.Items[0].Token)
	_, err = st.Browse(ctx, "apps", "a", "en-US", 2, policy)
	require.Error(t, err)
	fonts, err := st.Browse(ctx, "fonts", "a", "en-US", 1, policy)
	require.NoError(t, err)
	require.Len(t, fonts.Items, 1)
	untranslated, err := st.Browse(ctx, "apps", "", "de-DE", 1, policy)
	require.NoError(t, err)
	require.Empty(t, untranslated.Buckets)
}
