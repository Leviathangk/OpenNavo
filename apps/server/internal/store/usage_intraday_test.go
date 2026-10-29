package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageIntradayBeijingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		at   string
		date string
		hour int
	}{
		{"2026-10-28T15:59:59Z", "2026-10-28", 23},
		{"2026-10-28T16:00:00Z", "2026-10-29", 0},
		{"2026-10-28T16:59:59Z", "2026-10-29", 0},
		{"2026-10-28T17:00:00Z", "2026-10-29", 1},
	} {
		t.Run(tc.at, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.at)
			require.NoError(t, err)
			report := usageIntraday(now, []usageHourCount{{Hour: 0, Web: 2, Desktop: 3}})
			require.Equal(t, tc.date, report.Date)
			require.Equal(t, "Asia/Shanghai", report.TimeZone)
			require.Equal(t, now.UTC(), report.AsOf)
			require.Len(t, report.Hours, 24)
			for hour, entry := range report.Hours {
				require.Equal(t, hour, entry.Hour)
				if hour > tc.hour {
					require.Nil(t, entry.Web)
					require.Nil(t, entry.Desktop)
					require.Nil(t, entry.CumulativeWeb)
					require.Nil(t, entry.CumulativeDesktop)
					continue
				}
				require.EqualValues(t, 2, *entry.CumulativeWeb)
				require.EqualValues(t, 3, *entry.CumulativeDesktop)
				if hour > 0 {
					require.Zero(t, *entry.Web)
					require.Zero(t, *entry.Desktop)
				}
			}
		})
	}
}
