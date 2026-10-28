package store

import (
	"testing"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/stretchr/testify/require"
)

func TestIndexFactsCarryFontAndPopularity(t *testing.T) {
	row := IndexRow{IsFont: true, Rank30d: 500, Installs30d: 50}
	f := row.Facts()
	require.True(t, f.IsFont)
	require.True(t, (IndexRow{Token: "font-example"}).Facts().IsFont)
	require.Equal(t, 500, f.Rank30d)
	require.Equal(t, 50, f.Installs30d)
}

func TestSitemapSettingsDefaultsAndStoredOverrides(t *testing.T) {
	for _, raw := range []string{"", `{"pages":{"indexPolicy":{"mode":"shadow"}}}`} {
		s, err := DecodeSEOSettings([]byte(raw))
		require.NoError(t, err)
		require.Equal(t, seo.DefaultSitemapMinInstalls, s.Pages.SitemapMinInstalls)
		require.Equal(t, seo.DefaultSitemapNonEnglishTopN, s.Pages.SitemapNonEnglishTopN)
	}
	s, err := DecodeSEOSettings([]byte(`{"pages":{"sitemapMinInstalls":80,"sitemapNonEnglishTopN":250}}`))
	require.NoError(t, err)
	require.Equal(t, 80, s.Pages.SitemapMinInstalls)
	require.Equal(t, 250, s.Pages.SitemapNonEnglishTopN)
}

func TestBrowseDoesNotApplySitemapPopularityOrHideFonts(t *testing.T) {
	rows := []IndexRow{{ID: 1, Token: "low-installs", Version: "1", SourceLocale: "en-US"}, {ID: 2, Token: "font-example", IsFont: true, Version: "1", SourceLocale: "en-US"}}
	directory := browseDirectory(rows, map[int64]string{1: "Low installs", 2: "Example font"}, "en-US", seo.Policy{})
	require.Len(t, directory.Groups["l"], 1)
	require.Len(t, directory.Groups["e"], 1)
}
