package store

import (
	"context"
	"time"
)

type UsageHour struct {
	Hour              int    `json:"hour"`
	Web               *int64 `json:"web"`
	Desktop           *int64 `json:"desktop"`
	CumulativeWeb     *int64 `json:"cumulativeWeb"`
	CumulativeDesktop *int64 `json:"cumulativeDesktop"`
}

type UsageIntraday struct {
	Date     string      `json:"date"`
	TimeZone string      `json:"timeZone"`
	AsOf     time.Time   `json:"asOf"`
	Hours    []UsageHour `json:"hours"`
}

type usageHourCount struct {
	Hour         int
	Web, Desktop int64
}

// UsageIntraday uses exactly the daily rows counted by UsageOverview's active
// series. Each (day, platform, anonymous_id) contributes once, at its earliest
// observation; offline events may move it earlier, while later activity cannot.
func (s *Store) UsageIntraday(ctx context.Context, now time.Time) (UsageIntraday, error) {
	today := now.In(usageZone).Format("2006-01-02")
	var counts []usageHourCount
	err := s.DB.WithContext(ctx).Raw(`SELECT EXTRACT(HOUR FROM first_active_at AT TIME ZONE 'Asia/Shanghai')::int AS "hour",
 count(*) FILTER(WHERE platform='web') web,count(*) FILTER(WHERE platform='desktop') desktop
 FROM usage_daily WHERE day=?::date GROUP BY 1 ORDER BY 1`, today).Scan(&counts).Error
	return usageIntraday(now, counts), err
}

func usageIntraday(now time.Time, counts []usageHourCount) UsageIntraday {
	local := now.In(usageZone)
	result := UsageIntraday{Date: local.Format("2006-01-02"), TimeZone: "Asia/Shanghai", AsOf: now.UTC(), Hours: make([]UsageHour, 24)}
	byHour := [24]usageHourCount{}
	for _, count := range counts {
		byHour[count.Hour] = count
	}
	var web, desktop int64
	for hour := range result.Hours {
		entry := UsageHour{Hour: hour}
		if hour <= local.Hour() {
			count := byHour[hour]
			web += count.Web
			desktop += count.Desktop
			entry.Web, entry.Desktop = &count.Web, &count.Desktop
			// Each point owns its values; the running totals continue to change.
			entry.CumulativeWeb, entry.CumulativeDesktop = new(web), new(desktop)
		}
		result.Hours[hour] = entry
	}
	return result
}
