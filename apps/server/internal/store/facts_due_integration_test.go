//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDueFactRecordsListVerifiedRecordsNearExpiry(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,popularity) VALUES
 (801,'cask','due-middle','due-middle','homebrew/cask','Due Middle','1','1','{}','fixture',50),
 (802,'cask','due-popular','due-popular','homebrew/cask','Due Popular','1','1','{}','fixture',90),
 (803,'cask','due-removed','due-removed','homebrew/cask','Due Removed','1','1','{}','fixture',95),
 (804,'link','due-link','due-link','','Due Link','1','1','{}','fixture',10)`).Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE id=803").Error)
	record := func(id, pkg int64, kind, key, status string, until *time.Time) {
		t.Helper()
		evidence := map[string]any{"sourceUrl": "https://vendor.example/docs", "checkedAt": now.Add(-30 * 24 * time.Hour).Format(time.RFC3339), "status": status, "validUntil": nil}
		if until != nil {
			evidence["validUntil"] = until.Format(time.RFC3339)
		}
		raw, err := json.Marshal(evidence)
		require.NoError(t, err)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(id,package_id,kind,key,value,evidence) VALUES(?,?,?,?,'{"name":"open_source","booleanValue":true}',?::jsonb)`, id, pkg, kind, key, string(raw)).Error)
	}
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	record(1, 801, "attribute", "soon", "verified", at(3*24*time.Hour))
	record(2, 801, "price", "expired", "verified", at(-24*time.Hour))
	record(3, 802, "attribute", "soon", "verified", at(5*24*time.Hour))
	record(4, 802, "issue", "later", "verified", at(40*24*time.Hour))
	record(5, 803, "attribute", "removed-app", "verified", at(24*time.Hour))
	record(6, 804, "attribute", "under-review", "needs_review", at(24*time.Hour))
	record(7, 804, "attribute", "no-expiry", "verified", nil)
	record(8, 804, "attribute", "link-soon", "verified", at(10*24*time.Hour))

	rows, total, err := st.DueFactRecords(ctx, now.Add(14*24*time.Hour), "", 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 4, total)
	ids := []int64{}
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	// The most popular apps first, then the earliest validUntil.
	require.Equal(t, []int64{3, 2, 1, 8}, ids)
	require.Equal(t, "due-popular", rows[0].Token)
	require.Equal(t, "cask", rows[0].PackageKind)
	require.Equal(t, "link", rows[3].PackageKind)
	require.JSONEq(t, `{"name":"open_source","booleanValue":true}`, string(rows[0].Value))
	require.Equal(t, "stale", rows[1].Evidence.At(now).Status, "expired records are already unverified on the site")

	rows, total, err = st.DueFactRecords(ctx, now, "", 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "a zero-day window lists expired records only")
	require.EqualValues(t, 2, rows[0].ID)
	rows, total, err = st.DueFactRecords(ctx, now.Add(14*24*time.Hour), "price", 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "price", rows[0].Kind)
	rows, total, err = st.DueFactRecords(ctx, now.Add(14*24*time.Hour), "", 2, 3)
	require.NoError(t, err)
	require.EqualValues(t, 4, total)
	require.Len(t, rows, 1)
	require.EqualValues(t, 8, rows[0].ID)
	_, _, err = st.DueFactRecords(ctx, now, "unknown", 1, 20)
	var app *domain.AppError
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: record_kind_invalid:kind", app.Msg)
}
