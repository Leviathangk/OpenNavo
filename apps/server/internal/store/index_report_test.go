package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIndexReportGroupsAllFourDimensions(t *testing.T) {
	counts := reportCounts{}
	counts.add("versions", "en-US", "", true, true)
	counts.add("versions", "en-US", "", true, true)
	counts.add("versions", "en-US", "", true, false)
	counts.add("versions", "ja-JP", "thin_notes_page", false, false)
	counts.add("fonts", "en-US", "disabled", false, false)
	rows := counts.rows()
	require.Equal(t, []IndexReportCount{
		{"fonts", "en-US", "disabled", false, 1},
		{"versions", "en-US", "indexable", false, 1},
		{"versions", "en-US", "indexable", true, 2},
		{"versions", "ja-JP", "thin_notes_page", false, 1},
	}, rows)
}
