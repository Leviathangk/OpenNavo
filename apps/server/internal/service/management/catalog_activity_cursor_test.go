package management

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogActivityCursorParameters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		params  map[string]any
		current int
		after   int64
		cursor  bool
		reason  string
	}{
		{name: "time window", params: map[string]any{"from": "2026-10-25T00:00:00Z"}, current: 3},
		{name: "null cursor", params: map[string]any{"afterseq": nil}, current: 1},
		{name: "start", params: map[string]any{"afterseq": float64(0)}, current: 1, cursor: true},
		{name: "cursor", params: map[string]any{"afterseq": float64(1201), "type": "updated", "haseditorial": true, "sort": "changed"}, current: 1, after: 1201, cursor: true},
		{name: "empty dates", params: map[string]any{"afterseq": float64(5), "from": "", "to": ""}, current: 1, after: 5, cursor: true},
		{name: "negative", params: map[string]any{"afterseq": float64(-1)}, current: 1, reason: "parameter_invalid:afterSeq"},
		{name: "fraction", params: map[string]any{"afterseq": 1.5}, current: 1, reason: "parameter_invalid:afterSeq"},
		{name: "text", params: map[string]any{"afterseq": "12"}, current: 1, reason: "parameter_invalid:afterSeq"},
		{name: "with from", params: map[string]any{"afterseq": float64(1), "from": "2026-10-25T00:00:00Z"}, current: 1, reason: "parameter_conflict:from"},
		{name: "with to", params: map[string]any{"afterseq": float64(1), "to": "2026-10-25T00:00:00Z"}, current: 1, reason: "parameter_conflict:to"},
		{name: "popular", params: map[string]any{"afterseq": float64(1), "sort": "popular"}, current: 1, reason: "parameter_conflict:sort"},
		{name: "page two", params: map[string]any{"afterseq": float64(1)}, current: 2, reason: "parameter_conflict:current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after, cursor, err := catalogActivityCursor(tc.params, tc.current)
			if tc.reason != "" {
				require.EqualError(t, err, "1001: rejected: "+tc.reason)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.cursor, cursor)
			require.Equal(t, tc.after, after)
		})
	}
}
