//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSitemapFactsAndPolicyFromStorage(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,name,version,desc_en,source_locale,installs_30d,rank_30d,is_font,tap,version_base,raw,raw_hash)
 VALUES ('cask','ranked-app','ranked-app','Ranked App','1','A useful app','en-US',50,500,false,'homebrew/cask','1','{}','fixture'),
 ('cask','font-fixture','font-fixture','Font Fixture','1','A font','en-US',500,NULL,true,'homebrew/cask','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,summary,description,status,source_hash,source,source_locale)
 SELECT id,'zh-CN','实用应用','已发布的完整说明','machine',repeat('a',64),'llm','en-US' FROM packages WHERE token='ranked-app'`).Error)
	p, err := st.IndexPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 50, p.SitemapMinInstalls)
	require.Equal(t, 500, p.SitemapNonEnglishTopN)
	row, found, err := st.IndexRowByToken(ctx, "ranked-app")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 500, row.Facts().Rank30d)
	require.Equal(t, 50, row.Facts().Installs30d)
	for _, locale := range []string{"en-US", "zh-CN"} {
		decision := seo.DecidePage(row.Facts(), seo.PageRef{Kind: seo.PageOverview, Locale: locale}, p)
		require.True(t, decision.InSitemap, "%s: %+v", locale, decision)
	}
	font, found, err := st.IndexRowByToken(ctx, "font-fixture")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, font.Facts().IsFont)
	require.Equal(t, "font_page", seo.DecidePage(font.Facts(), seo.PageRef{Kind: seo.PageOverview, Locale: "en-US"}, p).Reason)
}
