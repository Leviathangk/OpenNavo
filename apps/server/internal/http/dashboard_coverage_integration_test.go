//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestDashboardCoverageAllEligibleAppsAndFontsAndFreshLocaleContent(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	password, err := seed.HashPassword("dashboard-test-password")
	require.NoError(t, err)
	seedData, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, seedData, "dashboard-tester", password))
	// Keep 1,001 valid apps and one font so both enter coverage, even outside the popularity ranking.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,popularity,raw,raw_hash)
 SELECT CASE WHEN n=1006 THEN 'formula' ELSE 'cask' END,'coverage-'||n,'coverage-'||n,
 CASE WHEN n=1006 THEN 'homebrew/core' ELSE 'homebrew/cask' END,'Coverage '||n,'1','1',1007-n,'{}',repeat('a',64)
 FROM generate_series(1,1006) n`).Error)
	// A link app (outside Homebrew) counts like any Cask app.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale)
 VALUES('link','coverage-link','coverage-link','','Coverage Link','','','{}','','en-US')`).Error)
	var packages []struct {
		ID    int64
		Token string
	}
	require.NoError(t, st.DB.Table("packages").Select("id,token").Find(&packages).Error)
	ids := map[string]int64{}
	for _, pkg := range packages {
		ids[pkg.Token] = pkg.ID
	}
	complete := func(id int64, locale string) {
		t.Helper()
		require.NoError(t, st.DB.Exec("UPDATE packages SET source_locale=? WHERE id=?", locale, id).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale)
 VALUES(?,?,'Coverage','Summary','Description','source','human',?) ON CONFLICT(package_id,locale)
 DO UPDATE SET display_name=EXCLUDED.display_name,summary=EXCLUDED.summary,description=EXCLUDED.description,source='human'`, id, locale, locale).Error)
		if id == ids["coverage-1006"] {
			// Formula is only an exclusion fixture and does not pass through the Cask content translation pipeline.
			return
		}
		require.NoError(t, st.ScheduleContentTranslation(ctx, content.Ref{Entity: "package", ID: id}))
		source, sourceErr := st.ReadContentSource(ctx, content.Ref{Entity: "package", ID: id})
		require.NoError(t, sourceErr)
		// S8 records translation work, but no longer creates placeholder locale rows.
		for _, target := range i18n.SiteLocales {
			require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale,source_hash)
 VALUES(?,?,'Coverage','Summary','Description','machine','llm',?,?) ON CONFLICT(package_id,locale) DO NOTHING`, id, string(target), locale, source.Hash).Error)
		}
		require.NoError(t, st.DB.Exec(`UPDATE package_i18n SET summary='Summary',description='Description',
	 source_locale=?,source_hash=?,status=CASE WHEN locale=? THEN 'source' WHEN locale='en-US' THEN 'manual' ELSE 'machine' END WHERE package_id=?`, locale, source.Hash, locale, id).Error)
		var count int64
		require.NoError(t, st.DB.Table("package_i18n").Where("package_id=?", id).Count(&count).Error)
		require.Equal(t, int64(len(i18n.SiteLocales)), count)
	}
	tailID := ids["coverage-1001"]
	complete(tailID, "zh-CN")
	complete(ids["coverage-1000"], "en-US")
	for _, token := range []string{"coverage-1002", "coverage-1003", "coverage-1004", "coverage-1005", "coverage-1006"} {
		complete(ids[token], "zh-CN")
	}
	require.NoError(t, st.DB.Exec("UPDATE packages SET is_font=true WHERE token='coverage-1002'").Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET disabled=true WHERE token='coverage-1003'").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,hidden) VALUES(?,true)", ids["coverage-1004"]).Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE token='coverage-1005'").Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET deprecated=true WHERE token='coverage-1000'").Error)
	// A fully populated font contributes to every metric, just like an app.
	fontID := ids["coverage-1002"]
	require.NoError(t, st.DB.Exec(`INSERT INTO package_categories(package_id,category_id,is_primary,source)
 VALUES(?,(SELECT id FROM categories ORDER BY id LIMIT 1),true,'human')`, fontID).Error)
	var iconID int64
	require.NoError(t, st.DB.Raw(`INSERT INTO assets(kind,storage_key,url,mime,bytes,sha256)
 VALUES('icon','coverage-font-icon','https://example.com/font.png','image/png',1,repeat('b',64)) RETURNING id`).Scan(&iconID).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,icon_asset_id) VALUES(?,?)", fontID, iconID).Error)
	for _, token := range []string{"coverage-1", "coverage-1001", "coverage-1002", "coverage-1003", "coverage-1004", "coverage-1005", "coverage-1006"} {
		require.NoError(t, st.DB.Exec("INSERT INTO releases(package_id,source,source_key,version,title) VALUES(?,'editorial',?,'1','Latest notes')", ids[token], token).Error)
	}
	require.NoError(t, st.DB.Exec("INSERT INTO releases(package_id,source,source_key,version,title,hidden) VALUES(?,'editorial','hidden','1','Hidden',true),(?,'editorial','old','0.9','Older',false),(?,'manual','legacy','1','Legacy',false)", ids["coverage-2"], ids["coverage-3"], ids["coverage-4"]).Error)
	cfg := config.Config{JWTSecret: strings.Repeat("c", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "dashboard-tester", "dashboard-test-password", store.AuditActor{})
	require.NoError(t, err)
	svc := &management.Service{Store: st, Config: cfg}
	app, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: svc}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	type dashboardSnapshot struct {
		Packages struct{ Casks, Fonts, Disabled int }
		Coverage map[string]adminapi.Ratio
	}
	overview := func() dashboardSnapshot {
		t.Helper()
		req := httptest.NewRequest("GET", "/admin-api/dashboard/overview", nil)
		req.Header.Set("Authorization", "Bearer "+pair.Token)
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		testutil.ValidateResponse(t, spec, "/admin-api", req, rec)
		var response struct {
			Code string
			Data dashboardSnapshot
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		require.Equal(t, "0000", response.Code)
		require.Len(t, response.Data.Coverage, 5)
		require.NotContains(t, response.Data.Coverage, "zhSummary")
		return response.Data
	}
	coverage := func() map[string]adminapi.Ratio { return overview().Coverage }
	counts := overview().Packages
	// Counts include hidden and deprecated apps and link apps, but never fonts, disabled apps or removed apps.
	require.Equal(t, 1003, counts.Casks)
	require.Equal(t, 1, counts.Fonts)
	require.Equal(t, 1, counts.Disabled)
	t.Run("disabled app leaves app count", func(t *testing.T) {
		require.NoError(t, st.DB.Exec("UPDATE packages SET disabled=true WHERE id=?", tailID).Error)
		got := overview().Packages
		require.Equal(t, counts.Casks-1, got.Casks)
		require.Equal(t, counts.Fonts, got.Fonts)
		require.Equal(t, counts.Disabled+1, got.Disabled)
		require.NoError(t, st.DB.Exec("UPDATE packages SET disabled=false WHERE id=?", tailID).Error)
	})
	got := coverage()
	for _, key := range []string{"primaryCategory", "icon", "latestVersionNotes", "sixLocaleContent", "siteLocaleContent"} {
		require.Contains(t, got, key)
		require.Equal(t, 1003, got[key].Total, key)
	}
	require.Equal(t, 3, got["latestVersionNotes"].Done)
	require.Equal(t, 3, got["sixLocaleContent"].Done)
	require.Equal(t, 3, got["siteLocaleContent"].Done)
	require.Equal(t, 1, got["primaryCategory"].Done)
	require.Equal(t, 1, got["icon"].Done)
	for _, tc := range []struct{ name, exclude, restore string }{
		{"disabled font", "UPDATE packages SET disabled=true WHERE id=?", "UPDATE packages SET disabled=false WHERE id=?"},
		{"hidden font", "UPDATE package_meta SET hidden=true WHERE package_id=?", "UPDATE package_meta SET hidden=false WHERE package_id=?"},
		{"removed font", "UPDATE packages SET removed_at=now() WHERE id=?", "UPDATE packages SET removed_at=NULL WHERE id=?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, st.DB.Exec(tc.exclude, fontID).Error)
			packages := overview().Packages
			require.Equal(t, counts.Casks, packages.Casks)
			if tc.name == "hidden font" {
				require.Equal(t, 1, packages.Fonts)
			} else {
				require.Zero(t, packages.Fonts)
			}
			for key, ratio := range coverage() {
				require.Equal(t, got[key].Total-1, ratio.Total, key)
				require.Equal(t, got[key].Done-1, ratio.Done, key)
			}
			require.NoError(t, st.DB.Exec(tc.restore, fontID).Error)
		})
	}
	for _, tc := range []struct{ name, query string }{
		{"missing language with display fallback", "DELETE FROM package_i18n WHERE package_id=? AND locale='ja-JP'"},
		{"missing summary", "UPDATE package_i18n SET summary=NULL WHERE package_id=? AND locale='ja-JP'"},
		{"blank description", "UPDATE package_i18n SET description=E' \\t\\n' WHERE package_id=? AND locale='ja-JP'"},
		{"pending text", "UPDATE package_i18n SET status='pending' WHERE package_id=? AND locale='ja-JP'"},
		{"failed text", "UPDATE package_i18n SET status='failed' WHERE package_id=? AND locale='ja-JP'"},
		{"outdated translation", "UPDATE package_i18n SET source_hash='old' WHERE package_id=? AND locale='ja-JP'"},
		{"unknown freshness", "UPDATE package_i18n SET source_hash=NULL WHERE package_id=? AND locale='ja-JP'"},
		{"previous source language", "UPDATE package_i18n SET source_locale='en-US' WHERE package_id=? AND locale='ja-JP'"},
		{"source changed before scheduling", "UPDATE package_i18n SET summary='Changed' WHERE package_id=? AND locale='zh-CN'"},
		{"homebrew source", "UPDATE package_i18n SET source='homebrew' WHERE package_id=? AND locale='zh-CN'"},
		{"source language changed", "UPDATE packages SET source_locale='ja-JP' WHERE id=?"},
		{"missing source metadata", "DELETE FROM content_translation_sources WHERE entity='package' AND object_key=?::bigint::text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, st.DB.Exec(tc.query, tailID).Error)
			got := coverage()
			require.Equal(t, 2, got["sixLocaleContent"].Done)
			require.Equal(t, 2, got["siteLocaleContent"].Done)
			complete(tailID, "zh-CN")
			got = coverage()
			require.Equal(t, 3, got["sixLocaleContent"].Done)
			require.Equal(t, 3, got["siteLocaleContent"].Done)
		})
	}
	// Languages outside the app set count toward the ten-language row but never the six app languages.
	for _, tc := range []struct{ name, query string }{
		{"missing site-only language", "DELETE FROM package_i18n WHERE package_id=? AND locale='de-DE'"},
		{"outdated site-only translation", "UPDATE package_i18n SET source_hash='old' WHERE package_id=? AND locale='zh-Hant'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, st.DB.Exec(tc.query, tailID).Error)
			got := coverage()
			require.Equal(t, 3, got["sixLocaleContent"].Done)
			require.Equal(t, 2, got["siteLocaleContent"].Done)
			complete(tailID, "zh-CN")
			require.Equal(t, 3, coverage()["siteLocaleContent"].Done)
		})
	}
	t.Run("current version changes", func(t *testing.T) {
		require.NoError(t, st.DB.Exec("UPDATE packages SET version='2',version_base='2' WHERE id=?", tailID).Error)
		require.Equal(t, 2, coverage()["latestVersionNotes"].Done)
	})
	t.Run("empty eligible catalog", func(t *testing.T) {
		require.NoError(t, st.DB.Exec("UPDATE packages SET disabled=true WHERE kind IN ('cask','link')").Error)
		packages := overview().Packages
		require.Zero(t, packages.Casks)
		require.Zero(t, packages.Fonts)
		for key, ratio := range coverage() {
			require.Zero(t, ratio.Done, key)
			require.Zero(t, ratio.Total, key)
		}
	})
}
