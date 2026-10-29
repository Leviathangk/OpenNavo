package sourcefacts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

type GitHubSourceFunc func(context.Context, string, string) (sources.RepoResult, error)

func (f GitHubSourceFunc) Repo(ctx context.Context, repo, etag string) (sources.RepoResult, error) {
	return f(ctx, repo, etag)
}

func TestGitHubSignalsUseDailyScheduleWithoutChangingLicenseSchedule(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	licenseNext := now.Add(29 * 24 * time.Hour)
	signalNext := now.Add(-time.Minute)
	row := store.SourceFact{Source: GitHub, Subject: "owner/app", Status: "found", FailCount: 2, CheckedAt: now.Add(-24 * time.Hour), NextCheckAt: licenseNext, SignalsNextCheckAt: &signalNext}
	require.True(t, githubSignalsDue(row, now))
	require.True(t, row.NextCheckAt.After(now), "the 30-day license check remains scheduled later")

	signalNext = now.Add(time.Minute)
	row.SignalsNextCheckAt = &signalNext
	require.False(t, githubSignalsDue(row, now))
	row.SignalsNextCheckAt = nil
	require.True(t, githubSignalsDue(row, now), "old rows without a signal schedule are backfilled")
}

func TestGitHubRequestsStopWhenRateLimitHeadersAreMissing(t *testing.T) {
	calls := 0
	language := "Go"
	svc := &Service{
		GitHub: GitHubSourceFunc(func(_ context.Context, _, _ string) (sources.RepoResult, error) {
			calls++
			return sources.RepoResult{Repository: sources.Repository{FullName: "owner/app", HTMLURL: "https://github.com/owner/app", Language: &language}, Requests: 1, Remaining: -1, Limit: -1}, nil
		}),
		Now: func() time.Time { return time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC) },
	}
	stats := &Stats{GitHubRemaining: -1}
	item := pending{pkg: store.SourceFactPackage{ID: 1}, input: "owner/app", items: []string{"owner/app"}}
	for {
		_, low, err := svc.checkGitHub(context.Background(), item, stats)
		if errors.Is(err, errGitHubRequestCap) {
			break
		}
		require.NoError(t, err)
		require.False(t, low)
		require.LessOrEqual(t, stats.GitHubRequests, maxGitHubRequestsWithoutQuota)
	}
	require.Equal(t, maxGitHubRequestsWithoutQuota-1, calls)
	require.Equal(t, maxGitHubRequestsWithoutQuota-1, stats.GitHubRequests)
}

func TestGitHubSignalRefreshTreatsPrivateRepositoryAsUnavailable(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	row := store.SourceFact{PackageID: 1, Source: GitHub, Input: "owner/app", Subject: "owner/app", Status: "found", Data: json.RawMessage(`{"repo":"owner/app","url":"https://github.com/owner/app","license":"MIT"}`)}
	svc := &Service{
		GitHub: GitHubSourceFunc(func(context.Context, string, string) (sources.RepoResult, error) {
			return sources.RepoResult{Repository: sources.Repository{FullName: "owner/app", HTMLURL: "https://github.com/owner/app", Private: true}, Requests: 1, Remaining: 4000, Limit: 5000}, nil
		}),
		Now: func() time.Time { return now },
	}
	_, _, err := svc.checkGitHub(context.Background(), pending{pkg: store.SourceFactPackage{ID: 1}, input: row.Input, items: []string{row.Subject}, row: &row, signalsOnly: true}, &Stats{GitHubRemaining: -1})
	require.ErrorIs(t, err, errGitHubRepositoryUnavailable)
}

func TestMergeGitHubSignalsPreservesExistingLicenseFacts(t *testing.T) {
	oldPushed := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	newPushed := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	data, err := json.Marshal(sources.GitHubFact{Repo: "owner/app", URL: "https://github.com/owner/app", License: "MIT", Archived: true, Stars: int64PointerForTest(12), PushedAt: &oldPushed})
	require.NoError(t, err)
	stars := int64(34)
	updated, err := mergeGitHubSignals(data, sources.Repository{FullName: "owner/app", HTMLURL: "https://github.com/owner/app", StargazersCount: &stars, PushedAt: &newPushed})
	require.NoError(t, err)
	var fact sources.GitHubFact
	require.NoError(t, json.Unmarshal(updated, &fact))
	require.Equal(t, "MIT", fact.License)
	require.False(t, fact.Archived, "archived is a daily repository signal")
	require.Equal(t, int64(34), *fact.Stars)
	require.Equal(t, newPushed, *fact.PushedAt)
}

func TestMergeGitHubSignalsDoesNotCarryLicenseAcrossRepositories(t *testing.T) {
	data := json.RawMessage(`{"repo":"owner/old","url":"https://github.com/owner/old","license":"MIT"}`)
	updated, err := mergeGitHubSignals(data, sources.Repository{FullName: "owner/new", HTMLURL: "https://github.com/owner/new"})
	require.NoError(t, err)
	var fact sources.GitHubFact
	require.NoError(t, json.Unmarshal(updated, &fact))
	require.Equal(t, "owner/new", fact.Repo)
	require.Empty(t, fact.License, "a signal merge cannot reuse another repository's license")
}

func int64PointerForTest(value int64) *int64 { return &value }
