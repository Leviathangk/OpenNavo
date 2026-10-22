//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestUsageDailyDedupOfflineDatesVersionsAndRetention(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC) // midnight Oct 23 in Shanghai
	a := store.UsageActivity{AnonymousID: "2a77e45e-1ac2-4d72-b9c2-aaf2a42f8234", Platform: "desktop", OccurredAt: now, Version: "0.1.3", Locale: "zh-CN"}
	var wg sync.WaitGroup
	errors := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- st.RecordUsage(ctx, a, now) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	// Same ID on another platform is not cross-device identity resolution.
	web := a
	web.Platform = "web"
	web.Version = ""
	require.NoError(t, st.RecordUsage(ctx, web, now))
	// Upgrade later today, then deliver an older version late: the newest version wins.
	a.Version = "0.1.4"
	a.OccurredAt = now.Add(time.Minute)
	require.NoError(t, st.RecordUsage(ctx, a, now.Add(time.Minute)))
	a.Version = "0.1.3"
	a.OccurredAt = now
	require.NoError(t, st.RecordUsage(ctx, a, now.Add(time.Minute)))
	// Offline activity goes to its original date and moves the first-seen date backward.
	a.OccurredAt = now.Add(-time.Second)
	require.NoError(t, st.RecordUsage(ctx, a, now))
	report, err := st.UsageOverview(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, report.Days, 30)
	require.Equal(t, store.UsageDay{Date: "2026-10-23", Desktop: 1, Web: 1, NewWeb: 1}, report.Days[29])
	require.Equal(t, store.UsageDay{Date: "2026-10-22", Desktop: 1, NewDesktop: 1}, report.Days[28])
	require.Equal(t, []store.UsageVersion{{Version: "0.1.4", Count: 1}}, report.Versions)
	a.OccurredAt = now.Add(-8 * 24 * time.Hour)
	require.Error(t, st.RecordUsage(ctx, a, now))
	_, err = st.PruneLogs(ctx, now.Add(time.Hour))
	require.NoError(t, err)
	report, err = st.UsageOverview(ctx, now)
	require.NoError(t, err)
	require.Empty(t, report.Versions)
}

func TestUsageFastClockAcknowledgementMatchesPersistedMidnightDays(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 15, 58, 0, 0, time.UTC)
	a := store.UsageActivity{AnonymousID: "2a77e45e-1ac2-4d72-b9c2-aaf2a42f8234", Platform: "web", OccurredAt: now.Add(3 * time.Minute), Locale: "en-US"}
	require.NoError(t, st.RecordUsage(ctx, a, now))
	require.Equal(t, "2026-10-22", a.ReportingDay(now))
	report, err := st.UsageOverview(ctx, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 1, report.Days[28].Web)
	require.Zero(t, report.Days[29].Web)
	now = now.Add(2 * time.Minute)
	a.OccurredAt = now.Add(3 * time.Minute)
	require.NoError(t, st.RecordUsage(ctx, a, now))
	require.Equal(t, "2026-10-23", a.ReportingDay(now))
	report, err = st.UsageOverview(ctx, now)
	require.NoError(t, err)
	require.EqualValues(t, 1, report.Days[28].Web)
	require.EqualValues(t, 1, report.Days[29].Web)
}

func TestUsageCountryCountsAndLatestObservation(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	a := store.UsageActivity{AnonymousID: "2a77e45e-1ac2-4d72-b9c2-aaf2a42f8234", Platform: "desktop", OccurredAt: now, Version: "0.1.3", Locale: "en-US", Country: "US"}
	require.NoError(t, st.RecordUsage(ctx, a, now))
	a.Country = "JP"
	a.OccurredAt = now.Add(time.Minute)
	require.NoError(t, st.RecordUsage(ctx, a, a.OccurredAt))
	a.Country = "US"
	a.OccurredAt = now
	require.NoError(t, st.RecordUsage(ctx, a, now.Add(time.Minute))) // Late retry cannot undo country.
	a.Platform = "web"
	a.Country = ""
	require.NoError(t, st.RecordUsage(ctx, a, now))
	report, err := st.UsageOverview(ctx, now)
	require.NoError(t, err)
	require.Equal(t, []store.UsageCountry{{Country: "JP", Desktop: 1}, {Country: "ZZ", Web: 1}}, report.Countries)
}
