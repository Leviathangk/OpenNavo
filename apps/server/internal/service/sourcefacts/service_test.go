package sourcefacts

import (
	"encoding/json"
	"testing"
	"time"

	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestGitHubSignalsUseDailyScheduleWithoutChangingLicenseSchedule(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	licenseNext := now.Add(29 * 24 * time.Hour)
	signalNext := now.Add(-time.Minute)
	row := store.SourceFact{Status: "found", CheckedAt: now.Add(-24 * time.Hour), NextCheckAt: licenseNext, SignalsNextCheckAt: &signalNext}
	require.True(t, githubSignalsDue(row, now))
	require.True(t, row.NextCheckAt.After(now), "the 30-day license check remains scheduled later")

	signalNext = now.Add(time.Minute)
	row.SignalsNextCheckAt = &signalNext
	require.False(t, githubSignalsDue(row, now))
	row.SignalsNextCheckAt = nil
	require.True(t, githubSignalsDue(row, now), "old rows without a signal schedule are backfilled")
}

func TestMergeGitHubSignalsPreservesExistingLicenseFacts(t *testing.T) {
	oldPushed := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	newPushed := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	data, err := json.Marshal(sources.OpenSource{Repo: "owner/app", URL: "https://github.com/owner/app", License: "MIT", Archived: true, Stars: int64PointerForTest(12), PushedAt: &oldPushed})
	require.NoError(t, err)
	stars := int64(34)
	updated, err := mergeGitHubSignals(data, sources.Repository{StargazersCount: &stars, PushedAt: &newPushed})
	require.NoError(t, err)
	var open sources.OpenSource
	require.NoError(t, json.Unmarshal(updated, &open))
	require.Equal(t, "MIT", open.License)
	require.True(t, open.Archived)
	require.Equal(t, int64(34), *open.Stars)
	require.Equal(t, newPushed, *open.PushedAt)
}

func int64PointerForTest(value int64) *int64 { return &value }
