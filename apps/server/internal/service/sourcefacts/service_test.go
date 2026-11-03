package sourcefacts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	reserved := maxGitHubRequestsWithoutQuota - maxGitHubRepoRequests + 1
	require.Equal(t, reserved, calls, "stop before a repository lookup could cross the cap")
	require.Equal(t, reserved, stats.GitHubRequests)
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

func TestGitHubSignalEscalationMatchesFullLicenseCheck(t *testing.T) {
	for index, tc := range []struct {
		name                                       string
		firstLicense, redirectLicense, lastLicense string
		status, subject, selected, license         string
		refreshCalls, fullCalls                    []string
	}{
		{
			name: "stored_subject_after_first_candidate", firstLicense: "Apache-2.0", redirectLicense: "MIT", lastLicense: "GPL-3.0",
			status: "found", subject: "acme/a", selected: "acme/a", license: "Apache-2.0",
			refreshCalls: []string{"acme/b", "acme/a"}, fullCalls: []string{"acme/a"},
		},
		{
			name: "reuse_redirect_after_earlier_non_osi_candidate", firstLicense: "BUSL-1.1", redirectLicense: "MIT", lastLicense: "Apache-2.0",
			status: "found", subject: "acme/renamed", selected: "acme/b", license: "MIT",
			refreshCalls: []string{"acme/b", "acme/a"}, fullCalls: []string{"acme/a", "acme/b"},
		},
		{
			name: "non_osi_redirect_before_later_osi_candidate", firstLicense: "BUSL-1.1", redirectLicense: "NOASSERTION", lastLicense: "Apache-2.0",
			status: "found", subject: "acme/c", selected: "acme/c", license: "Apache-2.0",
			refreshCalls: []string{"acme/b", "acme/a", "acme/c"}, fullCalls: []string{"acme/a", "acme/b", "acme/c"},
		},
		{
			name: "all_non_osi_candidates_keep_first_public_subject", firstLicense: "BUSL-1.1", redirectLicense: "NOASSERTION",
			status: "none", subject: "acme/a", selected: "acme/a",
			refreshCalls: []string{"acme/b", "acme/a", "acme/c"}, fullCalls: []string{"acme/a", "acme/b", "acme/c"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
			homepage, download, recorded := "https://github.com/acme/a", "https://github.com/acme/b/releases", "https://github.com/acme/c"
			pkg := store.SourceFactPackage{ID: 1, Homepage: &homepage, DownloadURL: &download, RepoURL: &recorded}
			items := candidates(GitHub, pkg)
			require.Equal(t, []string{"acme/a", "acme/b", "acme/c"}, items)
			base := int64(1000 + index*10)
			fixtures := map[string]sources.RepoResult{
				"acme/a": repoResultForTest("acme/a", tc.firstLicense, base+1, now.Add(-time.Duration(base+1)*time.Minute)),
				"acme/b": repoResultForTest("acme/renamed", tc.redirectLicense, base+2, now.Add(-time.Duration(base+2)*time.Minute)),
				"acme/c": repoResultForTest("acme/c", tc.lastLicense, base+3, now.Add(-time.Duration(base+3)*time.Minute)),
			}
			redirect := fixtures["acme/b"]
			redirect.Requests = 2
			fixtures["acme/b"] = redirect
			data, err := json.Marshal(sources.GitHubFact{Repo: "acme/b", URL: "https://github.com/acme/b", License: "MIT", Stars: int64PointerForTest(base + 4), PushedAt: timePointer(now.Add(-time.Duration(base+4) * time.Minute))})
			require.NoError(t, err)
			etag := `"before-redirect"`
			row := store.SourceFact{PackageID: pkg.ID, Source: GitHub, Input: strings.Join(items, " "), Status: "found", Subject: "acme/b", Data: data, ETag: &etag,
				CheckedAt: now.Add(-24 * time.Hour), NextCheckAt: now.Add(29 * 24 * time.Hour), FailCount: 2, SignalsFailCount: 1, SignalsUnavailableCount: 1}
			var calls []string
			svc := &Service{
				GitHub: GitHubSourceFunc(func(_ context.Context, repo, conditional string) (sources.RepoResult, error) {
					calls = append(calls, repo)
					require.Contains(t, fixtures, repo)
					if repo == row.Subject {
						require.Equal(t, etag, conditional)
					} else {
						require.Empty(t, conditional)
					}
					return fixtures[repo], nil
				}),
				Now: func() time.Time { return now },
			}
			stats := &Stats{GitHubRemaining: -1}
			refreshed, low, err := svc.checkGitHub(context.Background(), pending{pkg: pkg, input: row.Input, items: []string{row.Subject}, row: &row, signalsOnly: true}, stats)
			require.NoError(t, err)
			require.False(t, low)
			require.Equal(t, tc.refreshCalls, calls, "reuse the redirect response without another request")
			require.Equal(t, len(calls)+1, stats.GitHubRequests, "count the redirect's two HTTP requests only once")

			calls = nil
			full, low, err := svc.checkGitHub(context.Background(), pending{pkg: pkg, input: row.Input, items: items, row: &row}, &Stats{GitHubRemaining: -1})
			require.NoError(t, err)
			require.False(t, low)
			require.Equal(t, tc.fullCalls, calls)
			require.Equal(t, full, refreshed, "escalation must match the complete 30-day license check, including its fallback subject and signals")
			require.Equal(t, tc.status, refreshed.Status)
			require.Equal(t, tc.subject, refreshed.Subject)
			var published sources.GitHubFact
			require.NoError(t, json.Unmarshal(refreshed.Data, &published))
			require.Equal(t, tc.license, published.License)
			require.Equal(t, fixtures[tc.selected].Repository.StargazersCount, published.Stars)
			require.Equal(t, fixtures[tc.selected].Repository.PushedAt, published.PushedAt)
		})
	}
}

func TestGitHubSignalEscalationRespectsQuotaBoundary(t *testing.T) {
	for index, tc := range []struct {
		name      string
		remaining int64
		escalate  bool
	}{
		{name: "below_ten_percent", remaining: 499},
		{name: "exactly_ten_percent", remaining: 500},
		{name: "just_above_ten_percent", remaining: 501, escalate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
			homepage, download := "https://github.com/acme/a", "https://github.com/acme/b/releases"
			pkg := store.SourceFactPackage{ID: 1, Homepage: &homepage, DownloadURL: &download}
			base := int64(2000 + index*10)
			first := repoResultForTest("acme/a", "Apache-2.0", base+1, now.Add(-time.Duration(base+1)*time.Minute))
			first.Remaining = tc.remaining - 1
			redirect := repoResultForTest("acme/renamed", "BUSL-1.1", base+2, now.Add(-time.Duration(base+2)*time.Minute))
			redirect.Requests, redirect.Remaining = 2, tc.remaining
			data, err := json.Marshal(sources.GitHubFact{Repo: "acme/b", URL: "https://github.com/acme/b", License: "MIT", Stars: int64PointerForTest(base + 3), PushedAt: timePointer(now.Add(-time.Duration(base+3) * time.Minute))})
			require.NoError(t, err)
			row := store.SourceFact{PackageID: pkg.ID, Source: GitHub, Input: "acme/a acme/b", Status: "found", Subject: "acme/b", Data: data,
				CheckedAt: now.Add(-24 * time.Hour), NextCheckAt: now.Add(29 * 24 * time.Hour)}
			var calls []string
			svc := &Service{
				GitHub: GitHubSourceFunc(func(_ context.Context, repo, _ string) (sources.RepoResult, error) {
					calls = append(calls, repo)
					switch repo {
					case "acme/b":
						return redirect, nil
					case "acme/a":
						return first, nil
					default:
						t.Fatalf("unexpected repository: %s", repo)
						return sources.RepoResult{}, nil
					}
				}),
				Now: func() time.Time { return now },
			}
			stats := &Stats{GitHubRemaining: -1}
			fact, low, err := svc.checkGitHub(context.Background(), pending{pkg: pkg, input: row.Input, items: []string{row.Subject}, row: &row, signalsOnly: true}, stats)
			require.True(t, low, "the remaining quota is at most ten percent after the final request")
			if !tc.escalate {
				require.ErrorIs(t, err, errGitHubSignalRefreshFailed)
				require.Equal(t, []string{"acme/b"}, calls)
				require.Equal(t, 2, stats.GitHubRequests)
				require.Equal(t, tc.remaining, stats.GitHubRemaining)
				require.Equal(t, row, fact, "defer the new license decision without changing the stored repository")
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{"acme/b", "acme/a"}, calls)
			require.Equal(t, 3, stats.GitHubRequests)
			require.Equal(t, tc.remaining-1, stats.GitHubRemaining)
			require.Equal(t, "found", fact.Status)
			require.Equal(t, "acme/a", fact.Subject)
		})
	}
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

func repoResultForTest(repo, license string, stars int64, pushed time.Time) sources.RepoResult {
	language := "Go"
	result := sources.RepoResult{Repository: sources.Repository{FullName: repo, HTMLURL: "https://github.com/" + repo, Language: &language, StargazersCount: &stars, PushedAt: &pushed},
		ETag: `"` + repo + `"`, Requests: 1, Remaining: 4000, Limit: 5000}
	if license != "" {
		result.Repository.License = &struct {
			SPDXID string `json:"spdx_id"`
		}{SPDXID: license}
	}
	return result
}

func TestGitHubRenameAddsCanonicalCandidateWithoutAnotherRequest(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	homepage, recorded := "https://github.com/old/name", "https://github.com/other/repo"
	pkg := store.SourceFactPackage{ID: 1, Homepage: &homepage, RepoURL: &recorded}
	items := candidates(GitHub, pkg)
	require.Equal(t, []string{"old/name", "other/repo"}, items)
	var calls []string
	svc := &Service{
		GitHub: GitHubSourceFunc(func(_ context.Context, repo, _ string) (sources.RepoResult, error) {
			calls = append(calls, repo)
			switch repo {
			case "old/name":
				return repoResultForTest("new/name", "NOASSERTION", 10, now), nil
			case "other/repo":
				return repoResultForTest("other/repo", "NOASSERTION", 20, now), nil
			default:
				t.Fatalf("canonical name must reuse the rename response, got request for %s", repo)
				return sources.RepoResult{}, nil
			}
		}),
		Now: func() time.Time { return now },
	}
	fact, low, err := svc.checkGitHub(context.Background(), pending{pkg: pkg, input: "old/name other/repo", items: items}, &Stats{GitHubRemaining: -1})
	require.NoError(t, err)
	require.False(t, low)
	require.Equal(t, []string{"old/name", "other/repo"}, calls)
	require.Equal(t, "new/name", fact.Subject, "later checks use the canonical repository")
}

func TestAddCanonicalCandidateDeduplicatesCaseInsensitively(t *testing.T) {
	items := addCanonicalCandidate([]string{"old/name", "other/repo"}, "NEW/Name")
	require.Equal(t, []string{"old/name", "other/repo", "NEW/Name"}, items)
	require.Equal(t, items, addCanonicalCandidate(items, "new/name"))
	require.Equal(t, items, addCanonicalCandidate(items, ""))
}

// A row that found the repository but published no license must not stay "none" forever behind a
// 304: the full check asks again without its ETag so the license file is read (#65, F-02).
func TestGitHubLicenseRetryIgnoresETagForUnlicensedRepository(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	stored := `"W/\"1\""`
	row := store.SourceFact{PackageID: 1, Source: GitHub, Input: "owner/app", Subject: "owner/app", Status: "none", ETag: &stored,
		Data: json.RawMessage(`{"repo":"owner/app","url":"https://github.com/owner/app"}`), CheckedAt: now.Add(-24 * time.Hour), NextCheckAt: now.Add(-time.Minute)}
	calls := 0
	svc := &Service{
		GitHub: GitHubSourceFunc(func(_ context.Context, repo, etag string) (sources.RepoResult, error) {
			calls++
			require.Empty(t, etag, "the full check must read the repository, not the cached 304")
			return repoResultForTest(repo, "MIT", 10, now), nil
		}),
		Now: func() time.Time { return now },
	}
	item := pending{pkg: store.SourceFactPackage{ID: 1, RepoURL: stringPointerForTest("https://github.com/owner/app")}, input: "owner/app", items: []string{"owner/app"}, row: &row}
	fact, _, err := svc.checkGitHub(context.Background(), item, &Stats{GitHubRemaining: -1})
	require.NoError(t, err)
	require.Equal(t, "found", fact.Status)
	require.Equal(t, 1, calls)
	var published sources.GitHubFact
	require.NoError(t, json.Unmarshal(fact.Data, &published))
	require.Equal(t, "MIT", published.License)

	// The daily signal refresh keeps its conditional request; it cannot publish a license.
	calls = 0
	signals := &Service{
		GitHub: GitHubSourceFunc(func(_ context.Context, _ string, etag string) (sources.RepoResult, error) {
			calls++
			require.Equal(t, stored, etag)
			return sources.RepoResult{NotModified: true, Requests: 1, Remaining: 4000, Limit: 5000}, nil
		}),
		Now: func() time.Time { return now },
	}
	due := now.Add(-time.Minute)
	row.SignalsNextCheckAt = &due
	refreshed, _, err := signals.checkGitHub(context.Background(), pending{pkg: item.pkg, input: item.input, items: item.items, row: &row, signalsOnly: true}, &Stats{GitHubRemaining: -1})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "none", refreshed.Status)
	require.Equal(t, stored, *refreshed.ETag)
}

// A license decision that is already published keeps its conditional request even though the URL
// check is due again, so a repository that has not changed costs no quota.
func TestGitHubLicensedRepositoryKeepsConditionalRequest(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	stored := `"W/\"2\""`
	row := store.SourceFact{PackageID: 1, Source: GitHub, Input: "owner/app", Subject: "owner/app", Status: "found", ETag: &stored,
		Data: json.RawMessage(`{"repo":"owner/app","url":"https://github.com/owner/app","license":"MIT"}`), CheckedAt: now.Add(-30 * 24 * time.Hour), NextCheckAt: now.Add(-time.Minute)}
	svc := &Service{
		GitHub: GitHubSourceFunc(func(_ context.Context, _ string, etag string) (sources.RepoResult, error) {
			require.Equal(t, stored, etag)
			return sources.RepoResult{NotModified: true, Requests: 1, Remaining: 4000, Limit: 5000}, nil
		}),
		Now: func() time.Time { return now },
	}
	item := pending{pkg: store.SourceFactPackage{ID: 1, RepoURL: stringPointerForTest("https://github.com/owner/app")}, input: "owner/app", items: []string{"owner/app"}, row: &row}
	fact, _, err := svc.checkGitHub(context.Background(), item, &Stats{GitHubRemaining: -1})
	require.NoError(t, err)
	require.Equal(t, "found", fact.Status)
	require.Equal(t, "MIT", factLicenseForTest(t, fact.Data))
}

func stringPointerForTest(value string) *string { return &value }

func factLicenseForTest(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var fact sources.GitHubFact
	require.NoError(t, json.Unmarshal(data, &fact))
	return fact.License
}

// The canonical name of a rename becomes a candidate, so a later check that no longer lists the old
// name still reaches the repository. The response cache alone would not do this (#78 F-11).
func TestGitHubRenameCandidateSurvivesAFailedFirstCandidate(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	homepage := "https://github.com/old/name"
	pkg := store.SourceFactPackage{ID: 1, Homepage: &homepage}
	var calls []string
	svc := &Service{
		GitHub: GitHubSourceFunc(func(_ context.Context, repo, _ string) (sources.RepoResult, error) {
			calls = append(calls, repo)
			switch repo {
			case "old/name":
				// The old name is gone; only a candidate derived from the stored subject or a
				// rename response can still find the repository.
				return sources.RepoResult{NotFound: true, Requests: 1, Remaining: 4000, Limit: 5000}, nil
			case "new/name":
				return repoResultForTest("new/name", "MIT", 10, now), nil
			}
			return sources.RepoResult{}, nil
		}),
		Now: func() time.Time { return now },
	}
	storedEtag := `"new/name"`
	row := store.SourceFact{PackageID: 1, Source: GitHub, Input: "old/name", Subject: "new/name", Status: "none", ETag: &storedEtag,
		Data: json.RawMessage(`{"repo":"new/name","url":"https://github.com/new/name"}`), CheckedAt: now.Add(-24 * time.Hour), NextCheckAt: now.Add(-time.Minute)}
	fact, _, err := svc.checkGitHub(context.Background(), pending{pkg: pkg, input: "old/name", items: []string{"old/name"}, row: &row}, &Stats{GitHubRemaining: -1})
	require.NoError(t, err)
	require.Equal(t, "found", fact.Status, "the stored canonical subject still reaches the repository")
	require.Equal(t, "new/name", fact.Subject)
	require.Equal(t, []string{"old/name", "new/name"}, calls)
}

// A transient /license failure still publishes the repository signals, but must not wait for the
// next 30-day full check to try the OSI decision again.
func TestGitHubLicenseLookupFailureRetriesWithinADay(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	homepage := "https://github.com/acme/app"
	pkg := store.SourceFactPackage{ID: 1, Homepage: &homepage}
	svc := &Service{
		GitHub: GitHubSourceFunc(func(context.Context, string, string) (sources.RepoResult, error) {
			result := repoResultForTest("acme/app", "NOASSERTION", 5, now.Add(-time.Hour))
			result.LicenseUnavailable = true
			return result, nil
		}),
		Now: func() time.Time { return now },
	}
	fact, low, err := svc.checkGitHub(context.Background(), pending{pkg: pkg, items: []string{"acme/app"}}, &Stats{GitHubRemaining: -1})
	require.NoError(t, err)
	require.False(t, low)
	require.Equal(t, "none", fact.Status)
	require.Equal(t, "acme/app", fact.Subject)
	require.WithinDuration(t, now.Add(6*time.Hour+15*time.Minute), fact.NextCheckAt, time.Second)
}
