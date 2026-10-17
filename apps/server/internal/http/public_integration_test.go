//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/search"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestPublicAPIContractsWithPostgreSQLRedisCacheAndLimits(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	seed, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, seed, "", ""))
	mutations := []store.CatalogMutation{}
	for _, fixture := range []struct{ kind, raw string }{
		{"cask", `{"token":"visual-studio-code","name":["Visual Studio Code","VS Code"],"version":"1.140.0","desc":"Code editor","artifacts":[{"app":["Visual Studio Code.app"]}],"depends_on":{"formula":["ripgrep"],"macos":{">=":["13"]}}}`},
		{"cask", `{"token":"wechat","name":["WeChat","微信"],"version":"1"}`},
		{"cask", `{"token":"hidden","version":"1"}`},
		{"cask", `{"token":"disabled","version":"1","disabled":true,"disable_date":"2026-01-01"}`},
		{"cask", `{"token":"stale","version":"1"}`},
		{"cask", `{"token":"font-sample","version":"1"}`},
		{"formula", `{"name":"ripgrep","versions":{"stable":"1"},"executables":["rg"],"dependencies":["lib-sample"]}`},
		{"formula", `{"name":"lib-sample","versions":{"stable":"1"},"dependencies":["ripgrep"],"uses_from_macos":[{"zlib":"build"}]}`},
	} {
		p, err := homebrew.Normalize(fixture.kind, json.RawMessage(fixture.raw))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: p})
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutations))
	for _, sql := range []string{
		`INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden'`,
		`INSERT INTO package_categories(package_id,category_id,is_primary,source) SELECT p.id,c.id,true,'human' FROM packages p CROSS JOIN categories c WHERE p.token='ripgrep' AND c.slug='education'`,
		`UPDATE packages SET is_library=true WHERE token='lib-sample'`,
		`UPDATE packages SET stale_disabled=true WHERE token='stale'`,
		`UPDATE packages SET installs_30d=100,installs_90d=300,installs_365d=1000,popularity=10 WHERE token='visual-studio-code'`,
		`INSERT INTO package_categories(package_id,category_id,is_primary,source) SELECT p.id,c.id,true,'human' FROM packages p CROSS JOIN categories c WHERE p.token IN ('visual-studio-code','wechat') AND c.slug='developer-tools'`,
	} {
		require.NoError(t, st.DB.WithContext(ctx).Exec(sql).Error)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend := cache.Redis{Client: st.Redis}
	searchService := search.New(ctx, st, backend, logger)
	t.Cleanup(searchService.Close)
	service := &catalog.PublicService{Store: st, Cache: backend, WebBaseURL: "http://localhost:3000"}
	server, err := httpserver.New(config.Config{WebBaseURL: "http://localhost:3000"}, logger, st, httpserver.Dependencies{Public: &public.Handler{Catalog: service, Search: searchService}, Rate: &middleware.RateLimiter{Client: st.Redis}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		request.Header.Set("Origin", "http://localhost:3000")
		request.Header.Set("X-Request-Id", "cors-integration-1")
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		reply := httptest.NewRecorder()
		server.Engine.ServeHTTP(reply, request)
		require.Equal(t, "http://localhost:3000", reply.Header().Get("Access-Control-Allow-Origin"))
		require.Equal(t, "cors-integration-1", reply.Header().Get("X-Request-Id"))
		require.Contains(t, reply.Header().Get("Access-Control-Expose-Headers"), "X-RateLimit-Remaining")
		require.NotEmpty(t, reply.Header().Get("X-RateLimit-Limit"))
		require.NotEmpty(t, reply.Header().Get("X-RateLimit-Remaining"))
		testutil.ValidateResponse(t, spec, "/api/v1", request, reply)
		return reply
	}
	for _, path := range []string{"/home", "/categories", "/packages", "/packages?sort=name&includeFonts=true&includeLibraries=true&includeDisabled=true", "/packages?category=developer-tools", "/packages/cask/visual-studio-code", "/packages/cask/disabled", "/packages/cask/stale", "/packages/formula/ripgrep", "/packages/cask/visual-studio-code/dependencies?depth=3", "/packages/formula/ripgrep/dependencies?depth=3", "/packages/cask/visual-studio-code/related", "/rankings?kind=cask", "/rankings?kind=formula&period=90d&size=1&current=2", "/search?q=vscode", "/search?q=rg&kind=formula", "/packages?kind=formula", "/search/suggest?q=weix", "/sitemap/packages", "/config/client?platform=web", "/config/client?platform=desktop&version=0.0.1&locale=en-US"} {
		t.Run(path, func(t *testing.T) {
			reply := call("GET", path, "")
			if strings.Contains(path, "formula") {
				require.Equal(t, 404, reply.Code)
				return
			}
			require.Equal(t, 200, reply.Code, reply.Body.String())
			require.Contains(t, reply.Header().Get("Vary"), "Accept-Language")
			require.Contains(t, reply.Header().Get("ETag"), `W/"`)
			require.Contains(t, reply.Header().Get("Cache-Control"), "public")
		})
	}
	for _, path := range []string{"/packages?", "/rankings?kind=cask&", "/search?q=vscode&", "/sitemap/packages?", "/collections?", "/packages/cask/visual-studio-code/releases?"} {
		reply := call("GET", path+"current="+strconv.Itoa(1<<53)+"&size=100", "")
		require.Equal(t, 200, reply.Code)
		var page struct {
			Data struct{ Records []json.RawMessage }
		}
		require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &page))
		require.NotNil(t, page.Data.Records)
		require.Empty(t, page.Data.Records)
	}
	// Search lacks the catalog service's product validation; cover real requests that overflow OFFSET multiplication.
	require.Equal(t, 200, call("GET", "/search?q=vscode&size=100&current="+strconv.Itoa(math.MaxInt), "").Code)
	var home publicapi.HomeResponse
	homeReply := call("GET", "/home", "")
	require.NoError(t, json.Unmarshal(homeReply.Body.Bytes(), &home))
	require.Empty(t, home.Data.PopularCli)
	require.Zero(t, home.Data.Stats.Formulae)
	for _, section := range [][]publicapi.PackageSummary{home.Data.PopularApps, home.Data.RecentlyUpdated} {
		for _, item := range section {
			require.Equal(t, publicapi.PackageKindCask, item.Kind)
		}
	}
	categories := call("GET", "/categories", "").Body.String()
	require.NotContains(t, categories, `"slug":"education"`)
	require.NotContains(t, categories, `"slug":"languages"`)
	require.Contains(t, categories, `"slug":"developer-tools"`)
	require.NotContains(t, call("GET", "/search/suggest?q=education", "").Body.String(), `"slug":"education"`)
	require.NotContains(t, call("GET", "/search/suggest?q=ripgrep", "").Body.String(), `"token":"ripgrep"`)
	require.NotContains(t, call("GET", "/sitemap/packages", "").Body.String(), `"kind":"formula"`)
	var list publicapi.PackageSummaryPageResponse
	reply := call("GET", "/packages", "")
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &list))
	require.Equal(t, 3, list.Data.Total)
	for _, p := range list.Data.Records {
		require.Equal(t, publicapi.PackageKindCask, p.Kind)
		require.False(t, p.IsLibrary || p.Disabled)
		require.NotEqual(t, "hidden", p.Token)
	}
	require.Contains(t, reply.Body.String(), `"token":"font-sample"`)
	require.NotContains(t, reply.Body.String(), `"token":"stale"`)
	require.Contains(t, call("GET", "/packages?includeDisabled=true", "").Body.String(), `"token":"stale"`)
	var staleDetail publicapi.PackageDetailResponse
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/stale", "").Body.Bytes(), &staleDetail))
	require.NotNil(t, staleDetail.Data.Disable)
	require.Contains(t, *staleDetail.Data.Disable.Reason, "90")
	for _, path := range []string{"/packages", "/rankings?kind=cask"} {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		require.Contains(t, call("GET", path, "").Body.String(), `"token":"font-sample"`)
		require.NotContains(t, call("GET", path+separator+"includeFonts=false", "").Body.String(), `"token":"font-sample"`)
	}
	var detail publicapi.PackageDetailResponse
	reply = call("GET", "/packages/cask/visual-studio-code", "")
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &detail))
	require.Equal(t, "brew install --cask visual-studio-code", detail.Data.InstallCommand)
	require.Equal(t, 100, detail.Data.Installs.D30)
	require.Equal(t, ">= 13", *detail.Data.DependsOn.Macos)
	require.Equal(t, "Visual Studio Code", detail.Data.DisplayName)
	var deps publicapi.DependenciesResponse
	reply = call("GET", "/packages/formula/ripgrep/dependencies?depth=3", "")
	require.Equal(t, 404, reply.Code)
	reply = call("GET", "/packages/cask/visual-studio-code/dependencies?depth=3", "")
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &deps))
	require.Equal(t, "ripgrep", deps.Data.Dependencies[0].Token)
	require.Empty(t, deps.Data.Dependencies[0].Children)
	for _, path := range []string{"/packages/cask/missing", "/packages/cask/missing/related"} {
		require.Equal(t, 404, call("GET", path, "").Code)
	}
	require.Equal(t, 400, call("GET", "/config/client?platform=desktop", "").Code)
	reply = call("POST", "/packages/lookup", `{"items":[{"kind":"cask","token":"hidden"},{"kind":"formula","token":"missing"}]}`)
	require.Equal(t, 404, reply.Code)
	reply = call("POST", "/packages/lookup", `{"items":[{"kind":"cask","token":"hidden"},{"kind":"cask","token":"missing"}]}`)
	require.Equal(t, 200, reply.Code)
	var lookup publicapi.LookupResponse
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &lookup))
	require.True(t, lookup.Data[0].Found)
	require.False(t, lookup.Data[1].Found)
	reply = call("GET", "/search?q=vscode", "")
	var searchResult publicapi.SearchResponse
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &searchResult))
	require.NotEmpty(t, searchResult.Data.Records)
	require.Equal(t, "visual-studio-code", searchResult.Data.Records[0].Package.Token)
	click, _ := json.Marshal(publicapi.SearchClickRequest{QueryId: searchResult.Data.QueryId, Kind: "cask", Token: "visual-studio-code", Position: 1})
	require.Equal(t, 200, call("POST", "/search/clicks", string(click)).Code)
	reply = call("POST", "/feedback", `{"type":"other","content":"有效的反馈内容","platform":"web"}`)
	require.Equal(t, 200, reply.Code)
	reply = call("POST", "/feedback", `{"type":"other","content":"honeypot feedback","platform":"web","website":"http://spam.example"}`)
	require.Equal(t, 200, reply.Code)
	var count int64
	require.NoError(t, st.DB.WithContext(ctx).Table("feedback").Count(&count).Error)
	require.Equal(t, int64(1), count)
	for range 3 {
		require.Equal(t, 200, call("POST", "/feedback", `{"type":"other","content":"有效的反馈内容","platform":"web"}`).Code)
	}
	reply = call("POST", "/feedback", `{"type":"other","content":"有效的反馈内容","platform":"web"}`)
	require.Equal(t, 429, reply.Code)
	require.NotEmpty(t, reply.Header().Get("Retry-After"))
	require.Equal(t, "5", reply.Header().Get("X-RateLimit-Limit"))
	require.Equal(t, "0", reply.Header().Get("X-RateLimit-Remaining"))
	var failure respond.Envelope
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &failure))
	require.Equal(t, domain.CodeRateLimited, failure.Code)
	first := call("GET", "/home", "")
	request := httptest.NewRequest("GET", "/api/v1/home", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	reply = httptest.NewRecorder()
	server.Engine.ServeHTTP(reply, request)
	require.Equal(t, http.StatusNotModified, reply.Code)
	require.Empty(t, reply.Body.String())
	require.Equal(t, first.Header().Get("ETag"), reply.Header().Get("ETag"))
	require.Equal(t, "http://localhost:3000", reply.Header().Get("Access-Control-Allow-Origin"))
	require.NotEmpty(t, reply.Header().Get("X-RateLimit-Remaining"))
	stop, err := cache.Subscribe(ctx, st.Redis, logger)
	require.NoError(t, err)
	t.Cleanup(stop)
	require.NoError(t, st.Redis.Set(ctx, "auth:unrelated", "preserve", time.Minute).Err())
	require.NoError(t, cache.PublishInvalidation(ctx, st.Redis, "c:home:*", "c:pkg:*"))
	require.Eventually(t, func() bool {
		n, err := st.Redis.Exists(ctx, "c:home:c2:zh-CN", "c:pkg:c4:cask:visual-studio-code:zh-CN").Result()
		return err == nil && n == 0
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "preserve", st.Redis.Get(ctx, "auth:unrelated").Val())
	require.Error(t, cache.Invalidate(ctx, st.Redis, []string{"*"}))
}

func TestGCRALimitsAreAtomicAcrossConcurrentRequests(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	limiter := middleware.RateLimiter{Client: st.Redis}
	var wg sync.WaitGroup
	allowed := make(chan bool, 20)
	for range 20 {
		wg.Go(func() {
			ok, _, err := limiter.Allow(ctx, "atomic", 5, time.Hour)
			if err != nil {
				allowed <- false
				return
			}
			allowed <- ok
		})
	}
	wg.Wait()
	close(allowed)
	count := 0
	for ok := range allowed {
		if ok {
			count++
		}
	}
	require.Equal(t, 5, count)
	ok, retry, err := limiter.Allow(ctx, "atomic", 5, time.Hour)
	require.NoError(t, err)
	require.False(t, ok)
	require.Positive(t, retry)
	for remaining := 4; remaining >= 0; remaining-- {
		decision, err := limiter.Check(ctx, "remaining", 5, time.Hour)
		require.NoError(t, err)
		require.True(t, decision.Allowed)
		require.Equal(t, remaining, decision.Remaining)
	}
	decision, err := limiter.Check(ctx, "remaining", 5, time.Hour)
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Zero(t, decision.Remaining)
	for range 2 {
		decision, err = limiter.Check(ctx, "refill", 2, 200*time.Millisecond)
		require.NoError(t, err)
		require.True(t, decision.Allowed)
	}
	decision, err = limiter.Check(ctx, "refill", 2, 200*time.Millisecond)
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Eventually(t, func() bool {
		decision, err := limiter.Check(ctx, "refill", 2, 200*time.Millisecond)
		return err == nil && decision.Allowed && decision.Remaining == 0
	}, time.Second, 10*time.Millisecond)
}
