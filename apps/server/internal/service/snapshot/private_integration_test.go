//go:build integration

package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPrivateSnapshotPublicationRevokesAllPriorObjects(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	for _, token := range []string{"snapshot-public", "snapshot-private"} {
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","version":"1","desc":"Snapshot summary"}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	objects := memoryObjects{}
	purges := []string{}
	failPurge := false
	svc := &Service{Store: st, Objects: objects, Purge: func(_ context.Context, prefixes []string) error {
		purges = append(purges, prefixes...)
		if failPurge {
			return errors.New("injected CDN failure")
		}
		return nil
	}}
	_, err := svc.Build(ctx)
	require.NoError(t, err)
	old, err := objects.List(ctx, "snapshots/")
	require.NoError(t, err)
	require.Len(t, old, 13, "legacy base, modern base, ten text packs and latest manifest")
	// An interrupted publication and a pre-v2 archive must also be revoked.
	objects["snapshots/orphan-upload.json.gz"] = []byte("old private data")
	objects["snapshots/catalog-v1.json.gz"] = []byte("old private data")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,private,visibility_reason)
SELECT id,true,'Owner decision' FROM packages WHERE token='snapshot-private'`).Error)
	_, err = svc.Latest(ctx)
	require.Error(t, err, "metadata is unavailable until revocation completes")
	// A failed purge must not acknowledge privacy or lose the durable retry.
	failPurge = true
	require.ErrorContains(t, svc.RevokePrivate(ctx), "injected CDN failure")
	state, err := st.SnapshotVisibility(ctx)
	require.NoError(t, err)
	require.Greater(t, state.RevocationGeneration, state.SnapshotRevocation)
	var queued int64
	require.NoError(t, st.DB.Table("job_outbox").Where("unique_key='snapshot:private'").Count(&queued).Error)
	require.Equal(t, int64(1), queued)
	failPurge = false
	require.NoError(t, svc.RevokePrivate(ctx))
	latest, err := svc.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, latest.ItemCount)
	require.Contains(t, purges, "cdn.example.test/snapshots/")
	for _, key := range append(old, "snapshots/orphan-upload.json.gz", "snapshots/catalog-v1.json.gz") {
		if key != "snapshots/latest.json" {
			require.NotContains(t, objects, key, "old public URLs must be unaddressable")
		}
	}
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(objects["snapshots/latest.json"], &manifest))
	require.Equal(t, latest.Url, manifest["url"])
	for key, data := range objects {
		if !strings.HasSuffix(key, ".gz") {
			continue
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		require.NoError(t, err)
		raw, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.NotContains(t, string(raw), "snapshot-private", key)
		if strings.Contains(key, "base-v2") || strings.Contains(key, "en-US") {
			require.Contains(t, string(raw), "snapshot-public", key)
		}
	}
	var records int64
	require.NoError(t, st.DB.Table("catalog_snapshots").Count(&records).Error)
	require.Equal(t, int64(1), records)
	state, err = st.SnapshotVisibility(ctx)
	require.NoError(t, err)
	require.Equal(t, state.RevocationGeneration, state.SnapshotRevocation)
	_, err = svc.Build(ctx)
	require.NoError(t, err)
	latest, err = svc.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, latest.ItemCount, "later publishers cannot revive the private package")
}

type pausedSnapshotObjects struct {
	memoryObjects
	pause   atomic.Bool
	started chan struct{}
	finish  chan struct{}
}

func (m *pausedSnapshotObjects) Put(ctx context.Context, key string, data []byte, mime string) error {
	if m.pause.CompareAndSwap(true, false) {
		close(m.started)
		select {
		case <-m.finish:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.memoryObjects.Put(ctx, key, data, mime)
}

func TestSnapshotRevocationWaitsForInflightPublisher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"inflight-private","version":"1","desc":"Inflight private"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	objects := &pausedSnapshotObjects{memoryObjects: memoryObjects{}, started: make(chan struct{}), finish: make(chan struct{})}
	svc := &Service{Store: st, Objects: objects, Purge: func(context.Context, []string) error { return nil }}
	objects.pause.Store(true)
	published := make(chan error, 1)
	go func() { _, err := svc.Build(ctx); published <- err }()
	<-objects.started // The bundle still contains the public package.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,private,visibility_reason) SELECT id,true,'Owner decision' FROM packages WHERE token='inflight-private'`).Error)
	revoked := make(chan error, 1)
	go func() { revoked <- svc.RevokePrivate(ctx) }()
	select {
	case err := <-revoked:
		t.Fatalf("revocation must join the active publisher: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(objects.finish)
	require.NoError(t, <-published)
	require.NoError(t, <-revoked)
	latest, err := svc.Latest(ctx)
	require.NoError(t, err)
	require.Zero(t, latest.ItemCount)
	for key, data := range objects.memoryObjects {
		if !strings.HasSuffix(key, ".gz") {
			continue
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		require.NoError(t, err)
		raw, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.NotContains(t, string(raw), "inflight-private", "an older publisher must not revive a revoked object")
	}
}
