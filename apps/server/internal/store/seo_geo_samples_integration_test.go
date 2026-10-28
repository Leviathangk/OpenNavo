//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// G5-29/30 verify the database projection, not just hand-constructed ReadyLocales.
// These are synthetic published snapshots; no real translation quality is asserted.
func TestGEOPageDecisionPublishedSnapshotsAndReadiness(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	// Qualify for both English and non-English sitemap inclusion under 72a38e4's popularity policy.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale,installs_30d,rank_30d)
 VALUES(901,'cask','geo-fixture-29','geo-fixture-29','homebrew/cask','Fixture','2.0.0','2.0.0','{}','fixture','en-US',50,500)`).Error)
	for _, locale := range seo.Locales {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,source_locale,summary,description,status,source_hash,source)
 VALUES(901,?,'en-US','Useful fixture summary','Published fixture description H1','manual',repeat('a',64),'human')`, locale).Error)
	}
	policy := seo.Policy{Mode: "enforce", Scope: "all", TranslationMode: "enforce"}
	read := func() seo.Facts {
		row, found, err := st.IndexRowByID(ctx, 901)
		require.NoError(t, err)
		require.True(t, found)
		return row.Facts()
	}
	assertLocales := func(f seo.Facts, excluded ...string) {
		for _, locale := range seo.Locales {
			d := seo.DecidePage(f, seo.PageRef{Kind: seo.PageOverview, Locale: locale}, policy)
			want := true
			for _, blocked := range excluded {
				if locale == blocked {
					want = false
				}
				require.NotContains(t, d.Hreflang, blocked)
			}
			require.Equal(t, want, d.InSitemap, locale)
			require.Equal(t, want, d.Announce, locale)
			if want {
				require.Contains(t, d.Hreflang, locale)
			} else {
				require.Equal(t, "noindex, follow", d.Robots)
				require.Empty(t, d.Hreflang)
			}
		}
	}
	assertLocales(read())
	before := seo.DecidePage(read(), seo.PageRef{Kind: seo.PageOverview, Locale: "fr-FR"}, policy)
	require.NoError(t, st.DB.Exec(`UPDATE package_i18n SET description='Source description H2',source_hash=repeat('b',64)
 WHERE package_id=901 AND locale='en-US'`).Error)
	after := read()
	require.Equal(t, before, seo.DecidePage(after, seo.PageRef{Kind: seo.PageOverview, Locale: "fr-FR"}, policy), "G5-29: an existing published snapshot remains ready after a source edit")
	var french struct{ Description, SourceHash string }
	require.NoError(t, st.DB.Raw("SELECT description,source_hash FROM package_i18n WHERE package_id=901 AND locale='fr-FR'").Scan(&french).Error)
	require.Equal(t, "Published fixture description H1", french.Description)
	require.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", french.SourceHash)
	assertLocales(after)
	// Falling below either popularity boundary only removes sitemap membership, preserving published snapshots.
	require.NoError(t, st.DB.Exec(`UPDATE packages SET installs_30d=49,rank_30d=501 WHERE id=901`).Error)
	lessPopular := read()
	for _, locale := range seo.Locales {
		ref := seo.PageRef{Kind: seo.PageOverview, Locale: locale}
		want := seo.DecidePage(after, ref, policy)
		want.InSitemap = false
		require.Equal(t, want, seo.DecidePage(lessPopular, ref, policy), locale)
	}
	require.NoError(t, st.DB.Exec(`UPDATE packages SET installs_30d=50,rank_30d=500 WHERE id=901`).Error)

	// New-language readiness needs a published status, both texts and a valid snapshot hash.
	require.NoError(t, st.DB.Exec(`UPDATE package_i18n SET description='' WHERE package_id=901 AND locale='de-DE';
 UPDATE package_i18n SET source_hash='invalid' WHERE package_id=901 AND locale='zh-Hant';
 UPDATE package_i18n SET status='pending' WHERE package_id=901 AND locale='it-IT'`).Error)
	assertLocales(read(), "de-DE", "zh-Hant", "it-IT")
	policy.Mode, policy.TranslationMode = "shadow", "shadow"
	assertLocales(read(), "de-DE", "zh-Hant", "it-IT")
}
