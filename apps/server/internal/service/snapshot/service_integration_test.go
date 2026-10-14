//go:build integration

package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type memoryObjects map[string][]byte

func (m memoryObjects) Put(_ context.Context, k string, b []byte, _ string) error {
	m[k] = bytes.Clone(b)
	return nil
}
func (m memoryObjects) URL(k string) string { return "https://cdn.example.test/" + k }
func TestSnapshotConsistencyIncrementalPaginationRetentionAndAnalytics(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	seed, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, seed, "", ""))
	objects := memoryObjects{}
	now := time.Now().UTC()
	service := &Service{Store: st, Objects: objects, Now: func() time.Time { return now }}
	for _, raw := range []string{`{"token":"sample","name":["微信"],"version":"1","desc":"Messaging","artifacts":[{"app":["Sample.app"]}]}`, `{"token":"hidden","version":"2"}`, `{"token":"removed","version":"3"}`} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	legacy, err := homebrew.Normalize("formula", json.RawMessage(`{"name":"legacy","versions":{"stable":"1"}}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: legacy}}))
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden'`).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`UPDATE packages SET removed_at=now() WHERE token='removed'`).Error)
	// Pre-upgrade mixed snapshots must not continue to be distributed as the latest catalog.
	require.NoError(t, st.SaveSnapshot(ctx, store.SnapshotRecord{FormatVersion: 1, StorageKey: "snapshots/catalog-old.json.gz", URL: "https://cdn.example.test/old.json.gz", SHA256: string(bytes.Repeat([]byte("a"), 64)), Bytes: 1, ItemCount: 4, CreatedAt: now.Add(time.Hour)}))
	_, err = service.Latest(ctx)
	require.Error(t, err)
	stats, err := service.Build(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stats["items"])
	meta, err := service.Latest(ctx)
	require.NoError(t, err)
	data := objects["snapshots/latest.json"]
	require.Contains(t, string(data), meta.Sha256)
	var record store.SnapshotRecord
	record, err = st.LatestSnapshot(ctx)
	require.NoError(t, err)
	compressed := objects[record.StorageKey]
	hash := sha256.Sum256(compressed)
	require.Equal(t, meta.Sha256, hex.EncodeToString(hash[:]))
	require.Equal(t, int64(len(compressed)), meta.Bytes)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var doc Document
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Len(t, doc.Items, 2)
	require.Len(t, doc.Categories, 20)
	require.True(t, doc.Items[0].Hidden)
	require.Equal(t, []string{"Sample.app"}, doc.Items[1].Apps)
	require.Contains(t, *doc.Items[1].Pinyin, "weixin")
	stats, err = service.Build(ctx)
	require.NoError(t, err)
	require.Equal(t, true, stats["skipped"])
	// Statistics updates create no changes, but a new statistics run must force a snapshot refresh.
	now = now.Add(time.Minute)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO sync_runs(job_type,trigger,status,finished_at) VALUES('analytics:sync','manual','succeeded',?)`, now.Add(-time.Second)).Error)
	stats, err = service.Build(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stats["items"])
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,'upsert','content' FROM packages WHERE token='sample'`).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,'upsert','asset' FROM packages WHERE token='sample'`).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,'delete','homebrew' FROM packages WHERE token='removed'`).Error)
	page, err := service.Changes(ctx, meta.Cursor, 1)
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Len(t, page.Changes, 1)
	require.Equal(t, "sample", page.Changes[0].Token)
	require.NotNil(t, page.Changes[0].Item)
	page, err = service.Changes(ctx, page.NextCursor, 1)
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Equal(t, "delete", string(page.Changes[0].Op))
	require.Nil(t, page.Changes[0].Item)
	end := page.NextCursor
	// Advance the global cursor even when only old Formula entries changed, without returning catalog entries.
	require.NoError(t, st.DB.Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,'upsert','content' FROM packages WHERE token='legacy'`).Error)
	page, err = service.Changes(ctx, end, 500)
	require.NoError(t, err)
	require.Empty(t, page.Changes)
	require.False(t, page.HasMore)
	require.Greater(t, page.NextCursor, end)
	end = page.NextCursor
	page, err = service.Changes(ctx, end, 500)
	require.NoError(t, err)
	require.Empty(t, page.Changes)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`UPDATE catalog_changes SET created_at=now()-interval '15 days' WHERE seq<=?`, meta.Cursor+1).Error)
	_, err = service.Changes(ctx, 0, 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "1201")
	_, err = st.PruneCatalogChanges(ctx, time.Now().Add(-14*24*time.Hour))
	require.NoError(t, err)
	_, err = service.Changes(ctx, end+100, 500)
	require.Error(t, err)
	_, err = service.Changes(ctx, -1, 500)
	require.Error(t, err)
	// Renames must retain both old-token deletion and new-token upsert, even with the same package_id.
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,'previous','delete','homebrew' FROM packages WHERE token='sample'`).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,'upsert','homebrew' FROM packages WHERE token='sample'`).Error)
	page, err = service.Changes(ctx, end, 500)
	require.NoError(t, err)
	require.Len(t, page.Changes, 2)
	now = now.Add(25 * time.Hour)
	stats, err = service.Build(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stats["items"])
}

func TestCatalogCursorCannotCommitOutOfOrder(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"ordered","version":"1"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- st.WithTx(ctx, func(tx *gorm.DB) error {
			if err := store.LockCatalogWrites(ctx, tx); err != nil {
				return err
			}
			if err := tx.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,'upsert','content' FROM packages`).Error; err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	second := make(chan error, 1)
	p.Version = "2"
	go func() { second <- st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}) }()
	select {
	case err := <-second:
		t.Fatalf("second writer passed uncommitted cursor: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, <-second)
	page, err := (&Service{Store: st}).Changes(ctx, 1, 500)
	require.NoError(t, err)
	require.Len(t, page.Changes, 1)
	require.Equal(t, "2", page.Changes[0].Item.Version)
}

type failedObjects struct {
	memoryObjects
	writes int
	failAt int
}

func (m *failedObjects) Put(ctx context.Context, key string, data []byte, contentType string) error {
	m.writes++
	if m.failAt > 0 && m.writes == m.failAt {
		return errors.New("simulated interrupted upload")
	}
	return m.memoryObjects.Put(ctx, key, data, contentType)
}
func TestV2SparsePacksCategoryChangesAndInterruptedPublication(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	seed, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, seed, "", ""))
	normalized, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"editor","name":["Editor"],"version":"1","desc":"English summary"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: normalized}}))
	require.NoError(t, st.DB.Exec("INSERT INTO package_i18n(package_id,locale,source_locale,source,status,display_name) SELECT id,'ja-JP','en-US','human','manual','日本語エディター' FROM packages WHERE token='editor'").Error)
	objects := &failedObjects{memoryObjects: memoryObjects{}}
	now := time.Now().UTC()
	svc := &Service{Store: st, Objects: objects, Now: func() time.Time { return now }}
	_, err = svc.Build(ctx)
	require.NoError(t, err)
	latest, err := svc.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, latest.FormatVersion)
	require.NotNil(t, latest.TextPacks)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	for key, data := range objects.memoryObjects {
		if key == "snapshots/latest.json" {
			continue
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		require.NoError(t, err)
		raw, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		var value map[string]any
		require.NoError(t, json.Unmarshal(raw, &value))
		schema := "CatalogTextPack"
		if strings.HasPrefix(key, "snapshots/base-v2-") {
			schema = "CatalogBaseSnapshot"
			entry := value["items"].([]any)[0].(map[string]any)
			require.NotContains(t, entry, "displayName")
			require.NotContains(t, entry, "summary")
			require.Equal(t, "en-US", entry["sourceLocale"])
		} else if value["locale"] == "ja-JP" {
			entry := value["items"].([]any)[0].(map[string]any)
			require.Equal(t, "日本語エディター", entry["displayName"])
			require.NotContains(t, entry, "summary")
		} else if value["locale"] == "ru-RU" {
			require.Empty(t, value["items"])
		}
		require.NoError(t, spec.Components.Schemas[schema].Value.VisitJSON(value))
	}
	changes, err := svc.Changes(ctx, 0, 500)
	require.NoError(t, err)
	require.NotNil(t, changes.Categories)
	require.NotNil(t, changes.CategoryNames)
	require.Len(t, changes.Changes, 1)
	encoded, err := json.Marshal(changes.Changes[0].Item)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"ja-JP":"日本語エディター"`)
	require.NotContains(t, string(encoded), `"ru-RU"`)
	require.NotContains(t, string(encoded), `"zh-CN":null`)
	oldLatest := bytes.Clone(objects.memoryObjects["snapshots/latest.json"])
	oldRecord, err := st.LatestSnapshot(ctx)
	require.NoError(t, err)
	// Rebuild for category text changes at the same cursor too; increment pack versions and do not reuse manifests tied to the old base hash.
	now = now.Add(time.Minute)
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name=name || ' updated',updated_at=? WHERE locale='en-US'", now).Error)
	objects.writes = 0
	objects.failAt = 4
	_, err = svc.Build(ctx)
	require.Error(t, err)
	require.Equal(t, oldLatest, objects.memoryObjects["snapshots/latest.json"])
	current, err := st.LatestSnapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, oldRecord.ID, current.ID)
	objects.writes = 0
	objects.failAt = 0
	_, err = svc.Build(ctx)
	require.NoError(t, err)
	fresh, err := svc.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, latest.Cursor, fresh.Cursor)
	require.Greater(t, fresh.TextPacks.EnUS.Version, latest.TextPacks.EnUS.Version)
	require.NotEqual(t, latest.Sha256, fresh.Sha256)
}

// Exercise the published gzip, including CatalogItem -> CatalogBaseItem conversion.
func TestV2PublishedSnapshotPreservesTapAppTargets(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	raw, err := homebrew.ParseCaskSource(`cask "demo" do
version "1"
name "Demo"
sha256 :no_check
url "https://example.com/demo.zip"
app "Source.app", target: "Renamed.app"
app "folder/Other.app"
end`, homebrew.CaskSource{Tap: "example/tap", Token: "demo"}, time.Now())
	require.NoError(t, err)
	pkg, err := homebrew.Normalize("cask", raw)
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	objects := memoryObjects{}
	svc := &Service{Store: st, Objects: objects}
	_, err = svc.Build(ctx)
	require.NoError(t, err)
	latest, err := svc.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, latest.FormatVersion)
	data, ok := objects[strings.TrimPrefix(latest.Url, objects.URL(""))]
	require.True(t, ok, "manifest must reference the uploaded base snapshot")
	sum := sha256.Sum256(data)
	require.Equal(t, latest.Sha256, hex.EncodeToString(sum[:]))
	reader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	// Decode without generated models so a missing field cannot be hidden by a default.
	var document map[string]any
	require.NoError(t, json.Unmarshal(decoded, &document))
	entries := document["items"].([]any)
	require.Len(t, entries, 1)
	entry := entries[0].(map[string]any)
	require.Equal(t, "example/tap", entry["tap"])
	require.Equal(t, []any{"Source.app", "folder/Other.app"}, entry["apps"])
	require.Equal(t, []any{"/Applications/Renamed.app", "/Applications/Other.app"}, entry["appTargets"])
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	require.NoError(t, spec.Components.Schemas["CatalogBaseSnapshot"].Value.VisitJSON(document))
	changes, err := svc.Changes(ctx, 0, 100)
	require.NoError(t, err)
	require.Len(t, changes.Changes, 1)
	require.NotNil(t, changes.Changes[0].Item.AppTargets)
	require.Equal(t, []string{"/Applications/Renamed.app", "/Applications/Other.app"}, *changes.Changes[0].Item.AppTargets)
}
