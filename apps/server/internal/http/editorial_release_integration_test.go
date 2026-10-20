//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestEditorialReleaseLocaleIsolationAndPageDecision(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	server, err := httpserver.New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Public: &public.Handler{Catalog: svc}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path, body string) []byte {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		testutil.ValidateResponse(t, spec, "/api/v1", req, w)
		return w.Body.Bytes()
	}
	sourceBody := strings.Repeat("这是中文编辑说明，修复窗口和文件保存。", 30)
	translatedBody := strings.Repeat("The editor now saves files and restores windows correctly. ", 10)
	upstreamBody := strings.Repeat("Original upstream release notes for this exact version. ", 10)
	for n, tc := range []struct {
		name, locale, status                                                string
		stale, upstream, build, summaryOnly, noSource, sourceChanged, empty bool
		wantSource, wantStatus                                              string
		hasNotes, localized                                                 bool
	}{
		{name: "source", locale: "zh-CN", wantSource: "editorial", wantStatus: "source", hasNotes: true, localized: true},
		{name: "manual", locale: "en-US", status: "manual", wantSource: "editorial", wantStatus: "manual", hasNotes: true, localized: true},
		{name: "machine", locale: "ja-JP", status: "machine", wantSource: "editorial", wantStatus: "machine", hasNotes: true, localized: true},
		{name: "summary-only", locale: "en-US", status: "manual", summaryOnly: true, wantSource: "editorial", wantStatus: "manual", hasNotes: true, localized: true},
		{name: "stale-machine", locale: "en-US", status: "machine", stale: true, wantSource: "editorial", wantStatus: "pending"},
		{name: "stale-manual", locale: "ja-JP", status: "manual", stale: true, wantSource: "editorial", wantStatus: "pending"},
		{name: "source-changed-without-restamp", locale: "en-US", status: "manual", sourceChanged: true, wantSource: "editorial", wantStatus: "pending"},
		{name: "pending-with-old-content", locale: "en-US", status: "pending", wantSource: "editorial", wantStatus: "pending"},
		{name: "failed-with-old-content", locale: "ja-JP", status: "failed", wantSource: "editorial", wantStatus: "failed"},
		{name: "empty-pending", locale: "en-US", status: "pending", empty: true, wantSource: "editorial", wantStatus: "pending"},
		{name: "skipped", locale: "en-US", status: "skipped", wantSource: "editorial", wantStatus: "skipped"},
		{name: "missing", locale: "en-US", wantSource: "editorial", wantStatus: "pending"},
		{name: "missing-source-row", locale: "en-US", status: "manual", noSource: true, wantSource: "editorial", wantStatus: "pending"},
		{name: "missing-upstream", locale: "en-US", upstream: true, wantSource: "github_release", wantStatus: "source", hasNotes: true, localized: true},
		{name: "upstream-is-not-japanese", locale: "ja-JP", upstream: true, wantSource: "github_release", wantStatus: "source", hasNotes: true},
		{name: "stale-build-upstream", locale: "en-US", status: "machine", stale: true, upstream: true, build: true, wantSource: "github_release", wantStatus: "source", hasNotes: true, localized: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translated := translatedBody
			if tc.locale == "ja-JP" {
				translated = strings.Repeat("ウインドウの復元とファイルの保存を改善しました。", 30)
			}
			id := int64(9000 + n)
			releaseID := id * 10
			token := fmt.Sprintf("release-locale-%d", n)
			version := "1.140.0"
			if tc.build {
				version += ",49"
			}
			require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,source_locale,min_macos,raw,raw_hash)
 VALUES(?,'cask',?,?,'homebrew/cask','Locale Fixture',?,'1.140.0','zh-CN','13','{}','fixture')`, id, token, token, version).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(id,package_id,version,version_base) VALUES(?,?,?,'1.140.0')`, id, id, version).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,source_locale,status,source_hash,display_name,summary,description,source)
 SELECT ?,locale,'zh-CN',CASE WHEN locale='zh-CN' THEN 'source' ELSE 'manual' END,repeat('a',64),'Locale Fixture','A useful macOS editor','Detailed macOS editor content','human'
 FROM unnest(ARRAY['zh-CN','en-US','ja-JP']) locale`, id).Error)
			var buildID any
			if tc.build {
				buildID = id
			}
			require.NoError(t, st.DB.Exec(`INSERT INTO releases(id,package_id,package_version_id,source,source_key,version,source_locale,title,body_markdown,source_url)
 VALUES(?,?,?,'editorial','editorial:1.140.0','1.140.0','zh-CN','中文标题',?,'https://vendor.example/releases/1.140.0')`, releaseID, id, buildID, sourceBody).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,title,summary,body_markdown,sections)
 VALUES(?,'zh-CN','zh-CN','source','中文标题','中文摘要',?,'[{"area":"改进","items":["验证更新说明写入。"]}]')`, releaseID, sourceBody).Error)
			source, err := st.ReadContentSource(ctx, content.Ref{Entity: "release", ID: releaseID})
			require.NoError(t, err)
			if tc.status != "" {
				hash := source.Hash
				if tc.stale {
					hash = strings.Repeat("b", 64)
				}
				var summary any = translated
				var body any = translated
				if tc.summaryOnly {
					body = nil
				}
				if tc.empty {
					summary, body = nil, nil
				}
				require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,source_hash,summary,body_markdown,sections)
 VALUES(?,?,'zh-CN',?,?,?,?,'[]')`, releaseID, tc.locale, tc.status, hash, summary, body).Error)
			}
			if tc.noSource {
				require.NoError(t, st.DB.Exec(`DELETE FROM release_i18n WHERE release_id=? AND locale='zh-CN'`, releaseID).Error)
			}
			if tc.sourceChanged {
				require.NoError(t, st.DB.Exec(`UPDATE release_i18n SET summary='源文已更新' WHERE release_id=? AND locale='zh-CN'`, releaseID).Error)
			}
			// A different version must never become a fallback for this version.
			require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_locale,body_markdown) VALUES(?,'github_release','other-version','0.9','en-US','Other version')`, id).Error)
			if tc.upstream {
				require.NoError(t, st.DB.Exec(`INSERT INTO releases(id,package_id,source,source_key,version,source_locale,body_markdown,source_url)
 VALUES(?,?,'github_release','v1.140.0','1.140.0','en-US',?,'https://vendor.example/releases/1.140.0'),
 (?,?,'sparkle','v1.140.0','1.140.0','en-US','Lower priority','https://vendor.example/feed')`, releaseID+1, id, upstreamBody, releaseID+2, id).Error)
				// Fallback must use the upstream original, not its unrelated localized summary.
				require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,summary) VALUES(?,'en-US','en-US','manual','旧上游摘要')`, releaseID+1).Error)
			}
			if tc.name == "missing" {
				old := publicapi.ReleaseEntry{Version: version, Source: "editorial", HasNotes: true, BodyMarkdown: &sourceBody, Sections: []publicapi.ReleaseSection{}, Translation: publicapi.TranslationInfo{Status: "source"}}
				raw, err := json.Marshal([]publicapi.ReleaseEntry{old})
				require.NoError(t, err)
				require.NoError(t, st.Redis.Set(ctx, "c:rel:c2:cask:"+token+":"+tc.locale, raw, time.Minute).Err())
				raw, err = json.Marshal(publicapi.PackageDetail{LatestRelease: &old})
				require.NoError(t, err)
				require.NoError(t, st.Redis.Set(ctx, "c:pkg:c4:cask:"+token+":"+tc.locale, raw, time.Minute).Err())
			}
			var page publicapi.ReleasePageResponse
			require.NoError(t, json.Unmarshal(call("GET", fmt.Sprintf("/packages/cask/%s/releases?locale=%s&size=%d", token, tc.locale, seo.VersionsPageSize), ""), &page))
			require.NotEmpty(t, page.Data.Records)
			entry := page.Data.Records[0]
			require.Equal(t, version, entry.Version)
			require.Equal(t, tc.wantSource, string(entry.Source))
			require.Equal(t, tc.wantStatus, string(entry.Translation.Status))
			require.Equal(t, tc.hasNotes, entry.HasNotes)
			if !tc.hasNotes {
				require.Nil(t, entry.Title)
				require.Nil(t, entry.BodyMarkdown)
				require.Nil(t, entry.Summary)
				require.Empty(t, entry.Sections)
				require.Equal(t, tc.locale, string(*entry.Translation.Locale))
			} else if tc.upstream {
				require.Equal(t, releaseID+1, *entry.Id)
				require.Equal(t, upstreamBody, *entry.BodyMarkdown)
				require.Nil(t, entry.Summary)
				require.Equal(t, "en-US", string(*entry.Translation.Locale))
			} else if tc.locale == "zh-CN" {
				require.Equal(t, sourceBody, *entry.BodyMarkdown)
			} else {
				require.Nil(t, entry.Title, "foreign source title must not fill a missing translated title")
				if tc.summaryOnly {
					require.Nil(t, entry.BodyMarkdown)
				} else {
					require.Equal(t, translated, *entry.BodyMarkdown)
				}
			}
			require.Equal(t, tc.localized, page.Data.Indexing.Robots == "index, follow")
			var detail publicapi.PackageDetailResponse
			require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/"+token+"?locale="+tc.locale, ""), &detail))
			require.NotNil(t, detail.Data.LatestRelease)
			require.Equal(t, entry.BodyMarkdown, detail.Data.LatestRelease.BodyMarkdown)
			require.Equal(t, entry.Summary, detail.Data.LatestRelease.Summary)
			require.Equal(t, entry.Translation, detail.Data.LatestRelease.Translation)
			require.Equal(t, entry.HasNotes, detail.Data.LatestRelease.HasNotes)
			lookup := call("POST", "/packages/lookup?locale="+tc.locale, fmt.Sprintf(`{"items":[{"kind":"cask","token":%q}]}`, token))
			if tc.locale != "zh-CN" {
				require.NotContains(t, string(lookup), "中文摘要")
			}
			// H9 rollout never relaxes the per-language version-note threshold.
			row, found, err := st.IndexRowByID(ctx, id)
			require.NoError(t, err)
			require.True(t, found)
			rows := []store.IndexRow{row}
			require.NoError(t, st.PopulatePageNotes(ctx, rows, 1))
			require.Equal(t, tc.localized, rows[0].NotesPages[1][tc.locale].Chars >= 300)
			for _, mode := range []string{"shadow", "enforce"} {
				policy := seo.Policy{Mode: "shadow", TranslationMode: mode, TranslationLocales: []string{"en-US"}, VersionsMinNotesChars: 300}
				decision := seo.DecidePage(rows[0].Facts(), seo.PageRef{Kind: seo.PageVersions, Locale: tc.locale, Page: 1}, policy)
				require.Equal(t, tc.localized, decision.Robots == "index, follow", mode)
			}
		})
	}

	// A translated release cannot manufacture readiness for the package body.
	require.NoError(t, st.DB.Exec("DELETE FROM package_i18n WHERE package_id=9001 AND locale='en-US'").Error)
	row, found, err := st.IndexRowByID(ctx, 9001)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, seo.TranslationReady(row.Facts(), "en-US"))
	rows := []store.IndexRow{row}
	require.NoError(t, st.PopulatePageNotes(ctx, rows, 1))
	require.GreaterOrEqual(t, rows[0].NotesPages[1]["en-US"].Chars, 300)
	for _, kind := range []seo.PageKind{seo.PageOverview, seo.PageVersions} {
		decision := seo.DecidePage(rows[0].Facts(), seo.PageRef{Kind: kind, Locale: "en-US", Page: 1}, seo.Policy{Mode: "shadow", TranslationMode: "enforce", TranslationLocales: []string{"en-US"}})
		require.Equal(t, "noindex, follow", decision.Robots)
	}
}
