package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistoryHashMatchesLegacyRelease(t *testing.T) {
	legacy := HistoryState{"releases": {{"id": float64(1), "hidden": false}}}
	current := HistoryState{"releases": {{"id": float64(1), "hidden": false, "package_version_id": nil}}}
	hash := HistoryHash(current)
	require.True(t, HistoryHashMatches(current, HistoryHash(legacy)))
	require.Equal(t, hash, HistoryHash(current), "comparison must not mutate snapshots")
	require.True(t, HistoryHashMatches(current, hash))
	current["releases"][0]["hidden"] = true
	require.False(t, HistoryHashMatches(current, HistoryHash(legacy)))
	current["releases"][0]["hidden"] = false
	current["releases"][0]["package_version_id"] = float64(9)
	require.False(t, HistoryHashMatches(current, HistoryHash(legacy)))
	require.False(t, HistoryHashMatches(current, hash))
	require.True(t, HistoryHashMatches(current, HistoryHash(current)))
	require.True(t, HistoryHashMatches(nil, HistoryHash(nil)))
	require.False(t, HistoryHashMatches(nil, hash))
	require.False(t, HistoryHashMatches(current, HistoryHash(nil)))
}
