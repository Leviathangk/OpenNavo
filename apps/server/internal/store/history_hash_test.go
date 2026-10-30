package store

import (
	"fmt"
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

func TestHistoryHashMatchesMigration126Defaults(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		for _, boundColumn := range []bool{false, true} {
			t.Run(fmt.Sprintf("hidden=%t/release_column=%t", hidden, boundColumn), func(t *testing.T) {
				legacy := HistoryState{
					"package_meta": {{"package_id": float64(1), "hidden": hidden}},
					"releases":     {{"id": float64(2), "hidden": false}},
				}
				if boundColumn {
					legacy["releases"][0]["package_version_id"] = nil
				}
				reason := ""
				if hidden {
					reason = "Legacy setting; reason not recorded."
				}
				current := HistoryState{
					"package_meta": {{"package_id": float64(1), "hidden": hidden, "private": false, "visibility_reason": reason}},
					"releases":     {{"id": float64(2), "hidden": false, "package_version_id": nil}},
				}
				hash, expected := HistoryHash(current), HistoryHash(legacy)
				require.True(t, HistoryHashMatches(current, expected))
				require.Equal(t, hash, HistoryHash(current), "comparison must not mutate snapshots")
				current["package_meta"][0]["private"] = true
				require.False(t, HistoryHashMatches(current, expected), "a real visibility change must conflict")
				current["package_meta"][0]["private"] = false
				current["package_meta"][0]["visibility_reason"] = "A later recorded reason"
				require.False(t, HistoryHashMatches(current, expected), "a real reason edit must conflict")
			})
		}
	}
}
