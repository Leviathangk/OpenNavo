package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageValidation(t *testing.T) {
	now := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	valid := UsageActivity{AnonymousID: "2a77e45e-1ac2-4d72-b9c2-aaf2a42f8234", Platform: "desktop", OccurredAt: now, Version: "0.1.3", Locale: "zh-CN"}
	require.NoError(t, valid.Validate(now))
	for _, change := range []func(*UsageActivity){
		func(a *UsageActivity) { a.AnonymousID = "machine-serial" },
		func(a *UsageActivity) { a.AnonymousID = "00000000-0000-0000-0000-000000000000" },
		func(a *UsageActivity) { a.Platform = "bot" },
		func(a *UsageActivity) { a.Version = "" },
		func(a *UsageActivity) { a.Version = "/Users/someone/private" },
		func(a *UsageActivity) { a.Locale = "<script>" },
		func(a *UsageActivity) { a.OccurredAt = now.Add(-7*24*time.Hour - time.Second) },
		func(a *UsageActivity) { a.OccurredAt = now.Add(6 * time.Minute) },
	} {
		a := valid
		change(&a)
		require.Error(t, a.Validate(now))
	}
	require.Equal(t, "2026-10-23", now.In(usageZone).Format("2006-01-02"))
}

func TestUsageAcknowledgedDayAroundMidnight(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 58, 0, 0, time.UTC)
	a := UsageActivity{OccurredAt: now.Add(3 * time.Minute)}
	require.Equal(t, "2026-10-22", a.ReportingDay(now))
	require.Equal(t, "2026-10-23", a.ReportingDay(now.Add(2*time.Minute)))
	require.Equal(t, "2026-10-23", a.ReportingDay(now.Add(24*time.Hour))) // Offline replay keeps original day.
}
