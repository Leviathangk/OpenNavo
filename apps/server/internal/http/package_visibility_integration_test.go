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

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/search"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPrivatePackagesAbsentFromEveryPublicEntry(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	for _, raw := range []string{
		`{"token":"public-parent","name":["Public parent"],"version":"1","desc":"Useful public application","depends_on":{"cask":["private-sample","public-child"]},"conflicts_with":{"cask":["private-sample"]}}`,
		`{"token":"public-child","name":["Public child"],"version":"1","depends_on":{"cask":["private-sample"]}}`,
		`{"token":"private-sample","name":["Private sample"],"version":"1","desc":"Private fixture description"}`,
		`{"token":"private-font","version":"1"}`,
		`{"token":"unlisted-sample","version":"1"}`,
	} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES('link','private-link','private-link','','Private link','1','1','{}','fixture');
 INSERT INTO package_meta(package_id,private,hidden,visibility_reason) SELECT id,token<>'unlisted-sample',token='unlisted-sample','Visibility fixture' FROM packages WHERE token IN ('private-sample','private-font','private-link','unlisted-sample');
 UPDATE packages SET installs_30d=100,rank_30d=1,popularity=100;
 INSERT INTO package_aliases(package_id,kind,token,source) SELECT id,kind,'private-alias','homebrew' FROM packages WHERE token='private-sample';
 INSERT INTO categories(id,slug,applies_to,icon) VALUES(901,'visibility-fixture','cask','lucide:box');
 INSERT INTO package_categories(package_id,category_id,is_primary,source) SELECT id,901,true,'human' FROM packages;
 INSERT INTO collections(id,slug,status) VALUES(901,'visibility-fixture','published');
 INSERT INTO collection_i18n(collection_id,locale,title) VALUES(901,'en-US','Visibility fixture');
 INSERT INTO collection_items(collection_id,package_id,sort) SELECT 901,id,0 FROM packages;
 INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) VALUES(901,'visibility-fixture','published',901,901,now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type) VALUES(901,901,1,'en-US','roundup');
 INSERT INTO article_revision_i18n(revision_id,locale,source_locale,title) VALUES(901,'en-US','en-US','Visibility fixture');
 INSERT INTO article_revision_packages(revision_id,package_id,sort) SELECT 901,id,0 FROM packages;`).Error)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	searchService := search.New(ctx, st, nil, logger)
	t.Cleanup(searchService.Close)
	svc := &catalog.PublicService{Store: st}
	server, err := httpserver.New(config.Config{}, logger, st, httpserver.Dependencies{Public: &public.Handler{Catalog: svc, Search: searchService}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(t *testing.T, method, path, body string, status int) string {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("X-OpenNavo-Caps", "link")
		r.Header.Set("Accept-Language", "en-US")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		testutil.ValidateResponse(t, spec, "/api/v1", r, w)
		return w.Body.String()
	}
	t.Run("PublicPackage", func(t *testing.T) {
		for _, token := range []string{"private-sample", "private-font"} {
			_, err := st.PublicPackage(ctx, "cask", token, "en-US")
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		}
		_, err := st.PublicPackage(ctx, "link", "private-link", "en-US")
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	})
	t.Run("single_reads", func(t *testing.T) {
		for _, ref := range []string{"cask/private-sample", "cask/private-font", "link/private-link", "cask/private-alias"} {
			for _, suffix := range []string{"", "/related", "/dependencies", "/releases", "/lifecycle"} {
				call(t, "GET", "/packages/"+ref+suffix, "", 404)
			}
		}
	})
	t.Run("discovery_and_references", func(t *testing.T) {
		for _, path := range []string{"/home", "/packages?includeFonts=true&includeDisabled=true", "/rankings?kind=cask", "/search?q=private", "/search/suggest?q=private", "/browse?kind=apps", "/sitemap/packages?group=apps", "/sitemap/packages?group=fonts", "/packages/cask/public-parent", "/packages/cask/public-parent/related", "/packages/cask/public-parent/dependencies?depth=3", "/collections", "/collections/visibility-fixture", "/articles/visibility-fixture"} {
			t.Run(path, func(t *testing.T) {
				body := call(t, "GET", path, "", 200)
				for _, token := range []string{"private-sample", "private-font", "private-link"} {
					require.NotContains(t, body, token)
				}
			})
		}
		var categories struct{ Data []publicapi.CategoryNode }
		require.NoError(t, json.Unmarshal([]byte(call(t, "GET", "/categories", "", 200)), &categories))
		require.Len(t, categories.Data, 1)
		require.Equal(t, 2, categories.Data[0].PackageCount)
		rows, err := st.SnapshotPackages(ctx, "en-US")
		require.NoError(t, err)
		for _, row := range rows {
			require.NotContains(t, row.Token, "private")
			require.NotContains(t, string(row.Dependencies), "private-sample")
		}
	})
	t.Run("lookup", func(t *testing.T) {
		body := call(t, "POST", "/packages/lookup", `{"items":[{"kind":"cask","token":"public-parent"},{"kind":"cask","token":"private-sample"},{"kind":"link","token":"private-link"},{"kind":"cask","token":"unknown-sample"}]}`, 200)
		var response publicapi.LookupResponse
		require.NoError(t, json.Unmarshal([]byte(body), &response))
		require.Len(t, response.Data, 4)
		require.True(t, response.Data[0].Found)
		for _, row := range response.Data[1:] {
			require.False(t, row.Found)
			require.Nil(t, row.Version)
			require.Nil(t, row.LatestRelease)
		}
	})
	t.Run("sitemap", func(t *testing.T) {
		var page struct {
			Data catalog.SitemapPage
		}
		require.NoError(t, json.Unmarshal([]byte(call(t, "GET", "/sitemap/packages?group=apps", "", 200)), &page))
		require.Equal(t, 2, page.Data.Total)
		for _, row := range page.Data.Records {
			require.NotContains(t, row.Token, "private")
			require.NotEqual(t, "unlisted-sample", row.Token)
		}
		require.NoError(t, json.Unmarshal([]byte(call(t, "GET", "/sitemap/packages?group=fonts", "", 200)), &page))
		require.Zero(t, page.Data.Total)
		require.Empty(t, page.Data.Records)
	})
	t.Run("unlisted_remains_readable", func(t *testing.T) {
		var detail publicapi.PackageDetailResponse
		require.NoError(t, json.Unmarshal([]byte(call(t, "GET", "/packages/cask/unlisted-sample", "", 200)), &detail))
		require.NotNil(t, detail.Data.Indexing)
		require.Equal(t, "hidden", *detail.Data.Indexing.Reason)
		require.Contains(t, detail.Data.Indexing.Pages.Overview.Robots, "noindex")
		call(t, "GET", "/packages/cask/unlisted-sample/releases", "", 200)
		body := call(t, "POST", "/packages/lookup", `{"items":[{"kind":"cask","token":"unlisted-sample"}]}`, 200)
		require.Contains(t, body, `"found":true`)
	})
}
