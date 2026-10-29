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
	calls = nil
	now = now.Add(25 * time.Hour)
	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/open W/\"e1\"", "acme/closed "}, calls)
	require.EqualValues(t, 2, stats["githubRequests"])
	rows, err := st.SourceFacts(ctx)
	require.NoError(t, err)
	open := rows[store.SourceFactKey{PackageID: 1, Source: GitHub}]
	closed := rows[store.SourceFactKey{PackageID: 2, Source: GitHub}]
	require.Equal(t, int64(2000), openStars(t, open))
	require.WithinDuration(t, now, *open.SignalsCheckedAt, time.Second)
	require.WithinDuration(t, now, *closed.SignalsCheckedAt, time.Second)
	require.Equal(t, "acme/closed", closed.Subject)
	require.Equal(t, "none", closed.Status)
	detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "closed", "en-US")
	require.NoError(t, err)
	require.Nil(t, detail.OpenSource, "non-OSI repository must not publish openSource")
	require.Equal(t, "https://github.com/acme/closed", detail.Repository.Url)
	require.Nil(t, detail.Repository.Stars, "GitHub omitted stargazers_count in this response")
}

func openStars(t *testing.T, row store.SourceFact) int64 {
	t.Helper()
	var github sources.GitHubFact
	require.NoError(t, json.Unmarshal(row.Data, &github))
	require.NotNil(t, github.Stars)
	return *github.Stars
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
