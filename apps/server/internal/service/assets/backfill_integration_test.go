//go:build integration

package assets

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBackfillDryRunApplyAndRepeat(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	objects := newBackfillObjects()
	service := &Service{Store: st, Objects: objects}
	var before []store.Asset
	wantFiles := map[int64][]store.AssetFile{}
	for _, kind := range []string{"icon", "screenshot"} {
		uploaded, err := service.Upload(ctx, kind, backfillPNG(t, 256), nil, store.AuditActor{})
		require.NoError(t, err)
		asset, err := st.AssetByID(ctx, uploaded.Id)
		require.NoError(t, err)
		var all, retained []store.AssetFile
		require.NoError(t, json.Unmarshal(asset.Files, &all))
		wantFiles[asset.ID] = all
		for _, file := range all {
			if file.Role == "original" || file.Role == "normalized" || (file.Role == "responsive" && file.Width == 64) {
				retained = append(retained, file)
			} else {
				delete(objects.files, file.StorageKey)
			}
		}
		asset.Files, err = json.Marshal(retained)
		require.NoError(t, err)
		require.NoError(t, st.DB.Exec("UPDATE assets SET files=jsonb_set(?::jsonb,'{0,custom}',?::jsonb) WHERE id=?", string(asset.Files), `{"retain":"unknown metadata"}`, asset.ID).Error)
		asset, err = st.AssetByID(ctx, asset.ID)
		require.NoError(t, err)
		before = append(before, asset)
		// If backfill chooses normalized instead of original it now fails.
		objects.files[asset.StorageKey] = []byte("unreadable normalized fixture")
	}
	legacy, err := st.SaveAsset(ctx, store.Asset{Kind: "icon", StorageKey: "icons/legacy/primary.png", URL: objects.URL("icons/legacy/primary.png"), MIME: "image/png", Width: 256, Height: 256, Bytes: 42, SHA256: "legacy-fixture", Files: json.RawMessage(`[]`)}, nil)
	require.NoError(t, err)
	objects.files[legacy.StorageKey] = backfillPNG(t, 256)
	before = append(before, legacy)
	cover, err := st.SaveAsset(ctx, store.Asset{Kind: "cover", StorageKey: "screenshots/cover/primary.png", URL: objects.URL("screenshots/cover/primary.png"), MIME: "image/png", Width: 256, Height: 256, Bytes: 42, SHA256: "cover-fixture"}, nil)
	require.NoError(t, err)
	objects.writes = map[string]int{}
	objects.reads = nil
	cacheKey := "c:pkg:backfill-sentinel"
	require.NoError(t, st.Redis.Set(ctx, cacheKey, "unchanged", time.Hour).Err())
	counts := func() map[string]int64 {
		result := map[string]int64{}
		for _, table := range []string{"assets", "catalog_changes", "job_outbox", "audit_logs"} {
			var n int64
			require.NoError(t, st.DB.Table(table).Count(&n).Error)
			result[table] = n
		}
		return result
	}
	beforeCounts := counts()
	options := BackfillOptions{Rate: 1000, Concurrency: 2}
	p, err := service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 3, p.Processed)
	require.EqualValues(t, 3, p.Pending)
	require.Zero(t, p.Written)
	require.Empty(t, objects.writes)
	require.Empty(t, objects.reads, "dry-run is a manifest preview, not a download or image conversion")
	for _, original := range before {
		current, err := st.AssetByID(ctx, original.ID)
		require.NoError(t, err)
		require.Equal(t, original, current)
	}
	options.Apply = true
	p, err = service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 3, p.Written)
	require.EqualValues(t, 9, p.Files)
	require.Zero(t, p.Pending)
	require.Zero(t, p.Failed)
	for _, original := range before {
		current, err := st.AssetByID(ctx, original.ID)
		require.NoError(t, err)
		var oldJSON, newJSON []json.RawMessage
		require.NoError(t, json.Unmarshal(original.Files, &oldJSON))
		require.NoError(t, json.Unmarshal(current.Files, &newJSON))
		for i := range oldJSON {
			require.JSONEq(t, string(oldJSON[i]), string(newJSON[i]), "existing fields and entries are untouched")
		}
		var files []store.AssetFile
		require.NoError(t, json.Unmarshal(current.Files, &files))
		require.Empty(t, store.MissingAssetRenditions(current.Kind, files))
		if expected, ok := wantFiles[current.ID]; ok {
			require.ElementsMatch(t, expected, files, "original-backed backfill matches the normal upload's bytes, hashes, URLs and dimensions")
		}
		current.Files = original.Files
		require.Equal(t, original, current, "all other asset columns, identity and primary URL stay unchanged")
	}
	coverAfter, err := st.AssetByID(ctx, cover.ID)
	require.NoError(t, err)
	require.Equal(t, cover, coverAfter)
	require.Equal(t, beforeCounts, counts(), "no catalog events, outbox notifications or upload audits")
	require.Equal(t, "unchanged", st.Redis.Get(ctx, cacheKey).Val())
	writes := maps.Clone(objects.writes)
	reads := len(objects.reads)
	var snapshot []store.Asset
	require.NoError(t, st.DB.Order("id").Find(&snapshot).Error)
	p, err = service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.Zero(t, p.Processed)
	require.Zero(t, p.Written)
	require.Equal(t, writes, objects.writes)
	require.Len(t, objects.reads, reads)
	var after []store.Asset
	require.NoError(t, st.DB.Order("id").Find(&after).Error)
	require.Equal(t, snapshot, after, "second apply changes nothing")
}

func TestBackfillFailureResumeAndConcurrentAppend(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	objects := newBackfillObjects()
	service := &Service{Store: st, Objects: objects}
	uploaded, err := service.Upload(ctx, "screenshot", backfillPNG(t, 384), nil, store.AuditActor{})
	require.NoError(t, err)
	asset, err := st.AssetByID(ctx, uploaded.Id)
	require.NoError(t, err)
	var files, base, additions []store.AssetFile
	require.NoError(t, json.Unmarshal(asset.Files, &files))
	for _, file := range files {
		if file.Role == "responsive" || file.Role == "thumbnail" {
			delete(objects.files, file.StorageKey)
			additions = append(additions, file)
		} else {
			base = append(base, file)
		}
	}
	baseJSON, err := json.Marshal(base)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE assets SET files=?::jsonb WHERE id=?", string(baseJSON), asset.ID).Error)
	objects.writes = map[string]int{}
	objects.failKey = additions[len(additions)-1].StorageKey
	options := BackfillOptions{Apply: true, Rate: 1000, Concurrency: 1}
	p, err := service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.Failed)
	require.Zero(t, p.Written)
	current, err := st.AssetByID(ctx, asset.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(baseJSON), string(current.Files), "partial upload cannot publish a partial manifest")
	require.Len(t, objects.writes, 4)
	objects.failKey = ""
	// Two commands may prepare the same files concurrently, then only append
	// entries still missing when each short database transaction acquires its lock.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	added := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			n, err := service.backfillAsset(ctx, asset.ID)
			added <- n.Added
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	close(added)
	for err := range errs {
		require.NoError(t, err)
	}
	totalAdded := 0
	for n := range added {
		totalAdded += n
	}
	require.Equal(t, 5, totalAdded)
	for _, file := range additions {
		require.Equal(t, 1, objects.writes[file.StorageKey], "orphan objects are reused and concurrent retries cannot overwrite")
	}
	// A failed ID cannot starve later IDs in the same pass.
	for i := range 2 {
		key := fmt.Sprintf("icons/failure-resume/%d.png", i)
		_, err := st.SaveAsset(ctx, store.Asset{Kind: "icon", StorageKey: key, URL: objects.URL(key), MIME: "image/png", Width: 256, Height: 256, Bytes: 42, SHA256: "fixture"}, nil)
		require.NoError(t, err)
		objects.files[key] = []byte("corrupt")
		if i == 1 {
			objects.files[key] = backfillPNG(t, 256)
		}
	}
	var failures []string
	options.Progress = func(p BackfillProgress) {
		if p.Failure != "" {
			failures = append(failures, p.Failure)
		}
	}
	p, err = service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.Written)
	require.EqualValues(t, 1, p.Failed)
	require.EqualValues(t, 1, p.Pending)
	require.Equal(t, []string{"process_image:unsupported image"}, failures)
}

func TestBackfillLimitsCancellationAndCandidateSelection(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	objects := newBackfillObjects()
	service := &Service{Store: st, Objects: objects}
	for i := range 3 {
		key := fmt.Sprintf("icons/limits/%d.png", i)
		_, err := st.SaveAsset(ctx, store.Asset{Kind: "icon", StorageKey: key, URL: objects.URL(key), MIME: "image/png", Width: 256, Height: 256, Bytes: 42, SHA256: "fixture"}, nil)
		require.NoError(t, err)
		objects.files[key] = backfillPNG(t, 256)
	}
	options := BackfillOptions{Kind: "screenshot", Rate: 1000, Concurrency: 1}
	p, err := service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.Zero(t, p.Total)
	options.Kind, options.Apply, options.Limit = "icon", true, 1
	p, err = service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.Written)
	require.EqualValues(t, 2, p.Pending)
	// Cancel during the throttle's long wait, without waiting for that interval.
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	options.Limit, options.Rate = 0, 0.001
	var canceledAt time.Time
	options.Progress = func(p BackfillProgress) {
		if p.Written == 1 {
			canceledAt = time.Now()
			cancel()
		}
	}
	p, err = service.BackfillRenditions(cancelCtx, options)
	require.ErrorIs(t, err, context.Canceled)
	require.EqualValues(t, 1, p.Written)
	require.False(t, canceledAt.IsZero())
	require.Less(t, time.Since(canceledAt), 10*time.Second, "cancellation must interrupt the throttle, regardless of image conversion time")
	options.Progress, options.Rate = nil, 1000
	p, err = service.BackfillRenditions(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.Written)
	require.Zero(t, p.Pending)
}

// A blocked object read must leave the row immediately lockable by another
// transaction; NOWAIT makes the regression deterministic, independent of CPU load.
func TestBackfillPreparationDoesNotHoldRowLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st := testutil.NewStore(t)
	objects := newBackfillObjects()
	key := "icons/unlocked/primary.png"
	asset, err := st.SaveAsset(ctx, store.Asset{Kind: "icon", StorageKey: key, URL: objects.URL(key), MIME: "image/png", Width: 256, Height: 256, Bytes: 42, SHA256: "fixture"}, nil)
	require.NoError(t, err)
	objects.files[key] = backfillPNG(t, 256)
	entered, release := make(chan struct{}), make(chan struct{})
	service := &Service{Store: st, Objects: &backfillReadHook{backfillMemoryObjects: objects, afterRead: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}}
	done := make(chan error, 1)
	go func() {
		_, err := service.backfillAsset(ctx, asset.ID)
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("backfill did not reach object read")
	}
	lockErr := st.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Exec("SELECT id FROM assets WHERE id=? FOR UPDATE NOWAIT", asset.ID).Error
	})
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, lockErr, "image preparation must not hold a database row lock")
}

func TestBackfillConcurrentChangesAreDeferred(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	for _, tc := range []struct {
		name, update, reason string
		written, appended    int64
	}{
		{"changed source", `files=jsonb_set(files,'{1,sha256}','"changed-source"'::jsonb)`, "source_changed", 0, 0},
		{"new missing rendition", "files=files - 2", "prepared_renditions_mismatch", 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := newBackfillObjects()
			service := &Service{Store: st, Objects: objects}
			uploaded, err := service.Upload(ctx, "icon", backfillPNG(t, 256), nil, store.AuditActor{})
			require.NoError(t, err)
			asset, err := st.AssetByID(ctx, uploaded.Id)
			require.NoError(t, err)
			var files []store.AssetFile
			require.NoError(t, json.Unmarshal(asset.Files, &files))
			require.Equal(t, "original", files[1].Role)
			require.Equal(t, 64, files[2].Width)
			base, err := json.Marshal(files[:3])
			require.NoError(t, err)
			require.NoError(t, st.DB.Exec("UPDATE assets SET files=?::jsonb WHERE id=?", string(base), asset.ID).Error)
			service.Objects = &backfillReadHook{backfillMemoryObjects: objects, afterRead: func(ctx context.Context) error {
				return st.DB.WithContext(ctx).Exec("UPDATE assets SET "+tc.update+" WHERE id=?", asset.ID).Error
			}}
			var deferred []BackfillProgress
			options := BackfillOptions{Apply: true, Rate: 1000, Concurrency: 1, Progress: func(p BackfillProgress) {
				if p.SkipReason != "" {
					deferred = append(deferred, p)
				}
			}}
			p, err := service.BackfillRenditions(ctx, options)
			require.NoError(t, err)
			require.Zero(t, p.Failed)
			require.Equal(t, tc.written, p.Written)
			require.Equal(t, tc.appended, p.Files)
			require.EqualValues(t, 1, p.Deferred)
			require.EqualValues(t, 1, p.Pending)
			require.Len(t, deferred, 1)
			require.Equal(t, tc.reason, deferred[0].SkipReason)
			require.Equal(t, asset.ID, deferred[0].AssetID)
			require.EqualValues(t, 1, deferred[0].Pending, "a deferred asset must not be counted as complete")
			service.Objects, options.Progress = objects, nil
			p, err = service.BackfillRenditions(ctx, options)
			require.NoError(t, err)
			require.EqualValues(t, 1, p.Written)
			require.Zero(t, p.Deferred)
			require.Zero(t, p.Failed)
			require.Zero(t, p.Pending)
		})
	}
}

type backfillReadHook struct {
	*backfillMemoryObjects
	afterRead func(context.Context) error
}

func (o *backfillReadHook) ReadLimited(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	data, err := o.backfillMemoryObjects.ReadLimited(ctx, key, maxBytes)
	if err != nil {
		return nil, err
	}
	if err := o.afterRead(ctx); err != nil {
		return nil, err
	}
	return data, nil
}
