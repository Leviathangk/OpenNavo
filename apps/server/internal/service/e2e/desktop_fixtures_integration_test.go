//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fixtureReady struct{}

func (fixtureReady) Ready(context.Context) error { return nil }

func TestDesktopFixturesMatchSeedAndProductionSerialization(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	objects := memoryObjects{}
	_, err := (&Service{Store: st, Objects: objects}).Run(ctx, config.Config{AppEnv: "dev", E2EAdminPassword: "e2e-isolated-dev-only"}) //nolint:gosec // Explicit development fixture password, used only in temporary databases on random ports.
	require.NoError(t, err)
	// Rebuild snapshots only in the temporary test database; fixed generation times allow byte-for-byte comparisons.
	require.NoError(t, st.DB.Exec("DELETE FROM catalog_snapshots").Error)
	// Fixed Japanese offline fixtures write only to testcontainers; they call no translation service and never backfill the development database.
	require.NoError(t, st.DB.Exec("INSERT INTO package_i18n(package_id,locale,source_locale,source,status,display_name,summary) SELECT id,'ja-JP','en-US','human','manual','日本語エディター','ソースコードの編集' FROM packages WHERE token='visual-studio-code'").Error)
	clear(objects)
	snapshots := &snapshot.Service{Store: st, Objects: objects, Now: func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }}
	_, err = snapshots.Build(ctx)
	require.NoError(t, err)
	var latest publicapi.CatalogSnapshotInfo
	require.NoError(t, json.Unmarshal(objects["snapshots/latest.json"], &latest))
	var compressed []byte
	for key, data := range objects {
		if strings.HasPrefix(key, "snapshots/base-v2-") {
			compressed = data
		}
	}

	// Simulate updates, deletion and a second page after the snapshot so clients can verify cursor continuation fully.
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		for _, change := range []struct{ token, version, op string }{{"visual-studio-code", "1.140.1", "upsert"}, {"font-jetbrains-mono", "", "delete"}, {"ghostty", "1.3.1", "upsert"}, {"wechat", "4.0.7", "upsert"}} {
			query := "UPDATE packages SET version=?,version_changed_at='2026-10-02T00:00:00Z' WHERE token=?"
			args := []any{change.version, change.token}
			if change.op == "delete" {
				query, args = "UPDATE packages SET removed_at='2026-10-02T00:00:00Z' WHERE token=?", []any{change.token}
			}
			if err := tx.WithContext(ctx).Exec(query, args...).Error; err != nil {
				return err
			}
			if err := tx.WithContext(ctx).Exec("INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,?,'content' FROM packages WHERE token=?", change.op, change.token).Error; err != nil {
				return err
			}
		}
		return nil
	}))
	server, err := httpserver.New(config.Config{AppEnv: "dev", WebBaseURL: "https://opennavo.example"}, slog.New(slog.NewTextHandler(io.Discard, nil)), fixtureReady{}, httpserver.Dependencies{Public: &public.Handler{Snapshot: snapshots, Catalog: &catalog.PublicService{Store: st, WebBaseURL: "https://opennavo.example"}}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(path string, status int) []byte {
		request := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
		reply := httptest.NewRecorder()
		server.Engine.ServeHTTP(reply, request)
		require.Equal(t, status, reply.Code, reply.Body.String())
		testutil.ValidateResponse(t, spec, "/api/v1", request, reply)
		// Save actual response bodies rather than assembling failure messages or fields manually.
		var compact bytes.Buffer
		require.NoError(t, json.Compact(&compact, reply.Body.Bytes()))
		return compact.Bytes()
	}
	files := map[string][]byte{
		"catalog.json.gz":    compressed,
		"latest.json":        objects["snapshots/latest.json"],
		"snapshot.json":      call("/catalog/snapshot", 200),
		"snapshot-all.json":  call("/catalog/snapshot?locales=all", 200),
		"changes-page.json":  call("/catalog/changes?since=60&limit=3", 200),
		"changes-final.json": call("/catalog/changes?since=63&limit=3", 200),
		"changes-empty.json": call("/catalog/changes?since=64&limit=3", 200),
		"client-config.json": call("/config/client?platform=desktop&version=0.1.0&locale=zh-CN", 200),
	}

	// Independent update rules must leave every legacy response byte unchanged.
	feedKey := domain.UpdateFeedKey{BundleID: "com.example.Fixture", Source: "sparkle"}
	require.NoError(t, st.WriteUpdateFeed(ctx, domain.UpdateFeedInput{UpdateFeedKey: feedKey, FeedURL: "https://example.org/feed.xml", Channel: "stable"}, "manual"))
	rule, err := st.UpdateFeed(ctx, feedKey)
	require.NoError(t, err)
	require.NoError(t, st.CheckedUpdateFeed(ctx, *rule, "fixture-etag", nil))
	feedResponse := call("/catalog/update-feeds", 200)
	var feeds struct {
		Data snapshot.CatalogUpdateFeeds `json:"data"`
	}
	require.NoError(t, json.Unmarshal(feedResponse, &feeds))
	require.Len(t, feeds.Data.Items, 1)
	conditional := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/update-feeds", nil)
	conditional.Header.Set("If-None-Match", feeds.Data.ETag)
	cached := httptest.NewRecorder()
	server.Engine.ServeHTTP(cached, conditional)
	require.Equal(t, 304, cached.Code)
	require.Empty(t, cached.Body.Bytes())
	for target, name := range map[string]string{"/catalog/snapshot": "snapshot.json", "/catalog/changes?since=60&limit=3": "changes-page.json", "/catalog/changes?since=63&limit=3": "changes-final.json", "/catalog/changes?since=64&limit=3": "changes-empty.json"} {
		require.Equal(t, files[name], call(target, 200), name)
	}

	// A new link must not change even the empty legacy cursor page or compressed
	// base/language bytes. Exercise production serialization, not copied DTOs.
	beforeLinks, err := snapshots.BuildLocal(ctx)
	require.NoError(t, err)
	home := "https://example.com/link-fixture"
	var linkID int64
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		var err error
		linkID, err = (&store.Store{DB: tx, Redis: st.Redis}).WriteLinkApp(ctx, 0, domain.LinkInput{Token: "link-fixture", Name: "Link Fixture", Homepage: &home, Pricing: "free"})
		return err
	}))
	require.NoError(t, st.DB.Exec(`INSERT INTO package_categories(package_id,category_id,is_primary,source) SELECT ?,c.id,true,'human' FROM categories c WHERE NOT EXISTS(SELECT 1 FROM package_categories pc WHERE pc.category_id=c.id) ORDER BY c.id LIMIT 1`, linkID).Error)
	afterLinks, err := snapshots.BuildLocal(domain.WithLinks(ctx, true))
	require.NoError(t, err)
	require.Equal(t, beforeLinks.Files, afterLinks.Files)
	for path, name := range map[string]string{"/catalog/snapshot": "snapshot.json", "/catalog/changes?since=60&limit=3": "changes-page.json", "/catalog/changes?since=63&limit=3": "changes-final.json", "/catalog/changes?since=64&limit=3": "changes-empty.json"} {
		require.Equal(t, files[name], call(path, 200), name)
	}
	require.NoError(t, st.DB.Exec("UPDATE packages SET version_changed_at='2026-10-16T00:00:00Z' WHERE id=?", linkID).Error)
	var linkEnvelope struct {
		Data snapshot.CatalogLinks `json:"data"`
	}
	linkBytes := call("/catalog/links", 200)
	linkFixture := filepath.Join("..", "..", "..", "testdata", "desktop", "links.json")
	if os.Getenv("UPDATE_LINK_FIXTURE") == "1" {
		require.NoError(t, os.WriteFile(linkFixture, linkBytes, 0o600))
	}
	expectedLinks, err := os.ReadFile(linkFixture)
	require.NoError(t, err)
	var compactLinks bytes.Buffer
	require.NoError(t, json.Compact(&compactLinks, expectedLinks))
	require.Equal(t, compactLinks.Bytes(), linkBytes)
	require.NoError(t, json.Unmarshal(linkBytes, &linkEnvelope))
	require.Len(t, linkEnvelope.Data.Items, 1)
	call("/packages/link/link-fixture", 200)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/links", nil)
	request.Header.Set("If-None-Match", linkEnvelope.Data.ETag)
	reply := httptest.NewRecorder()
	server.Engine.ServeHTTP(reply, request)
	require.Equal(t, 304, reply.Code)
	require.Empty(t, reply.Body.String())
	request = httptest.NewRequest(http.MethodGet, "/api/v1/catalog/changes?since=64&limit=3", nil)
	request.Header.Set("X-OpenNavo-Caps", "link")
	reply = httptest.NewRecorder()
	server.Engine.ServeHTTP(reply, request)
	require.Equal(t, 200, reply.Code)
	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, reply.Body.Bytes()))
	require.Equal(t, files["changes-empty.json"], compact.Bytes())
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error { return (&store.Store{DB: tx, Redis: st.Redis}).RemoveLinkApp(ctx, linkID) }))
	require.Equal(t, files["changes-empty.json"], call("/catalog/changes?since=64&limit=3", 200))
	for _, locale := range i18n.DesktopLocales {
		for key, data := range objects {
			if strings.HasPrefix(key, "snapshots/text-v2-"+string(locale)+"-") {
				files["text-"+string(locale)+".json.gz"] = data
			}
		}
	}
	require.NoError(t, st.DB.Exec("INSERT INTO app_config(key,value) VALUES('catalog.retentionCursor','60'::jsonb) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value").Error)
	files["changes-expired.json"] = call("/catalog/changes?since=0&limit=3&locale=zh-CN", 410)
	directory := filepath.Join("..", "..", "..", "testdata", "desktop", "v2")
	if os.Getenv("UPDATE_DESKTOP_FIXTURES") == "1" {
		require.NoError(t, os.MkdirAll(directory, 0o750))
		for name, data := range files {
			require.NoError(t, os.WriteFile(filepath.Join(directory, name), data, 0o600))
		}
	}
	for name, data := range files {
		previous, err := os.ReadFile(filepath.Join(directory, name)) //nolint:gosec // directory is a fixed fixture directory; name comes only from the finite file set generated by this test.
		require.NoError(t, err, "run node tooling/scripts/tasks/server.mjs desktop-fixtures after intentional format changes")
		require.Equal(t, previous, data, name+": current seeded serialization differs from committed fixture")
	}
}
