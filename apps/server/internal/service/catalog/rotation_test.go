package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRotateDailyKeepsWindowOffsetWhenOutputIsLonger(t *testing.T) {
	items := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	day := int64(20763) // 2026-11-06 UTC
	require.Equal(t, []int{2, 3, 4, 5}, rotateDaily(items, 4, 4, day))
	require.Equal(t, []int{2, 3, 4, 5, 6, 7, 8}, rotateDaily(items, 4, 7, day))
}
