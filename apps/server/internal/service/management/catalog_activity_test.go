package management

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCatalogActivityRange(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		params map[string]any
		reason string
	}{
		{"omitted", nil, ""},
		{"empty", map[string]any{"from": "", "to": ""}, ""},
		{"null", map[string]any{"from": nil, "to": nil}, ""},
		{"valid", map[string]any{"from": "2026-10-25T00:00:00Z", "to": "2026-10-25T01:00:00Z"}, ""},
		{"offset", map[string]any{"from": "2026-10-25T08:00:00+08:00"}, ""},
		{"invalid", map[string]any{"from": "private-value"}, "date_time_invalid:from"},
		{"invalid end", map[string]any{"to": "private-value"}, "date_time_invalid:to"},
		{"equal", map[string]any{"from": now.Format(time.RFC3339)}, "date_range_order:from"},
		{"retention", map[string]any{"from": now.Add(-14*time.Hour*24 - time.Second).Format(time.RFC3339)}, "date_before_retention:from"},
		{"retention boundary", map[string]any{"from": now.Add(-14 * time.Hour * 24).Format(time.RFC3339)}, ""},
		{"future", map[string]any{"to": now.Add(time.Minute + time.Second).Format(time.RFC3339)}, "date_in_future:to"},
		{"clock tolerance", map[string]any{"to": now.Add(time.Minute).Format(time.RFC3339)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, to, err := catalogActivityRange(tc.params, now)
			if tc.reason != "" {
				require.ErrorContains(t, err, "rejected: "+tc.reason)
				require.NotContains(t, err.Error(), "private-value")
				return
			}
			require.NoError(t, err)
			if tc.name == "omitted" || tc.name == "empty" || tc.name == "null" {
				require.Equal(t, now.Add(-24*time.Hour), from)
				require.Equal(t, now, to)
			}
		})
	}
}
