//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPackageSEOOverridesKeepClearAndDeriveTraditional(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (990,'cask','seo-fixture','seo-fixture','homebrew/cask','SEO Fixture','1','1','{}','fixture'),
 (991,'cask','seo-removed','seo-removed','homebrew/cask','SEO Removed','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE id=991").Error)
	value := func(v string) *string { return &v }
	keep, clear := store.SEOText{}, store.SEOText{Set: true}
	set := func(id int64, locale string, title, description store.SEOText, owner string) (bool, error) {
		t.Helper()
		var changed bool
		err := st.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			changed, err = (&store.Store{DB: tx}).SetPackageSEO(ctx, id, locale, title, description, owner)
			return err
		})
		return changed, err
	}
	revision := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, st.DB.Raw("SELECT coalesce((SELECT revision FROM entity_revisions WHERE entity_key='pkgseo:990'),0)").Scan(&n).Error)
		return n
	}
	read := func(locale string) *store.PackageSEO {
		t.Helper()
		row, err := st.PackageSEOOverride(ctx, 990, locale)
		require.NoError(t, err)
		return row
	}

	changed, err := set(990, "en-US", store.SEOText{Set: true, Value: value("SEO Fixture for Mac")}, keep, "admin:1")
	require.NoError(t, err)
	require.True(t, changed)
	row := read("en-US")
	require.Equal(t, "SEO Fixture for Mac", *row.Title)
	require.Nil(t, row.Description)
	require.Equal(t, "admin:1", row.UpdatedBy)
	require.False(t, row.Derived)
	require.EqualValues(t, 1, revision())
	var patterns string
	require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE unique_key='cache:invalidate:package-seo:990'").Scan(&patterns).Error)
	require.Contains(t, patterns, "cask:seo-fixture:*", "the overview's cached detail is refreshed in every language")

	// Writing the same value changes nothing and keeps the revision.
	changed, err = set(990, "en-US", store.SEOText{Set: true, Value: value("SEO Fixture for Mac")}, keep, "agent:3:7")
	require.NoError(t, err)
	require.False(t, changed)
	require.EqualValues(t, 1, revision())
	require.Equal(t, "admin:1", read("en-US").UpdatedBy)

	// An omitted field keeps its value; a cleared one drops it; clearing both removes the override.
	changed, err = set(990, "en-US", keep, store.SEOText{Set: true, Value: value("Download and install with Homebrew.")}, "agent:3:7")
	require.NoError(t, err)
	require.True(t, changed)
	row = read("en-US")
	require.Equal(t, "SEO Fixture for Mac", *row.Title)
	require.Equal(t, "Download and install with Homebrew.", *row.Description)
	require.Equal(t, "agent:3:7", row.UpdatedBy)
	changed, err = set(990, "en-US", clear, keep, "agent:3:7")
	require.NoError(t, err)
	require.True(t, changed)
	row = read("en-US")
	require.Nil(t, row.Title)
	require.NotNil(t, row.Description)
	changed, err = set(990, "en-US", keep, clear, "agent:3:7")
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, read("en-US"))
	changed, err = set(990, "en-US", clear, clear, "agent:3:7")
	require.NoError(t, err)
	require.False(t, changed, "clearing an absent override is a no-op")
	require.EqualValues(t, 4, revision())

	// zh-Hant converts zh-CN's override with the glossary's protected terms; it has none of its own.
	require.NoError(t, st.DB.Exec("INSERT INTO i18n_glossary(term,do_not_translate) VALUES('Homebrew',true) ON CONFLICT DO NOTHING").Error)
	_, err = set(990, "zh-CN", store.SEOText{Set: true, Value: value("SEO Fixture 下载与安装")}, store.SEOText{Set: true, Value: value("用 Homebrew 下载这个应用")}, "agent:3:7")
	require.NoError(t, err)
	_, protected, err := st.ContentGlossary(ctx)
	require.NoError(t, err)
	wantTitle, err := traditional.Text("SEO Fixture 下载与安装", protected)
	require.NoError(t, err)
	wantDescription, err := traditional.Text("用 Homebrew 下载这个应用", protected)
	require.NoError(t, err)
	derived := read("zh-Hant")
	require.True(t, derived.Derived)
	require.Equal(t, "zh-Hant", derived.Locale)
	require.Equal(t, wantTitle, *derived.Title)
	require.Equal(t, wantDescription, *derived.Description)
	require.NotEqual(t, "SEO Fixture 下载与安装", *derived.Title, "the text is converted")
	require.Contains(t, *derived.Description, "Homebrew")
	all, err := st.PackageSEOOverrides(ctx, 990)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, "zh-CN", all[0].Locale)
	require.Equal(t, "zh-Hant", all[1].Locale)
	require.True(t, all[1].Derived)
	require.Nil(t, read("de-DE"))

	// Writes that the overview cannot take are rejected with their reason.
	for _, tc := range []struct {
		id     int64
		locale string
		code   string
		msg    string
	}{
		{990, "zh-Hant", domain.CodeValidation, "rejected: derived_locale_read_only:zh-Hant:zh-CN"},
		{990, "xx-XX", domain.CodeValidation, "rejected: locale_invalid:locale"},
		{991, "en-US", domain.CodeNotFound, ""},
		{99999, "en-US", domain.CodeNotFound, ""},
	} {
		_, err := set(tc.id, tc.locale, store.SEOText{Set: true, Value: value("Title")}, keep, "admin:1")
		var app *domain.AppError
		require.ErrorAs(t, err, &app, tc.locale)
		require.Equal(t, tc.code, app.Code)
		require.Equal(t, tc.msg, app.Msg)
	}
	_, err = set(990, "en-US", store.SEOText{Set: true, Value: value("Title")}, keep, "root")
	require.Error(t, err, "owners have a fixed form")
}

func TestPackageSEOChangesOverviewLastmod(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec("INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,first_seen_at) VALUES(990,'cask','seo-dates','seo-dates','homebrew/cask','SEO Dates','1','1','{}','fixture','2020-01-01')").Error)
	refresh := func() {
		_, err := st.RefreshPackagePageStates(ctx, 100)
		require.NoError(t, err)
	}
	read := func(locale, kind string) store.PageState {
		var row store.PageState
		require.NoError(t, st.DB.Raw("SELECT * FROM package_page_state WHERE package_id=990 AND locale=? AND page_kind=? AND page_number=1", locale, kind).Scan(&row).Error)
		require.NotEmpty(t, row.ContentHash)
		return row
	}
	refresh()
	german := read("de-DE", "overview")
	details := read("zh-CN", "details")
	for _, values := range [][2]string{{"软件标题", ""}, {"软件标题", "软件说明"}, {"", ""}} {
		before, traditionalBefore := read("zh-CN", "overview"), read("zh-Hant", "overview")
		field := func(v string) store.SEOText {
			if v == "" {
				return store.SEOText{Set: true}
			}
			return store.SEOText{Set: true, Value: &v}
		}
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
			changed, err := (&store.Store{DB: tx}).SetPackageSEO(ctx, 990, "zh-CN", field(values[0]), field(values[1]), "system")
			require.True(t, changed)
			return err
		}))
		refresh()
		after, traditionalAfter := read("zh-CN", "overview"), read("zh-Hant", "overview")
		require.NotEqual(t, before.ContentHash, after.ContentHash)
		require.True(t, after.ContentModifiedAt.After(before.ContentModifiedAt))
		require.NotEqual(t, traditionalBefore.ContentHash, traditionalAfter.ContentHash)
		require.True(t, traditionalAfter.ContentModifiedAt.After(traditionalBefore.ContentModifiedAt))
		require.Equal(t, german.ContentModifiedAt, read("de-DE", "overview").ContentModifiedAt)
		require.Equal(t, details.ContentModifiedAt, read("zh-CN", "details").ContentModifiedAt)
		refresh()
		require.Equal(t, after.ContentModifiedAt, read("zh-CN", "overview").ContentModifiedAt)
	}
}

func TestInitialOverviewLastmodIncludesSEOOverride(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec("INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,first_seen_at) VALUES(990,'cask','initial-seo','initial-seo','homebrew/cask','Initial SEO','1','1','{}','fixture','2020-01-01')").Error)
	title := "软件标题"
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		_, err := (&store.Store{DB: tx}).SetPackageSEO(ctx, 990, "zh-CN", store.SEOText{Set: true, Value: &title}, store.SEOText{}, "system")
		return err
	}))
	override, err := st.PackageSEOOverride(ctx, 990, "zh-CN")
	require.NoError(t, err)
	require.NotNil(t, override)
	_, err = st.RefreshPackagePageStates(ctx, 100)
	require.NoError(t, err)
	for _, locale := range []string{"zh-CN", "zh-Hant", "en-US"} {
		for _, kind := range []string{"overview", "details", "dependencies", "versions"} {
			var row store.PageState
			require.NoError(t, st.DB.Raw("SELECT * FROM package_page_state WHERE package_id=990 AND locale=? AND page_kind=? AND page_number=1", locale, kind).Scan(&row).Error)
			require.NotEmpty(t, row.ContentHash)
			if kind == "overview" && locale != "en-US" {
				require.True(t, row.ContentModifiedAt.Equal(override.UpdatedAt), "%s: lastmod=%s override=%s", locale, row.ContentModifiedAt, override.UpdatedAt)
			} else {
				require.Equal(t, 2020, row.ContentModifiedAt.Year(), "%s %s", locale, kind)
			}
		}
	}
}
