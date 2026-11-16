//go:build integration

package assets

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type sizeStub func(context.Context, string) (*int64, error)

func (f sizeStub) Size(ctx context.Context, target string) (*int64, error) { return f(ctx, target) }
func TestDownloadSizeAllVisibleUnknownAndStaleVersion(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1","url":"https://download.example.test/app.dmg"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE packages SET rank_30d=10").Error)
	row, err := st.PublicPackage(ctx, "cask", "sample", "zh-CN")
	require.NoError(t, err)
	n := int64(123)
	calls := 0
	svc := &SizeService{Store: st, Fetcher: sizeStub(func(context.Context, string) (*int64, error) { calls++; return &n, nil })}
	stats, err := svc.Run(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, true, stats["changed"])
	stats, err = svc.Run(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, false, stats["changed"])
	svc.Fetcher = sizeStub(func(context.Context, string) (*int64, error) { return nil, nil })
	_, err = svc.Run(ctx, row.ID)
	require.NoError(t, err)
	row, err = st.PublicPackage(ctx, "cask", "sample", "zh-CN")
	require.NoError(t, err)
	require.Nil(t, row.DownloadSize)
	svc.Fetcher = sizeStub(func(context.Context, string) (*int64, error) {
		require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE packages SET version='2'").Error)
		return &n, nil
	})
	stats, err = svc.Run(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, false, stats["changed"])
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE packages SET rank_30d=2001").Error)
	stats, err = svc.Run(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, true, stats["changed"])
	require.Equal(t, 2, calls)

	// A catalog sync can hide or remove a package after its size job was queued. The job has nothing
	// left to measure and must not keep retrying a row that is intentionally out of scope.
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE packages SET removed_at=now() WHERE id=?", row.ID).Error)
	stats, err = svc.Run(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, true, stats["skipped"])
}

func TestDownloadSizeSkipsHiddenPackage(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"hidden-sample","version":"1","url":"https://download.example.test/app.dmg"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	row, err := st.PublicPackage(ctx, "cask", "hidden-sample", "zh-CN")
	require.NoError(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO package_meta(package_id,hidden) VALUES (?,true)
		ON CONFLICT (package_id) DO UPDATE SET hidden=true`, row.ID).Error)

	svc := &SizeService{Store: st, Fetcher: sizeStub(func(context.Context, string) (*int64, error) {
		t.Fatal("a hidden package must not be measured")
		return nil, nil
	})}
	stats, err := svc.Run(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, true, stats["skipped"])
}

func TestDailySizesOnlyQueueMissingVisibleCasks(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	for _, token := range []string{"eligible", "known", "hidden", "disabled", "font-sample", "removed"} {
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","version":"1","url":"https://example.test/file"}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	require.NoError(t, st.DB.Exec(`UPDATE packages SET download_size=7 WHERE token='known'; UPDATE packages SET disabled=true WHERE token='disabled'; UPDATE packages SET removed_at=now() WHERE token='removed'; INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden'`).Error)
	svc := &SizeService{Store: st}
	result, err := svc.Run(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, result["enqueued"])
	var token string
	require.NoError(t, st.DB.Raw(`SELECT p.token FROM job_outbox o JOIN packages p ON p.id=(o.payload->>'packageId')::bigint WHERE o.job_type='assets:download-size'`).Scan(&token).Error)
	require.Equal(t, "eligible", token)
	result, err = svc.Run(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 0, result["enqueued"])
}
