//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPublicReleaseTimelineContractsPriorityTranslationAndPatch(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1.140.1,42"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='sample'").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE package_versions SET brew_committed_at='2026-10-02T00:00:00Z' WHERE package_id=?", pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO package_versions(package_id,version,version_base,brew_committed_at) VALUES(?,'1.140.0','1.140.0','2026-09-30T01:00:00Z'),(?,'1.139.0','1.139.0','2026-09-22T23:00:00Z'),(?,'1.130.0','1.130.0',NULL)", pid, pid, pid).Error)
	for _, row := range []struct {
		source, key, version, body, date string
		hidden                           bool
	}{
		{"webpage", "patch", "1.140.1", "", "2026-10-02T00:00:00Z", false},
		{"webpage", "minor", "1.140.0", "Original minor", "2026-09-30T00:00:00Z", false},
		{"manual", "priority", "1.140.0", "Manual preferred", "2026-09-30T00:00:00Z", false},
		{"github_release", "old", "1.139.0", "Older version", "2026-09-23T00:00:00Z", false},
		{"manual", "belated", "1.138.0", "Belated old release", "2026-10-04T00:00:00Z", false},
		{"manual", "hidden", "9.0.0", "Hidden", "2026-10-05T00:00:00Z", true},
	} {
		require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO releases(package_id,source,source_key,version,body_markdown,published_at,hidden,source_url) VALUES(?,?,?,?,NULLIF(?,''),?,?, 'https://example.com/notes')", pid, row.source, row.key, row.version, row.body, row.date, row.hidden).Error)
	}
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO release_i18n(release_id,locale,status,summary,body_markdown,sections) SELECT id,'zh-CN','machine','摘要','人工来源的中文译文','[{"area":"编辑器","items":["新增功能"]}]' FROM releases WHERE source_key='priority'`).Error)
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	server, err := httpserver.New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Public: &public.Handler{Catalog: svc}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Accept-Language", "zh-CN")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		testutil.ValidateResponse(t, spec, "/api/v1", r, w)
		require.Equal(t, status, w.Code, w.Body.String())
		return w
	}
	var result publicapi.ReleasePageResponse
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/sample/releases?size=100", "", 200).Body.Bytes(), &result))
	require.Equal(t, 5, result.Data.Total)
	require.Equal(t, []string{"1.140.1", "1.140.0", "1.139.0", "1.138.0", "1.130.0"}, []string{result.Data.Records[0].Version, result.Data.Records[1].Version, result.Data.Records[2].Version, result.Data.Records[3].Version, result.Data.Records[4].Version})
	require.True(t, result.Data.Records[0].IsLatest)
	require.False(t, result.Data.Records[0].HasNotes)
	require.Nil(t, result.Data.Records[0].BodyMarkdown)
	require.Equal(t, "人工来源的中文译文", *result.Data.Records[1].BodyMarkdown)
	require.Equal(t, publicapi.TranslationInfoStatusMachine, result.Data.Records[1].Translation.Status)
	require.Equal(t, publicapi.ReleaseEntrySource("manual"), result.Data.Records[1].Source)
	require.Equal(t, 4, result.Data.Stats.Count30d)
	require.Equal(t, 3, result.Data.Stats.BrewLag.Compared)
	require.Equal(t, 1, result.Data.Stats.BrewLag.EarlierCount)
	require.Equal(t, 0, *result.Data.Stats.BrewLag.MedianMinutes)
	require.Nil(t, result.Data.Records[4].BrewCommittedAt)
	// "With notes" lists only versions that have notes and pages over them; stats still cover the whole timeline.
	var notes publicapi.ReleasePageResponse
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/sample/releases?onlyWithNotes=true&size=100", "", 200).Body.Bytes(), &notes))
	require.Equal(t, 3, notes.Data.Total)
	versions := []string{}
	for _, entry := range notes.Data.Records {
		require.True(t, entry.HasNotes)
		versions = append(versions, entry.Version)
	}
	require.Equal(t, []string{"1.140.0", "1.139.0", "1.138.0"}, versions)
	require.False(t, notes.Data.Records[0].IsLatest)
	require.Equal(t, result.Data.Stats, notes.Data.Stats)
	var notesPage publicapi.ReleasePageResponse
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/sample/releases?onlyWithNotes=true&current=2&size=2", "", 200).Body.Bytes(), &notesPage))
	require.Equal(t, 3, notesPage.Data.Total)
	require.Len(t, notesPage.Data.Records, 1)
	require.Equal(t, "1.138.0", notesPage.Data.Records[0].Version)
	var unfiltered publicapi.ReleasePageResponse
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/sample/releases?onlyWithNotes=false&size=1", "", 200).Body.Bytes(), &unfiltered))
	require.Equal(t, 5, unfiltered.Data.Total)
	require.Contains(t, call("GET", "/packages/cask/sample/releases?locale=en-US&current=2&size=1", "", 200).Body.String(), "Manual preferred")
	// "Original" consistently selects English, even when both the UI and content source language are Chinese.
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE releases SET source_locale='zh-CN' WHERE source_key='priority'").Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO release_i18n(release_id,locale,status,summary,body_markdown,sections) SELECT id,'en-US','manual','English summary','English notes','[{"area":"Editor","items":["New feature"]}]' FROM releases WHERE source_key='priority'`).Error)
	require.NoError(t, st.Redis.Del(ctx, "c:rel:c2:cask:sample:en-US").Err())
	var original publicapi.ReleasePageResponse
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/sample/releases?original=true&locale=zh-CN&size=100", "", 200).Body.Bytes(), &original))
	require.Equal(t, "English notes", *original.Data.Records[1].BodyMarkdown)
	require.Equal(t, "English summary", *original.Data.Records[1].Summary)
	require.Equal(t, "Editor", original.Data.Records[1].Sections[0].Area)
	require.Equal(t, publicapi.Locale("en-US"), *original.Data.Records[1].Translation.Locale)
	require.Contains(t, call("GET", "/packages/cask/sample/releases?original=false&locale=zh-CN", "", 200).Body.String(), "人工来源的中文译文")
	require.Contains(t, call("GET", "/packages/cask/sample", "", 200).Body.String(), `"releaseCount":5`)
	require.Contains(t, call("POST", "/packages/lookup", `{"items":[{"kind":"cask","token":"sample"}]}`, 200).Body.String(), `"latestRelease":{"`)
	call("GET", "/packages/cask/missing/releases", "", 404)
	call("GET", "/packages/cask/sample/releases?size=101", "", 400)
	call("GET", "/packages/cask/sample/releases?current=99", "", 200)
	var empty publicapi.ReleasePageResponse
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/sample/releases?size=100&current="+strconv.Itoa(1<<53), "", 200).Body.Bytes(), &empty))
	require.Empty(t, empty.Data.Records)
	require.Equal(t, 5, empty.Data.Total)
	for _, body := range []any{nil, ""} {
		require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE release_i18n SET status='manual',body_markdown=?", body).Error)
		require.NoError(t, st.Redis.Del(ctx, "c:rel:c2:cask:sample:zh-CN").Err())
		var reviewed publicapi.ReleasePageResponse
		require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/sample/releases?size=100", "", 200).Body.Bytes(), &reviewed))
		entry := reviewed.Data.Records[1]
		require.Equal(t, publicapi.TranslationInfoStatusManual, entry.Translation.Status)
		require.Equal(t, publicapi.Locale("zh-CN"), *entry.Translation.Locale)
		require.Equal(t, "摘要", *entry.Summary)
		require.Len(t, entry.Sections, 1)
		require.Equal(t, "Manual preferred", *entry.BodyMarkdown)
		require.True(t, entry.HasNotes)
	}
	// Reviewed results with only summaries/sections can be published without a source body.
	for _, content := range []struct{ summary, sections string }{
		{"仅摘要", "[]"}, {"", `[{"area":"编辑器","items":["新增功能"]}]`},
	} {
		require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE releases SET body_markdown=NULL WHERE source_key='priority'").Error)
		require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE release_i18n SET summary=?,sections=?", content.summary, content.sections).Error)
		require.NoError(t, st.Redis.Del(ctx, "c:rel:c2:cask:sample:zh-CN").Err())
		page, err := svc.ReleasePage(ctx, "cask", "sample", "zh-CN", 1, 100, false)
		require.NoError(t, err)
		require.True(t, page.Records[1].HasNotes)
		require.Nil(t, page.Records[1].BodyMarkdown)
	}
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE release_i18n SET status='skipped'").Error)
	require.NoError(t, st.Redis.Del(ctx, "c:rel:c2:cask:sample:zh-CN").Err())
	skipped, err := svc.ReleasePage(ctx, "cask", "sample", "zh-CN", 1, 100, false)
	require.NoError(t, err)
	require.False(t, skipped.Records[1].HasNotes)
	require.Nil(t, skipped.Records[1].BodyMarkdown)
}
