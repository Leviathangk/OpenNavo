package changelog

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimelineVersionOrderingAndStatistics(t *testing.T) {
	versions := []string{"1.9.0", "1.10.0-rc1", "1.10.0", "1.10.1", "latest"}
	sort.Slice(versions, func(i, j int) bool { return VersionLess(versions[i], versions[j]) })
	require.Equal(t, []string{"latest", "1.10.1", "1.10.0", "1.10.0-rc1", "1.9.0"}, versions)
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	stamp := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	stats := Stats([]*time.Time{stamp(-24 * time.Hour), stamp(-8 * 24 * time.Hour), nil, stamp(-31 * 24 * time.Hour), stamp(time.Hour)}, []*time.Time{stamp(-24*time.Hour - 10*time.Minute), stamp(-8*24*time.Hour + 20*time.Minute), stamp(-15 * 24 * time.Hour)}, now)
	require.Equal(t, 3, stats.Count30d)
	require.Equal(t, 2, stats.Compared)
	require.Equal(t, 1, stats.EarlierCount)
	require.Equal(t, 5, *stats.MedianMinutes)
	require.Equal(t, "weekly", stats.Cadence)
	empty := Stats(nil, nil, now)
	require.Equal(t, "irregular", empty.Cadence)
	require.Nil(t, empty.MedianMinutes)
	for _, tc := range []struct {
		days int
		want string
	}{{1, "daily"}, {9, "weekly"}, {20, "biweekly"}, {45, "monthly"}, {46, "irregular"}} {
		a, b := now, now.Add(-time.Duration(tc.days)*24*time.Hour)
		require.Equal(t, tc.want, Stats([]*time.Time{&a, &b, &b}, nil, now).Cadence)
	}
	a, b, c := stamp(-10*time.Hour), stamp(-20*time.Hour), stamp(-30*time.Hour)
	require.Equal(t, -5, *Stats([]*time.Time{a, b, c}, []*time.Time{stamp(-10*time.Hour - 5*time.Minute), stamp(-20*time.Hour - 7*time.Minute), stamp(-30 * time.Hour)}, now).MedianMinutes)
}

func TestCadenceUsesRecentReleasesAndDetectsStaleHistory(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		days []int
		want string
	}{
		{"stale weekly", []int{40, 47, 54, 61}, "irregular"},
		{"old weekly excluded", []int{0, 40, 400, 407, 414, 421, 428}, "monthly"},
		{"monthly still current", []int{35, 70, 105}, "monthly"},
		{"monthly stale", []int{80, 115, 150}, "irregular"},
		{"no recent history", []int{400, 407}, "irregular"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dates := []*time.Time{}
			for _, days := range tc.days {
				d := now.AddDate(0, 0, -days)
				dates = append(dates, &d)
			}
			require.Equal(t, tc.want, Stats(dates, nil, now).Cadence)
		})
	}
}
