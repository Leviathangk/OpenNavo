//go:build integration

package management

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type privateSnapshotObjects map[string][]byte

func (m privateSnapshotObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	m[key] = bytes.Clone(data)
	return nil
}
func (m privateSnapshotObjects) URL(key string) string { return "https://cdn.example.test/" + key }
func (m privateSnapshotObjects) Delete(_ context.Context, key string) error {
	delete(m, key)
	return nil
}
func (m privateSnapshotObjects) List(_ context.Context, prefix string) ([]string, error) {
	keys := []string{}
	for key := range m {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func TestPrivateWriteRevokesPublishedSnapshotBeforeSuccess(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
VALUES(994,'cask','private-write','private-write','homebrew/cask','Private write','1','1','{}','fixture');
INSERT INTO package_meta(package_id) VALUES(994)`).Error)
	objects := privateSnapshotObjects{}
	purges := 0
	snapshots := &snapshot.Service{Store: st, Objects: objects, Purge: func(_ context.Context, prefixes []string) error {
		purges++
		require.Equal(t, []string{"cdn.example.test/snapshots/"}, prefixes)
		return nil
	}}
	_, err := snapshots.Build(ctx)
	require.NoError(t, err)
	old, err := snapshots.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, old.ItemCount)
	svc := &Service{Store: st, Snapshots: snapshots}
	in := Input{ID: 994, Body: map[string]any{"private": true, "visibilityReason": "Owner decision"}}
	before := bytes.Clone(objects["snapshots/latest.json"])
	_, err = svc.Execute(domain.WithOperation(ctx, &domain.Operation{DryRun: true}), "UpdatePackageMeta", in)
	require.NoError(t, err)
	require.Equal(t, before, objects["snapshots/latest.json"])
	require.Zero(t, purges, "previews must neither publish nor revoke")
	_, err = svc.Execute(ctx, "UpdatePackageMeta", in)
	require.NoError(t, err)
	fresh, err := snapshots.Latest(ctx)
	require.NoError(t, err)
	require.Zero(t, fresh.ItemCount)
	require.NotEqual(t, old.Url, fresh.Url)
	require.NotContains(t, objects, strings.TrimPrefix(old.Url, objects.URL("")))
	require.Equal(t, 1, purges, "a successful private write includes CDN revocation")
}
