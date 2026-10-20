//go:build integration

package taps

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTapConfirmationRefreshesOnlySuccessfulCurrentRegistration(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-48 * time.Hour)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"confirmed-tap","tap":"fixture/apps","name":["Tap"],"version":"1"}`))
	require.NoError(t, err)
	_, err = st.ApplyCatalogBatchWithLifecycle(ctx, []store.CatalogMutation{{Package: p}}, old)
	require.NoError(t, err)
	row, err := st.CatalogRow(ctx, "cask", p.Token)
	require.NoError(t, err)
	require.NoError(t, st.RegisterTapCask(ctx, row.ID, p.Tap, "Casks/confirmed-tap.rb", "fixture-etag", "fixture-head", "fixture-head", old))
	registration, err := st.TapCask(ctx, row.ID)
	require.NoError(t, err)
	svc := &Service{Store: st, Now: func() time.Time { return now }}
	outcome, _, err := svc.finishRefresh(ctx, *registration, nil, commit{SHA: "fixture-head", ETag: "fixture-etag"}, false, nil)
	require.NoError(t, err)
	require.Equal(t, "unchanged", outcome)
	read := func() time.Time {
		var at time.Time
		require.NoError(t, st.DB.Raw("SELECT homebrew_checked_at FROM packages WHERE id=?", row.ID).Scan(&at).Error)
		return at
	}
	require.WithinDuration(t, now, read(), time.Microsecond)
	success := now
	now = now.Add(time.Hour)
	// A 304 belongs to the definition sent with the request, not a replacement
	// committed while that request was in flight under the same registration.
	require.NoError(t, st.DB.Exec("UPDATE packages SET raw_hash='newer-definition' WHERE id=?", row.ID).Error)
	_, _, err = svc.finishRefresh(ctx, *registration, nil, commit{SHA: "fixture-head", ETag: "fixture-etag"}, false, nil)
	require.NoError(t, err)
	require.WithinDuration(t, success, read(), time.Microsecond)
	require.NoError(t, st.DB.Exec("UPDATE packages SET raw_hash=? WHERE id=?", p.RawHash, row.ID).Error)
	require.NoError(t, svc.recordFailure(ctx, *registration, "fixture failed read"))
	require.WithinDuration(t, success, read(), time.Microsecond)
	_, _, err = svc.finishRefresh(ctx, *registration, nil, commit{}, true, nil)
	require.NoError(t, err)
	require.WithinDuration(t, success, read(), time.Microsecond)
	// A successful stale callback cannot revive or refresh a removed row.
	_, _, err = svc.finishRefresh(ctx, *registration, nil, commit{}, false, nil)
	require.NoError(t, err)
	require.WithinDuration(t, success, read(), time.Microsecond)
}
