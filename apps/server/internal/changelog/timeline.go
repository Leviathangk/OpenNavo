package changelog

import (
	"sort"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
)

func VersionLess(left, right string) bool {
	order, ok := homebrew.CompareVersion(left, right)
	if ok && order != 0 {
		return order > 0
	}
	return strings.Compare(left, right) > 0
}

type TimelineStats struct {
	Count30d               int
	Cadence                string
	MedianMinutes          *int
	EarlierCount, Compared int
}

func Stats(published, recorded []*time.Time, now time.Time) TimelineStats {
	out := TimelineStats{Cadence: "irregular"}
	dates := []time.Time{}
	lags := []float64{}
	for i, date := range published {
		effective := date
		if effective == nil && i < len(recorded) {
			effective = recorded[i]
		}
		if effective != nil && !effective.Before(now.Add(-30*24*time.Hour)) && !effective.After(now) {
			out.Count30d++
		}
		if effective != nil && !effective.Before(now.AddDate(-1, 0, 0)) && !effective.After(now) {
			dates = append(dates, *effective)
		}
		if date != nil && i < len(recorded) && recorded[i] != nil {
			minutes := recorded[i].Sub(*date).Minutes()
			lags = append(lags, minutes)
			if minutes < 0 {
				out.EarlierCount++
			}
		}
	}
	out.Compared = len(lags)
	if len(lags) > 0 {
		median := int(median(lags))
		out.MedianMinutes = &median
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	gaps := []float64{}
	for i := 1; i < len(dates); i++ {
		gap := dates[i].Sub(dates[i-1]).Hours() / 24
		if gap > 0 {
			gaps = append(gaps, gap)
		}
	}
	if len(gaps) > 0 {
		value := median(gaps)
		if out.Count30d == 0 && now.Sub(dates[len(dates)-1]).Hours()/24 > 2*value {
			return out
		}
		switch {
		case value <= 2:
			out.Cadence = "daily"
		case value <= 9:
			out.Cadence = "weekly"
		case value <= 20:
			out.Cadence = "biweekly"
		case value <= 45:
			out.Cadence = "monthly"
		}
	}
	return out
}
func median(values []float64) float64 {
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 0 {
		return (values[mid-1] + values[mid]) / 2
	}
	return values[mid]
}
