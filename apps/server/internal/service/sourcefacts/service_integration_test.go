//go:build integration

package sourcefacts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type githubStub struct {
	calls []string
	etags []string
	repo  func(repo, etag string) (sources.RepoResult, error)
}

func (g *githubStub) Repo(_ context.Context, repo, etag string) (sources.RepoResult, error) {
	g.calls, g.etags = append(g.calls, repo), append(g.etags, etag)
	return g.repo(repo, etag)
}

type storeStub struct {
	lookups [][]string
	lookup  func(ids []string) (map[string]sources.Listing, error)
}

func (a *storeStub) Lookup(_ context.Context, ids []string) (map[string]sources.Listing, error) {
	a.lookups = append(a.lookups, ids)
	return a.lookup(ids)
}

func addCask(t *testing.T, st *store.Store, raw string) {
	t.Helper()
	p, err := homebrew.Normalize("cask", json.RawMessage(raw))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(context.Background(), []store.CatalogMutation{{Package: p}}))
}

func mitRepo(name string) sources.RepoResult {
	language := "Swift"
	stars := int64(1234)
	pushed := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	repo := sources.Repository{FullName: name, HTMLURL: "https://github.com/" + name, Language: &language, StargazersCount: &stars, PushedAt: &pushed}
	repo.License = &struct {
		SPDXID string `json:"spdx_id"`
	}{SPDXID: "MIT"}
	return sources.RepoResult{Repository: repo, ETag: `W/"e1"`, Remaining: 4000, Limit: 5000}
}

func invalidations(t *testing.T, st *store.Store) []string {
	t.Helper()
	var keys []string
	require.NoError(t, st.DB.Raw(`SELECT unique_key FROM job_outbox WHERE job_type='cache:invalidate' ORDER BY unique_key`).Scan(&keys).Error)
	require.NoError(t, st.DB.Exec(`DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
	return keys
}

func TestSourceFactsJobFindsSchedulesAndPublishes(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"alpha","name":["Alpha"],"version":"1","homepage":"https://github.com/acme/alpha","url":"https://github.com/acme/alpha/releases/download/v1/Alpha.dmg",
		"artifacts":[{"app":["Alpha.app"]},{"uninstall":[{"quit":"com.acme.alpha"}]},{"zap":[{"trash":["~/Library/Preferences/com.acme.alpha.plist"]}]}]}`)
	addCask(t, st, `{"token":"beta","name":["Beta Pro"],"version":"1","homepage":"https://beta.example","url":"https://beta.example/Beta.dmg",
		"artifacts":[{"app":["Beta Pro.app"]},{"zap":[{"trash":["~/Library/Containers/com.beta.pro","~/Library/Caches/com.beta.pro"]}]}]}`)
	addCask(t, st, `{"token":"font-sample","version":"1","url":"https://github.com/acme/fonts/releases/download/v1/f.zip","artifacts":[{"font":["F.ttf"]}]}`)
	addCask(t, st, `{"token":"hidden","version":"1","homepage":"https://github.com/acme/hidden","url":"https://example.test/h.dmg","artifacts":[{"app":["H.app"]}]}`)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden'`).Error)

	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
		require.Equal(t, "acme/alpha", repo)
		if etag == `W/"e1"` {
			return sources.RepoResult{NotModified: true, Remaining: 4000, Limit: 5000}, nil
		}
		return mitRepo("acme/alpha"), nil
	}}
	appStore := &storeStub{lookup: func([]string) (map[string]sources.Listing, error) {
		return map[string]sources.Listing{
			"com.beta.pro":   {Name: "Beta Pro - Editor", URL: "https://apps.apple.com/us/app/beta/id1?mt=12", BundleID: "com.beta.pro", Price: 0, Currency: "USD", Region: "US", Languages: []string{"EN", "ZH"}},
			"com.acme.alpha": {Name: "Something Else", URL: "https://apps.apple.com/us/app/else/id2?mt=12", BundleID: "com.acme.alpha", Price: 1, Currency: "USD", Region: "US"},
		}, nil
	}}
	svc := &Service{Store: st, GitHub: github, AppStore: appStore, Now: func() time.Time { return now }}
	invalidations(t, st) // the catalog writes above queue their own

	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/alpha"}, github.calls)
	require.Equal(t, [][]string{{"com.acme.alpha", "com.beta.pro"}}, appStore.lookups)
	require.EqualValues(t, 2, stats["changed"])
	require.Equal(t, []string{"cache:invalidate:seo-content:pkg:1", "cache:invalidate:seo-content:pkg:2"}, invalidations(t, st))

	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	alpha := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	require.Equal(t, "found", alpha.Status)
	require.Equal(t, "acme/alpha", alpha.Subject)
	require.Equal(t, `W/"e1"`, *alpha.ETag)
	require.Equal(t, "none", rows[store.SourceFactKey{PackageID: 1, Source: AppStore}].Status)
	require.Equal(t, "found", rows[store.SourceFactKey{PackageID: 2, Source: AppStore}].Status)

	detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "alpha", "en-US")
	require.NoError(t, err)
	require.Equal(t, "MIT", detail.OpenSource.License)
	require.Equal(t, "https://github.com/acme/alpha", detail.OpenSource.Url)
	require.Equal(t, int64(1234), *detail.Repository.Stars)
	require.Equal(t, time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC), *detail.Repository.PushedAt)
	require.Equal(t, now, *detail.Repository.SignalsCheckedAt)
	require.NotContains(t, string(mustJSON(t, detail.OpenSource)), "stars")
	require.NotContains(t, string(mustJSON(t, detail.OpenSource)), "pushedAt")
	require.NotContains(t, string(mustJSON(t, detail.OpenSource)), "signalsCheckedAt")
	require.Nil(t, detail.AppStore)
	require.Equal(t, "active", string(detail.HomebrewStatus.State))
	detail, err = (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "beta", "zh-CN")
	require.NoError(t, err)
	require.Nil(t, detail.OpenSource)
	require.Nil(t, detail.Repository, "an app without a public GitHub repository has neither fact")
	require.Equal(t, "Beta Pro - Editor", detail.AppStore.Name)
	require.Equal(t, []string{"EN", "ZH"}, detail.AppStore.Languages)
	require.Zero(t, detail.AppStore.Price)
	var snapshots int64
	require.NoError(t, st.DB.Table("app_store_price_snapshots").Count(&snapshots).Error)
	require.EqualValues(t, 1, snapshots)
	require.Equal(t, now.Add(24*time.Hour), rows[store.SourceFactKey{PackageID: 2, Source: AppStore}].NextCheckAt.UTC())
	require.NotNil(t, detail.Facts)
	require.Empty(t, *detail.Facts.Offers)

	// Nothing is due until the schedule says so.
	stats, err = svc.Run(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, stats["due"].(map[string]any)["github"])
	require.Len(t, github.calls, 1)

	// A month later GitHub answers 304 for the known repository and nothing visible changes.
	now = now.Add(31 * 24 * time.Hour)
	stats, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"", `W/"e1"`}, github.etags)
	require.EqualValues(t, 0, stats["changed"])
	require.EqualValues(t, 1, stats["githubRequests"])
	require.EqualValues(t, 1, stats["githubNotModified"])
	require.EqualValues(t, 4000, stats["githubRemaining"])
	rows, err = st.SourceFacts(ctx)
	require.NoError(t, err)
	var githubFact sources.GitHubFact
	require.NoError(t, json.Unmarshal(rows[store.SourceFactKey{PackageID: 1, Source: GitHub}].Data, &githubFact))
	require.Equal(t, int64(1234), *githubFact.Stars)
	require.Equal(t, time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC), *githubFact.PushedAt)
	require.Empty(t, invalidations(t, st))

	// The definition no longer points at an App Store identifier: the row goes and so does the page fact.
	require.NoError(t, st.DB.Exec(`UPDATE packages SET artifacts='{"apps":["Beta Pro.app"],"binaries":[],"pkgs":[],"entries":[]}' WHERE token='beta'`).Error)
	stats, err = svc.Run(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats["removed"])
	require.Equal(t, []string{"cache:invalidate:seo-content:pkg:2"}, invalidations(t, st))
}

func TestSourceFactsSignalsRefreshOnlyFoundAppsDaily(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"open","name":["Open"],"version":"1","homepage":"https://github.com/acme/open","url":"https://example.test/open.dmg","artifacts":[{"app":["Open.app"]}]}`)
	addCask(t, st, `{"token":"closed","name":["Closed"],"version":"1","homepage":"https://github.com/acme/closed","url":"https://example.test/closed.dmg","artifacts":[{"app":["Closed.app"]}]}`)
	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	var calls []string
	github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
		calls = append(calls, repo+" "+etag)
		if repo == "acme/open" && etag != "" {
			stars := int64(2000)
			pushed := now.Add(24 * time.Hour)
			result := mitRepo(repo)
			result.Repository.StargazersCount = &stars
			result.Repository.PushedAt = &pushed
			return result, nil
		}
		if repo == "acme/closed" {
			language := "Swift"
			return sources.RepoResult{Repository: sources.Repository{FullName: repo, HTMLURL: "https://github.com/" + repo, Language: &language}, Remaining: 4000, Limit: 5000}, nil
		}
		return mitRepo(repo), nil
	}}
	svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/open ", "acme/closed "}, calls)
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	openBefore, closedBefore := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}], rows[store.SourceFactKey{PackageID: 2, Source: GitHub}]
	require.NoError(t, st.DB.Exec(`UPDATE package_source_facts SET fail_count=2 WHERE package_id=? AND source='github'`, openBefore.PackageID).Error)
	calls = nil
	now = now.Add(25 * time.Hour)
	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/open W/\"e1\"", "acme/closed "}, calls)
	require.EqualValues(t, 2, stats["githubRequests"])
	rows, err = st.SourceFacts(ctx)
	require.NoError(t, err)
	open := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	closed := rows[store.SourceFactKey{PackageID: 2, Source: GitHub}]
	require.Equal(t, int64(2000), openStars(t, open))
	require.WithinDuration(t, now, *open.SignalsCheckedAt, time.Second)
	require.Equal(t, openBefore.CheckedAt, open.CheckedAt)
	require.Equal(t, openBefore.NextCheckAt, open.NextCheckAt)
	require.Equal(t, 2, open.FailCount)
	require.WithinDuration(t, now, *closed.SignalsCheckedAt, time.Second)
	require.Equal(t, closedBefore.CheckedAt, closed.CheckedAt)
	require.Equal(t, closedBefore.NextCheckAt, closed.NextCheckAt)
	require.Equal(t, "acme/closed", closed.Subject)
	require.Equal(t, "none", closed.Status)
	detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "closed", "en-US")
	require.NoError(t, err)
	require.Nil(t, detail.OpenSource, "non-OSI repository must not publish openSource")
	require.Equal(t, "https://github.com/acme/closed", detail.Repository.Url)
	require.Nil(t, detail.Repository.Stars, "GitHub omitted stargazers_count in this response")
}

func TestSourceFactsSignalRedirectChecksAllLicenseCandidates(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"redirect","name":["Redirect"],"version":"1","homepage":"https://github.com/acme/a","url":"https://github.com/acme/c/releases/download/v1/Redirect.dmg","artifacts":[{"app":["Redirect.app"]}]}`)
	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	redirected := false
	github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
		if redirected && repo == "acme/a" {
			require.Equal(t, `W/"e1"`, etag)
			result := mitRepo("acme/b")
			result.Repository.License.SPDXID = "BUSL-1.1"
			result.Requests = 2
			return result, nil
		}
		if repo == "acme/c" {
			require.Empty(t, etag, "the previous repository's ETag must not be reused")
			result := mitRepo(repo)
			result.Repository.License.SPDXID = "Apache-2.0"
			result.ETag = `W/"c"`
			return result, nil
		}
		require.Equal(t, "acme/a", repo)
		return mitRepo(repo), nil
	}}
	svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/a"}, github.calls)
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	initial := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	require.Equal(t, "acme/a", initial.Subject)
	require.Equal(t, "found", initial.Status)

	redirected = true
	github.calls, github.etags = nil, nil
	now = now.Add(25 * time.Hour)
	require.True(t, initial.NextCheckAt.After(now), "only the daily signal refresh is due")
	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	rows, err = st.SourceFacts(ctx)
	require.NoError(t, err)
	refreshed := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	require.Equal(t, "found", refreshed.Status, "a non-OSI redirect must not hide another candidate's OSI license")
	require.Equal(t, "acme/c", refreshed.Subject)
	require.Equal(t, now, refreshed.CheckedAt.UTC())
	require.Equal(t, next(now, recheck, 1), refreshed.NextCheckAt.UTC())
	require.NotNil(t, refreshed.ETag)
	require.Equal(t, `W/"c"`, *refreshed.ETag)
	require.Equal(t, []string{"acme/a", "acme/c"}, github.calls)
	require.EqualValues(t, 3, stats["githubRequests"])
	detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "redirect", "en-US")
	require.NoError(t, err)
	require.NotNil(t, detail.OpenSource)
	require.Equal(t, "acme/c", detail.OpenSource.Repo)
	require.Equal(t, "Apache-2.0", detail.OpenSource.License)
	require.NotNil(t, detail.Repository)
	require.Equal(t, "https://github.com/acme/c", detail.Repository.Url)
}

func TestSourceFactsSignalEscalationQuotaCutoffPreservesStoredFact(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining int64
	}{
		{name: "exactly_ten_percent", remaining: 500},
		{name: "below_ten_percent", remaining: 499},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewFactsStore(t)
			addCask(t, st, `{"token":"quota","name":["Quota"],"version":"1","homepage":"https://github.com/acme/a","url":"https://github.com/acme/b/releases/download/v1/Quota.dmg","artifacts":[{"app":["Quota.app"]}]}`)
			require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,repo_url) SELECT id,'https://github.com/acme/c' FROM packages WHERE token='quota'`).Error)

			now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
			refresh := false
			github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
				if !refresh {
					result := mitRepo(repo)
					if repo == "acme/a" {
						result.Repository.License.SPDXID = "BUSL-1.1"
					}
					return result, nil
				}
				switch repo {
				case "acme/b":
					require.Equal(t, `W/"e1"`, etag)
					result := repoResultForTest("acme/renamed", "BUSL-1.1", 2000, now.Add(-time.Hour))
					result.Requests, result.Remaining = 2, 501
					return result, nil
				case "acme/a":
					require.Empty(t, etag)
					// This non-OSI response drops quota to the boundary. The cached
					// redirect is also non-OSI, so completion needs an uncached c.
					result := repoResultForTest(repo, "BUSL-1.1", 3000, now.Add(-2*time.Hour))
					result.Remaining = tc.remaining
					return result, nil
				case "acme/c":
					return repoResultForTest(repo, "Apache-2.0", 4000, now.Add(-3*time.Hour)), nil
				default:
					t.Fatalf("unexpected repository: %s", repo)
					return sources.RepoResult{}, nil
				}
			}}
			svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
			_, err := svc.Run(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"acme/a", "acme/b"}, github.calls)
			require.NoError(t, st.DB.Exec(`UPDATE package_source_facts SET fail_count=2,signals_unavailable_count=2 WHERE source='github'`).Error)
			rows, err := st.SourceFacts(ctx)
			require.NoError(t, err)
			key := store.SourceFactKey{PackageID: 1, Source: GitHub}
			initial := rows[key]
			require.Equal(t, "acme/a acme/b acme/c", initial.Input)
			require.Equal(t, "acme/b", initial.Subject)
			detailBefore, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "quota", "en-US")
			require.NoError(t, err)
			require.NotNil(t, detailBefore.OpenSource)
			require.Equal(t, "MIT", detailBefore.OpenSource.License)
			invalidations(t, st)

			refresh = true
			now = now.Add(25 * time.Hour)
			for attempt, backoff := range []time.Duration{time.Hour, 2 * time.Hour} {
				require.True(t, initial.NextCheckAt.After(now), "only the daily signal refresh is due")
				github.calls, github.etags = nil, nil
				stats, err := svc.Run(ctx)
				require.NoError(t, err)
				rows, err = st.SourceFacts(ctx)
				require.NoError(t, err)
				refreshed := rows[key]
				require.Equal(t, attempt+1, refreshed.SignalsFailCount)
				require.NotNil(t, refreshed.SignalsNextCheckAt)
				require.Equal(t, now.Add(backoff), refreshed.SignalsNextCheckAt.UTC())
				expected := initial
				expected.SignalsFailCount = attempt + 1
				expected.SignalsNextCheckAt = refreshed.SignalsNextCheckAt
				require.Equal(t, expected, refreshed, "quota cutoff must preserve the subject, signals, license and their successful check state")
				require.Equal(t, []string{"acme/b", "acme/a"}, github.calls)
				require.EqualValues(t, 3, stats["githubRequests"])
				require.EqualValues(t, tc.remaining, stats["githubRemaining"])
				require.EqualValues(t, 1, stats["failed"].(map[string]any)[GitHub])
				require.Equal(t, "quota", stats["stopped"].(map[string]any)[GitHub])
				require.Equal(t, true, stats["githubQuotaReserved"])
				require.Zero(t, stats["changed"])
				require.Empty(t, invalidations(t, st))
				detailAfter, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "quota", "en-US")
				require.NoError(t, err)
				require.Equal(t, detailBefore.OpenSource, detailAfter.OpenSource)
				require.Equal(t, detailBefore.Repository, detailAfter.Repository)
				now = refreshed.SignalsNextCheckAt.UTC()
			}
		})
	}
}

func TestSourceFactsSignalRefreshUsesSignalsUpdate(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"signals-update","name":["Signals Update"],"version":"1","homepage":"https://github.com/acme/signals-update","url":"https://example.test/signals-update.dmg","artifacts":[{"app":["Signals Update.app"]}]}`)
	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
		result := mitRepo(repo)
		if etag != "" {
			stars := int64(2000)
			result.Repository.StargazersCount = &stars
		}
		return result, nil
	}}
	svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	initial := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	require.NoError(t, st.DB.Exec(`UPDATE package_source_facts SET fail_count=2 WHERE package_id=? AND source='github'`, initial.PackageID).Error)

	t.Cleanup(func() {
		_ = st.DB.Exec(`DROP TRIGGER IF EXISTS reject_source_fact_insert ON package_source_facts`).Error
		_ = st.DB.Exec(`DROP FUNCTION IF EXISTS reject_source_fact_insert()`).Error
	})
	require.NoError(t, st.DB.Exec(`CREATE FUNCTION reject_source_fact_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'signal refresh must use UPDATE'; END $$`).Error)
	require.NoError(t, st.DB.Exec(`CREATE TRIGGER reject_source_fact_insert BEFORE INSERT ON package_source_facts FOR EACH ROW EXECUTE FUNCTION reject_source_fact_insert()`).Error)

	now = now.Add(25 * time.Hour)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	rows, err = st.SourceFacts(ctx)
	require.NoError(t, err)
	refreshed := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	require.Equal(t, initial.CheckedAt, refreshed.CheckedAt)
	require.Equal(t, initial.NextCheckAt, refreshed.NextCheckAt)
	require.Equal(t, 2, refreshed.FailCount)
	require.Equal(t, int64(2000), openStars(t, refreshed))
	require.Zero(t, refreshed.SignalsUnavailableCount)
}

func openStars(t *testing.T, row store.SourceFact) int64 {
	t.Helper()
	var github sources.GitHubFact
	require.NoError(t, json.Unmarshal(row.Data, &github))
	require.NotNil(t, github.Stars)
	return *github.Stars
}

func TestSourceFactsUnavailableRepositoryBacksOffAndClearsAfterThreeFailures(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"gone","name":["Gone"],"version":"1","homepage":"https://github.com/acme/gone","url":"https://example.test/gone.dmg","artifacts":[{"app":["Gone.app"]}]}`)
	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
		if etag == "" {
			result := mitRepo(repo)
			result.Repository.Archived = true
			return result, nil
		}
		return sources.RepoResult{NotFound: true, Requests: 1, Remaining: 4000, Limit: 5000}, nil
	}}
	svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	initial := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	initialData := string(initial.Data)
	initialSignalsCheckedAt := *initial.SignalsCheckedAt
	initialDetail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "gone", "en-US")
	require.NoError(t, err)
	require.NotNil(t, initialDetail.OpenSource)
	require.True(t, initialDetail.OpenSource.Archived)

	for attempt := 1; attempt <= 3; attempt++ {
		if attempt == 1 {
			now = now.Add(25 * time.Hour)
		} else {
			now = *rows[store.SourceFactKey{PackageID: 1, Source: GitHub}].SignalsNextCheckAt
		}
		_, err = svc.Run(ctx)
		require.NoError(t, err)
		rows, err = st.SourceFacts(ctx)
		require.NoError(t, err)
		row := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
		require.Equal(t, initial.CheckedAt, row.CheckedAt)
		require.Equal(t, initial.NextCheckAt, row.NextCheckAt)
		require.Equal(t, initialSignalsCheckedAt, *row.SignalsCheckedAt)
		require.Equal(t, attempt, row.SignalsFailCount)
		switch attempt {
		case 1, 2:
			require.Equal(t, initial.Subject, row.Subject)
			require.Equal(t, initial.Status, row.Status)
			require.Equal(t, initialData, string(row.Data))
			require.WithinDuration(t, now.Add(time.Duration(1<<(attempt-1))*time.Hour), *row.SignalsNextCheckAt, time.Second)
		case 3:
			require.Empty(t, row.Subject)
			require.Equal(t, "found", row.Status)
			require.NotContains(t, string(row.Data), `"stars"`)
			require.NotContains(t, string(row.Data), `"pushedAt"`)
			require.Nil(t, row.ETag)
			require.Equal(t, 3, row.SignalsUnavailableCount)
		}
	}
	public, err := st.PublicSourceFacts(ctx, 1)
	require.NoError(t, err)
	require.Len(t, public, 1)
	detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "gone", "en-US")
	require.NoError(t, err)
	require.Equal(t, initialDetail.OpenSource, detail.OpenSource, "clearing preserves the entire license snapshot")
	require.Nil(t, detail.Repository, "a cleared subject must not publish a repository from the license snapshot")
	require.NotContains(t, string(mustJSON(t, detail)), `"repository"`)
	require.Equal(t, []string{"cache:invalidate:seo-content:pkg:1"}, invalidations(t, st))
}

func TestSourceFactsClearedRepositoryRetriesAndRecoversWithinSignalBackoff(t *testing.T) {
	for _, tt := range []struct {
		name, license, wantLicense string
	}{
		{name: "no_license"},
		{name: "non_osi", license: "BUSL-1.1"},
		{name: "osi", license: "Apache-2.0", wantLicense: "Apache-2.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewFactsStore(t)
			addCask(t, st, `{"token":"recover","name":["Recover"],"version":"1","homepage":"https://github.com/acme/gone","url":"https://github.com/acme/recovered/releases/download/v1/Recover.dmg","artifacts":[{"app":["Recover.app"]}]}`)
			now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
			signalCalls := 0
			github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
				if repo == "acme/gone" && (signalCalls > 0 || etag != "") {
					signalCalls++
					return sources.RepoResult{NotFound: true, Requests: 1, Remaining: 4000, Limit: 5000}, nil
				}
				result := mitRepo(repo)
				if repo == "acme/recovered" {
					require.Empty(t, etag, "the previous repository's ETag must not be reused")
					if tt.license == "" {
						result.Repository.License = nil
					} else {
						result.Repository.License.SPDXID = tt.license
					}
				}
				return result, nil
			}}
			svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
			_, err := svc.Run(ctx)
			require.NoError(t, err)
			rows, err := st.SourceFacts(ctx)
			require.NoError(t, err)
			initial := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
			for attempt := 0; attempt < 3; attempt++ {
				now = rows[store.SourceFactKey{PackageID: 1, Source: GitHub}].SignalsNextCheckAt.UTC()
				_, err = svc.Run(ctx)
				require.NoError(t, err)
				rows, err = st.SourceFacts(ctx)
				require.NoError(t, err)
			}
			cleared := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
			require.Empty(t, cleared.Subject)
			require.Equal(t, "found", cleared.Status)
			require.NotNil(t, cleared.SignalsNextCheckAt)
			require.Equal(t, 3, cleared.SignalsUnavailableCount)
			require.Equal(t, initial.CheckedAt, cleared.CheckedAt)
			require.Equal(t, initial.NextCheckAt, cleared.NextCheckAt)

			now = cleared.SignalsNextCheckAt.UTC()
			_, err = svc.Run(ctx)
			require.NoError(t, err)
			rows, err = st.SourceFacts(ctx)
			require.NoError(t, err)
			recovered := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
			require.Equal(t, "acme/recovered", recovered.Subject)
			detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "recover", "en-US")
			require.NoError(t, err)
			if tt.wantLicense == "" {
				require.Equal(t, "none", recovered.Status)
				require.Nil(t, detail.OpenSource, "the recovered repository must not inherit MIT")
			} else {
				require.Equal(t, "found", recovered.Status)
				require.NotNil(t, detail.OpenSource)
				require.Equal(t, tt.wantLicense, detail.OpenSource.License)
				require.Equal(t, "acme/recovered", detail.OpenSource.Repo)
				require.Equal(t, now, detail.OpenSource.CheckedAt.UTC())
			}
			require.Equal(t, now, recovered.CheckedAt.UTC(), "a changed subject requires a full license check")
			require.Equal(t, next(now, recheck, 1), recovered.NextCheckAt.UTC())
			require.Zero(t, recovered.SignalsFailCount)
			require.Zero(t, recovered.SignalsUnavailableCount)
			require.Equal(t, []string{"acme/gone", "acme/gone", "acme/gone", "acme/gone", "acme/gone", "acme/recovered"}, github.calls)
			require.Equal(t, int64(1234), openStars(t, recovered))
			require.NotNil(t, detail.Repository)
			require.Equal(t, "https://github.com/acme/recovered", detail.Repository.Url)
		})
	}
}

func TestSourceFactsTransientFailurePreservesUnavailableCount(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"interrupted","name":["Interrupted"],"version":"1","homepage":"https://github.com/acme/interrupted","url":"https://example.test/interrupted.dmg","artifacts":[{"app":["Interrupted.app"]}]}`)
	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	signalCalls := 0
	github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
		if etag == "" {
			return mitRepo(repo), nil
		}
		signalCalls++
		if signalCalls == 2 {
			return sources.RepoResult{Requests: 1, Remaining: 4000, Limit: 5000}, errors.New("github repository status 500")
		}
		return sources.RepoResult{NotFound: true, Requests: 1, Remaining: 4000, Limit: 5000}, nil
	}}
	svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	initial := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	invalidations(t, st)

	// 404 -> 500 -> 404 -> 404: only the unavailable responses advance the count.
	for attempt, wantUnavailable := range []int{1, 1, 2, 3} {
		now = rows[store.SourceFactKey{PackageID: 1, Source: GitHub}].SignalsNextCheckAt.UTC()
		_, err = svc.Run(ctx)
		require.NoError(t, err)
		rows, err = st.SourceFacts(ctx)
		require.NoError(t, err)
		row := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
		require.Equal(t, wantUnavailable, row.SignalsUnavailableCount, "attempt %d", attempt+1)
		require.Equal(t, attempt+1, row.SignalsFailCount)
		require.Equal(t, initial.CheckedAt, row.CheckedAt)
		require.Equal(t, initial.NextCheckAt, row.NextCheckAt)
		if wantUnavailable < 3 {
			require.Equal(t, initial.Subject, row.Subject)
			require.JSONEq(t, string(initial.Data), string(row.Data))
			require.Empty(t, invalidations(t, st))
		} else {
			require.Empty(t, row.Subject)
			require.Equal(t, []string{"cache:invalidate:seo-content:pkg:1"}, invalidations(t, st))
		}
	}
	require.Equal(t, 4, signalCalls)
}

func TestSourceFactsMixedSignalFailuresDoNotClearAfterOneUnavailableResponse(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"mixed","name":["Mixed"],"version":"1","homepage":"https://github.com/acme/mixed","url":"https://example.test/mixed.dmg","artifacts":[{"app":["Mixed.app"]}]}`)
	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	signalCalls := 0
	github := &githubStub{repo: func(repo, etag string) (sources.RepoResult, error) {
		if etag == "" {
			return mitRepo(repo), nil
		}
		signalCalls++
		switch signalCalls {
		case 1, 2:
			return sources.RepoResult{Requests: 1, Remaining: 4000, Limit: 5000}, errors.New("github repository status 500")
		case 3:
			return sources.RepoResult{NotFound: true, Requests: 1, Remaining: 4000, Limit: 5000}, nil
		default:
			return mitRepo(repo), nil
		}
	}}
	svc := &Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	for attempt := 0; attempt < 3; attempt++ {
		rows, queryErr := st.SourceFacts(ctx)
		require.NoError(t, queryErr)
		now = rows[store.SourceFactKey{PackageID: 1, Source: GitHub}].SignalsNextCheckAt.UTC()
		_, err = svc.Run(ctx)
		require.NoError(t, err)
	}
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	row := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	require.Equal(t, 3, signalCalls)
	require.Equal(t, "acme/mixed", row.Subject)
	require.Equal(t, "found", row.Status)
	require.Equal(t, 3, row.SignalsFailCount)
	require.Equal(t, 1, row.SignalsUnavailableCount)
	require.NotNil(t, row.SignalsNextCheckAt)
	require.Equal(t, int64(1234), openStars(t, row))
	detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "mixed", "en-US")
	require.NoError(t, err)
	require.NotNil(t, detail.OpenSource)
	require.NotNil(t, detail.Repository)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}

func TestSourceFactsJobStopsOnRateLimitsAndBacksOffOnErrors(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"alpha","name":["Alpha"],"version":"1","homepage":"https://github.com/acme/alpha","url":"https://github.com/acme/alpha-bin/releases/download/v1/a.dmg","artifacts":[{"app":["Alpha.app"]},{"uninstall":[{"quit":"com.acme.alpha"}]}]}`)
	addCask(t, st, `{"token":"gamma","name":["Gamma"],"version":"1","homepage":"https://github.com/acme/gamma","url":"https://example.test/g.dmg","artifacts":[{"app":["Gamma.app"]}]}`)
	now := time.Date(2026, 10, 14, 3, 35, 0, 0, time.UTC)
	github := &githubStub{repo: func(string, string) (sources.RepoResult, error) { return sources.RepoResult{}, sources.ErrRateLimited }}
	appStore := &storeStub{lookup: func([]string) (map[string]sources.Listing, error) { return nil, errors.New("connection reset") }}
	svc := &Service{Store: st, GitHub: github, AppStore: appStore, Now: func() time.Time { return now }}
	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Len(t, github.calls, 1)
	require.Equal(t, "rate_limited", stats["stopped"].(map[string]any)["github"])
	require.EqualValues(t, 1, stats["failed"].(map[string]any)["app_store"])
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	failed := rows[store.SourceFactKey{PackageID: 1, Source: AppStore}]
	require.Equal(t, "error", failed.Status)
	require.Equal(t, 1, failed.FailCount)
	require.WithinDuration(t, now.Add(time.Hour), failed.NextCheckAt, time.Second)

	// The quota guard stops after the app whose requests leave only the last tenth, but that app's
	// candidates are all read first, so its answer is complete.
	github.calls = nil
	github.repo = func(repo, _ string) (sources.RepoResult, error) {
		result := mitRepo(repo)
		if repo == "acme/alpha" {
			result.Repository.License = nil
		}
		result.Remaining, result.Limit = 400, 5000
		return result, nil
	}
	stats, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/alpha", "acme/alpha-bin"}, github.calls)
	require.Equal(t, "quota", stats["stopped"].(map[string]any)["github"])
	rows, err = st.SourceFacts(ctx)
	require.NoError(t, err)
	require.Equal(t, "acme/alpha-bin", rows[store.SourceFactKey{PackageID: 1, Source: GitHub}].Subject)
	_, gamma := rows[store.SourceFactKey{PackageID: 2, Source: GitHub}]
	require.False(t, gamma, "left for the next run")
}

func TestSourceFactFailurePreservesSuccessfulCheck(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	addCask(t, st, `{"token":"alpha","name":["Alpha"],"version":"1"}`)
	checked := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	fact := store.SourceFact{PackageID: 1, Source: AppStore, Input: "com.example.alpha", Subject: "com.example.alpha", Status: "found", Data: json.RawMessage(`{"price":9,"languages":["EN"]}`), CheckedAt: checked, NextCheckAt: checked.Add(24 * time.Hour)}
	require.NoError(t, st.SaveSourceFact(ctx, fact, "alpha", false))
	initial, err := st.PublicSourceFacts(ctx, 1)
	require.NoError(t, err)
	require.Len(t, initial, 1)
	for attempt := 1; attempt <= 2; attempt++ {
		now := checked.Add(time.Duration(attempt) * 24 * time.Hour)
		next := now.Add(time.Hour)
		require.NoError(t, st.RecordSourceFactFailure(ctx, store.SourceFactKey{PackageID: 1, Source: AppStore}, fact.Input, now, next))
		rows, err := st.PublicSourceFacts(ctx, 1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, initial[0].CheckedAt, rows[0].CheckedAt)
		require.JSONEq(t, string(initial[0].Data), string(rows[0].Data))
		require.Equal(t, attempt, rows[0].FailCount)
		require.WithinDuration(t, next, rows[0].NextCheckAt, time.Second)
	}
}

func TestSourceFactsChangedInputFailureRespectsBackoff(t *testing.T) {
	for _, source := range []string{GitHub, AppStore} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewFactsStore(t)
			raw := `{"token":"alpha","name":["Alpha"],"version":"1","homepage":"https://github.com/example/new"}`
			input, subject := "example/new", "example/old"
			if source == AppStore {
				raw = `{"token":"alpha","name":["Alpha"],"version":"1","artifacts":[{"app":["Alpha.app"]},{"uninstall":[{"quit":"com.example.new"}]}]}`
				input, subject = "com.example.new", "com.example.old"
			}
			addCask(t, st, raw)
			now := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
			checked := now.Add(-24 * time.Hour)
			etag := `W/"old"`
			old := store.SourceFact{PackageID: 1, Source: source, Input: subject, Subject: subject, Status: "found", Data: json.RawMessage(`{"price":9,"languages":["EN"]}`), ETag: &etag, CheckedAt: checked, NextCheckAt: now.Add(24 * time.Hour)}
			require.NoError(t, st.SaveSourceFact(ctx, old, "alpha", false))
			calls := 0
			failing := true
			github := &githubStub{repo: func(repo, sentETag string) (sources.RepoResult, error) {
				calls++
				require.Equal(t, input, repo)
				require.Empty(t, sentETag, "old subject's ETag must not be sent to the new repository")
				if failing {
					return sources.RepoResult{}, errors.New("connection reset")
				}
				return mitRepo(repo), nil
			}}
			appStore := &storeStub{lookup: func(ids []string) (map[string]sources.Listing, error) {
				calls++
				require.Equal(t, []string{input}, ids)
				if failing {
					return nil, errors.New("connection reset")
				}
				return map[string]sources.Listing{input: {Name: "Alpha", URL: "https://apps.apple.com/us/app/alpha/id1", BundleID: input, Price: 0, Currency: "USD", Region: "US", Languages: []string{"EN"}}}, nil
			}}
			svc := &Service{Store: st, GitHub: github, AppStore: appStore, Now: func() time.Time { return now }}
			run := func() { t.Helper(); _, err := svc.Run(ctx); require.NoError(t, err) }
			run()
			require.Equal(t, 1, calls, "changed input is checked immediately")
			rows, err := st.SourceFacts(ctx)
			require.NoError(t, err)
			row := rows[store.SourceFactKey{PackageID: 1, Source: source}]
			require.Equal(t, input, row.Input)
			require.Equal(t, old.Subject, row.Subject)
			require.Equal(t, old.Status, row.Status)
			require.Equal(t, old.ETag, row.ETag)
			require.JSONEq(t, string(old.Data), string(row.Data))
			require.True(t, checked.Equal(row.CheckedAt))
			require.Equal(t, 1, row.FailCount)
			require.True(t, now.Add(time.Hour).Equal(row.NextCheckAt))
			run()
			now = now.Add(59 * time.Minute)
			run()
			require.Equal(t, 1, calls, "new input must respect the retry deadline")
			now = now.Add(time.Minute)
			run()
			require.Equal(t, 2, calls)
			now = now.Add(time.Hour)
			run()
			require.Equal(t, 2, calls, "second failure backs off for two hours")
			now = now.Add(time.Hour)
			failing = false
			run()
			require.Equal(t, 3, calls)
			rows, err = st.SourceFacts(ctx)
			require.NoError(t, err)
			row = rows[store.SourceFactKey{PackageID: 1, Source: source}]
			require.Equal(t, input, row.Subject)
			require.Equal(t, "found", row.Status)
			require.Zero(t, row.FailCount)
			require.True(t, now.Equal(row.CheckedAt))
		})
	}
}
