//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestUsageTotalsCountCumulativeIdentitiesByPlatform(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
	report, err := st.UsageOverview(ctx, now)
	require.NoError(t, err)
	require.Zero(t, report.DesktopTotal)
	require.Zero(t, report.WebTotal)

	sharedID := uuid.NewString()
	for _, a := range []store.UsageActivity{
		// An identity outside the daily report's 30-day window still contributes to the total.
		{AnonymousID: sharedID, Platform: "desktop", OccurredAt: now.AddDate(0, 0, -40), Version: "0.1.3", Locale: "en-US"},
		{AnonymousID: sharedID, Platform: "web", OccurredAt: now, Locale: "en-US"},
		{AnonymousID: uuid.NewString(), Platform: "web", OccurredAt: now, Locale: "en-US"},
		{AnonymousID: uuid.NewString(), Platform: "desktop", OccurredAt: now.AddDate(0, 0, -1), Version: "0.1.4", Locale: "en-US"},
		{AnonymousID: uuid.NewString(), Platform: "desktop", OccurredAt: now, Version: "test-ci-bot", Locale: "en-US"},
	} {
		// Retries deduplicate at (platform, anonymous_id), including test/CI/bot installations.
		for range 2 {
			require.NoError(t, st.RecordUsage(ctx, a, a.OccurredAt))
		}
	}

	report, err = st.UsageOverview(ctx, now)
	require.NoError(t, err)
	var desktopTotal, webTotal int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM usage_identities WHERE platform='desktop'").Scan(&desktopTotal).Error)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM usage_identities WHERE platform='web'").Scan(&webTotal).Error)
	require.EqualValues(t, 3, desktopTotal)
	require.EqualValues(t, 2, webTotal)
	require.Equal(t, desktopTotal, report.DesktopTotal)
	require.Equal(t, webTotal, report.WebTotal)
	require.Equal(t, store.UsageDay{Date: "2026-12-01", Desktop: 1, Web: 2, NewDesktop: 1, NewWeb: 2}, report.Days[29])
}

func TestUsagePruneKeepsIdentitiesAndNinetyDayDailyBoundary(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	now := time.Date(2027, 2, 1, 16, 30, 0, 0, time.UTC) // Feb 2 in Shanghai
	before := now.AddDate(0, 0, -90)
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	oldID, boundaryID, activeID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, platform := range []string{"desktop", "web"} {
		for _, observation := range []struct {
			id string
			at time.Time
		}{
			{oldID, before.AddDate(0, 0, -30)},
			// Earlier than the timestamp cutoff but on the retained Shanghai date.
			{boundaryID, before.Add(-30 * time.Minute)},
			{activeID, before.AddDate(0, 0, -30)},
			{activeID, before.AddDate(0, 0, -1)},
			{activeID, now},
		} {
			a := store.UsageActivity{AnonymousID: observation.id, Platform: platform, OccurredAt: observation.at, Version: "0.1.3", Locale: "en-US"}
			require.NoError(t, st.RecordUsage(ctx, a, observation.at))
		}
	}
	type identity struct {
		Platform    string
		AnonymousID string
		FirstSeenAt time.Time
		LastSeenAt  time.Time
	}
	var identitiesBefore, identitiesAfter []identity
	const identitiesQuery = "SELECT * FROM usage_identities ORDER BY platform,anonymous_id"
	require.NoError(t, st.DB.Raw(identitiesQuery).Scan(&identitiesBefore).Error)
	require.Len(t, identitiesBefore, 6)
	var cascade bool
	require.NoError(t, st.DB.Raw(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='usage_daily'::regclass AND confrelid='usage_identities'::regclass AND contype='f' AND confdeltype='c')`).Scan(&cascade).Error)
	require.True(t, cascade, "exercise retention with the real ON DELETE CASCADE foreign key")

	pruned, err := st.PruneLogs(ctx, before)
	require.NoError(t, err, "pruning child daily rows must not cause foreign-key errors")
	require.EqualValues(t, 6, pruned["usage_daily"])
	require.NotContains(t, pruned, "usage_identities")
	require.NoError(t, st.DB.Raw(identitiesQuery).Scan(&identitiesAfter).Error)
	require.Equal(t, identitiesBefore, identitiesAfter, "keep every identifier and its first/last activity times")
	var dailyCount, expiredCount, boundaryCount int64
	require.NoError(t, st.DB.Table("usage_daily").Count(&dailyCount).Error)
	require.EqualValues(t, 4, dailyCount)
	require.NoError(t, st.DB.Table("usage_daily").Where("day<?::date", before.In(zone).Format("2006-01-02")).Count(&expiredCount).Error)
	require.Zero(t, expiredCount)
	require.NoError(t, st.DB.Table("usage_daily").Where("day=?::date", before.In(zone).Format("2006-01-02")).Count(&boundaryCount).Error)
	require.EqualValues(t, 2, boundaryCount)
	pruned, err = st.PruneLogs(ctx, before)
	require.NoError(t, err)
	require.Zero(t, pruned["usage_daily"], "cleanup is idempotent")

	// Returning after more than 90 days uses the existing identity, even after every old daily row was deleted.
	for _, platform := range []string{"desktop", "web"} {
		a := store.UsageActivity{AnonymousID: oldID, Platform: platform, OccurredAt: now, Version: "0.1.3", Locale: "en-US"}
		require.NoError(t, st.RecordUsage(ctx, a, now))
	}
	report, err := st.UsageOverview(ctx, now)
	require.NoError(t, err)
	require.EqualValues(t, 3, report.DesktopTotal)
	require.EqualValues(t, 3, report.WebTotal)
	require.Equal(t, store.UsageDay{Date: "2027-02-02", Desktop: 2, Web: 2}, report.Days[29])
}

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
	require.Equal(t, []store.UsageVersion{{Version: "0.1.4", Count: 1}}, report.Versions, "keep daily rows on the cutoff date even if their activity time is earlier")
	_, err = st.PruneLogs(ctx, now.AddDate(0, 0, 1))
	require.NoError(t, err)
	report, err = st.UsageOverview(ctx, now)
	require.NoError(t, err)
	require.Empty(t, report.Versions)
	require.EqualValues(t, 1, report.DesktopTotal)
	require.EqualValues(t, 1, report.WebTotal)
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
