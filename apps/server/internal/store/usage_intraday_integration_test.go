//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestUsageIntradayFirstActivityAndDailyTotals(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	midnight := time.Date(2026, 10, 29, 0, 0, 0, 0, zone)
	activity := store.UsageActivity{AnonymousID: uuid.NewString(), Platform: "desktop", Version: "1.0.0", Locale: "en-US"}
	record := func(at time.Time) {
		activity.OccurredAt = at
		require.NoError(t, st.RecordUsage(ctx, activity, at))
	}
	check := func(now time.Time) store.UsageIntraday {
		report, err := st.UsageIntraday(ctx, now)
		require.NoError(t, err)
		overview, err := st.UsageOverview(ctx, now)
		require.NoError(t, err)
		last := report.Hours[now.In(zone).Hour()]
		today := overview.Days[len(overview.Days)-1]
		require.Equal(t, today.Web, *last.CumulativeWeb)
		require.Equal(t, today.Desktop, *last.CumulativeDesktop)
		return report
	}
	// Beijing 23:59 belongs to yesterday; midnight creates a fresh daily observation.
	record(midnight.Add(-time.Second))
	yesterday := check(midnight.Add(-time.Second))
	require.EqualValues(t, 1, *yesterday.Hours[23].Desktop)
	record(midnight)
	start := check(midnight)
	require.EqualValues(t, 1, *start.Hours[0].Desktop)
	require.Nil(t, start.Hours[1].Desktop)
	// A version upgrade at 01:00 updates last activity but cannot move the 00:00 observation.
	activity.Version = "1.1.0"
	record(midnight.Add(time.Hour))
	// A late retry must not move the observation either.
	activity.OccurredAt = midnight.Add(30 * time.Minute)
	require.NoError(t, st.RecordUsage(ctx, activity, midnight.Add(2*time.Hour)))
	var times struct{ FirstActiveAt, LastActiveAt time.Time }
	require.NoError(t, st.DB.Table("usage_daily").Select("first_active_at,last_active_at").Where("day='2026-10-29' AND platform='desktop'").Scan(&times).Error)
	require.True(t, times.FirstActiveAt.Equal(midnight))
	require.True(t, times.LastActiveAt.Equal(midnight.Add(time.Hour)))
	// Same ID on the web is a separate browser. Exercise both sides of the hour boundary.
	activity.Platform = "web"
	record(midnight.Add(time.Hour - time.Second))
	beforeHour := check(midnight.Add(time.Hour - time.Second))
	require.EqualValues(t, 1, *beforeHour.Hours[0].Web)
	require.Nil(t, beforeHour.Hours[1].Web)
	activity.AnonymousID = uuid.NewString()
	record(midnight.Add(time.Hour))
	report := check(midnight.Add(time.Hour))
	require.EqualValues(t, 1, *report.Hours[0].Desktop)
	require.Zero(t, *report.Hours[1].Desktop)
	require.EqualValues(t, 1, *report.Hours[0].Web)
	require.EqualValues(t, 1, *report.Hours[1].Web)
	require.EqualValues(t, 2, *report.Hours[1].CumulativeWeb)
	require.EqualValues(t, 1, *report.Hours[0].CumulativeWeb)
	// Empty hours are zero, future hours stay unknown, and day rollover resets the totals.
	report = check(midnight.Add(3 * time.Hour))
	require.Zero(t, *report.Hours[2].Web)
	require.EqualValues(t, 2, *report.Hours[3].CumulativeWeb)
	require.Nil(t, report.Hours[4].Web)
	report = check(midnight.Add(24 * time.Hour))
	require.Zero(t, *report.Hours[0].CumulativeWeb)
	require.Zero(t, *report.Hours[0].CumulativeDesktop)
}
