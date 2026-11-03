package changelog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Fixtures are the first page (per_page=5) of GET /repos/{repo}/releases recorded anonymously on 2026-10-31,
// trimmed to the fields the fetcher reads. They carry no credentials.
// publicRepo answers the repository visibility check as public and passes release list requests on.
func publicRepo(next func(*http.Request) (*http.Response, error)) roundTrip {
	return roundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/releases") {
			return reply(http.StatusOK, `{"private":false,"visibility":"public"}`), nil
		}
		return next(r)
	})
}

func fixtureFetcher(t *testing.T, file string, header http.Header) (*Fetcher, *int) {
	t.Helper()
	body, err := os.ReadFile("testdata/github_releases/" + file) //nolint:gosec // file comes only from the fixed fixture names in this package.
	require.NoError(t, err)
	calls := 0
	f := NewFetcher("test")
	f.HTTP.Client.Transport = publicRepo(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "api.github.com", r.URL.Host)
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		response := reply(http.StatusOK, string(body))
		for key, values := range header {
			response.Header[key] = values
		}
		return response, nil
	})
	return f, &calls
}

func TestGitHubReleasesRecordedApps(t *testing.T) {
	for _, tc := range []struct {
		file, repo string
		versions   []string
		prerelease map[string]bool
	}{
		{"rectangle.json", "rxhanson/Rectangle", []string{"2.0.4", "2.0.3", "2.0.2", "2.0.1", "2.0"}, nil},
		{"iina.json", "iina/iina", []string{"1.5.0", "1.5.0-beta2", "1.5.0-beta1", "1.4.4", "1.4.3"}, map[string]bool{"1.5.0-beta2": true, "1.5.0-beta1": true}},
		{"stats.json", "exelban/stats", []string{"3.0.23", "3.0.22", "3.0.21", "3.0.20", "3.0.19"}, nil},
		{"monitorcontrol.json", "MonitorControl/MonitorControl", []string{"4.4.0", "4.3.3", "4.3.2", "4.2.0", "4.1.0"}, nil},
		{"maccy.json", "p0deje/Maccy", []string{"2.8.0", "2.7.1", "2.7.0", "2.6.1", "2.6.0"}, nil},
	} {
		t.Run(tc.repo, func(t *testing.T) {
			f, _ := fixtureFetcher(t, tc.file, http.Header{"Etag": {`W/"recorded"`}})
			result, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "github_releases", Config: map[string]any{"repo": tc.repo}})
			require.NoError(t, err)
			require.Equal(t, `W/"recorded"`, result.ETag)
			require.Len(t, result.Releases, len(tc.versions))
			for i, release := range result.Releases {
				require.Equal(t, tc.versions[i], release.Version)
				require.Equal(t, tc.prerelease[release.Version], release.Prerelease)
				require.NotEmpty(t, release.Body, release.Tag)
				require.Len(t, release.BodyHash, 64)
				require.NotNil(t, release.PublishedAt)
				require.True(t, strings.HasPrefix(release.SourceURL, "https://github.com/"+tc.repo+"/releases/"), release.SourceURL)
				require.NotContains(t, release.Body, "<script")
			}
		})
	}
}

func TestGitHubReleasesSkipDraftsAndSanitize(t *testing.T) {
	f := NewFetcher("test")
	long := strings.Repeat("a", 300*1024)
	f.HTTP.Client.Transport = publicRepo(func(r *http.Request) (*http.Response, error) {
		return reply(200, `[{"tag_name":"v3.0","draft":true,"body":"secret"},
{"tag_name":"v2.0-rc.1","prerelease":true,"name":"RC","body":"<script>alert(1)</script>**rc**","published_at":"2026-10-01T00:00:00Z","html_url":"https://evil.example/x"},
{"tag_name":"nightly","body":"n"},
{"tag_name":"app-1.9","name":"`+strings.Repeat("t", 400)+`","body":"`+long+`","published_at":"2026-09-01T00:00:00Z"}]`), nil
	})
	result, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "github_releases", Config: map[string]any{"repo": "owner/app"}})
	require.NoError(t, err)
	require.Len(t, result.Releases, 2)
	rc := result.Releases[0]
	require.Equal(t, "2.0-rc.1", rc.Version)
	require.True(t, rc.Prerelease)
	require.Equal(t, "alert(1)**rc**", rc.Body)
	require.Equal(t, "https://github.com/owner/app/releases/tag/v2.0-rc.1", rc.SourceURL)
	old := result.Releases[1]
	require.Equal(t, "1.9", old.Version)
	require.LessOrEqual(t, len(old.Body), 200*1024)
	require.Contains(t, old.Body, "truncated")
	require.Len(t, []rune(old.Title), releaseTitleMaxRune)
}

func TestGitHubReleasesConditionalRequest(t *testing.T) {
	f := NewFetcher("test")
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, `W/"old"`, r.Header.Get("If-None-Match"))
		return reply(http.StatusNotModified, ""), nil
	})
	result, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "github_releases", ETag: `W/"old"`, Config: map[string]any{"repo": "iina/iina"}})
	require.NoError(t, err)
	require.True(t, result.NotModified)
	require.Empty(t, result.Releases)
	require.Equal(t, `W/"old"`, result.ETag)
}

func TestGitHubReleasesPauseOnLowQuota(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	reset := now.Add(30 * time.Minute)
	f, calls := fixtureFetcher(t, "stats.json", http.Header{"X-Ratelimit-Remaining": {"400"}, "X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Reset": {strconv.FormatInt(reset.Unix(), 10)}})
	f.Now = func() time.Time { return now }
	source := FetchSource{Type: "github_releases", Config: map[string]any{"repo": "exelban/stats"}}
	// The list response itself reports a quota inside the reserve. Storing it would need the
	// visibility request, which would spend that reserve, so the fetch fails closed instead.
	result, err := f.Fetch(context.Background(), Candidate{}, source)
	var rate *RateLimitError
	require.True(t, errors.As(err, &rate))
	require.Empty(t, result.Releases, "nothing is stored without the visibility check")
	require.Equal(t, reset.Unix(), rate.Reset.Unix())
	require.Equal(t, 1, *calls, "the denied visibility request is not made")
	_, err = f.Fetch(context.Background(), Candidate{}, source)
	require.True(t, errors.As(err, &rate))
	require.Equal(t, 1, *calls, "paused fetch must not spend a request")
	f.Now = func() time.Time { return reset.Add(time.Second) }
	result, err = f.Fetch(context.Background(), Candidate{}, source)
	require.NoError(t, err)
	require.Len(t, result.Releases, 5)
	require.Equal(t, 2, *calls)
}

func TestGitHubReleasesRateLimitResponses(t *testing.T) {
	reset := time.Now().Add(time.Hour).Unix()
	for _, header := range []http.Header{
		{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(reset, 10)}},
		{"Retry-After": {"60"}},
	} {
		f := NewFetcher("test")
		f.HTTP.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			response := reply(http.StatusForbidden, "")
			response.Header = header
			return response, nil
		})
		_, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "github_releases", Config: map[string]any{"repo": "a/b"}})
		var rate *RateLimitError
		require.True(t, errors.As(err, &rate), header)
	}
}

func TestReleaseTagNormalization(t *testing.T) {
	for tag, want := range map[string]string{"v1.5.0": "1.5.0", "V2.0": "2.0", "2.8.0": "2.8.0", "v1.5.0-beta2": "1.5.0-beta2",
		"release-3.1": "3.1", "Rectangle-0.86": "0.86", "desktop-v1.2.3": "1.2.3", "nightly": "", "latest": ""} {
		require.Equal(t, want, ReleaseVersion(tag, "owner/Rectangle"), tag)
	}
	for left, right := range map[string]string{"2.0": "2.0.0", "v1.4.4": "1.4.4", "1.2.3,456": "1.2.3", "3.0-RC1": "3.0.0-rc1"} {
		require.Equal(t, VersionKey(left), VersionKey(right), left)
	}
	require.NotEqual(t, VersionKey("1.10"), VersionKey("1.1"))
	require.NotEqual(t, VersionKey("2.0-beta1"), VersionKey("2.0"))
}

func TestPrereleaseTagsNeverJoinStableVersions(t *testing.T) {
	for tag, want := range map[string]string{"v3.0-RC1": "3.0-rc1", "v1.5.0-Beta2": "1.5.0-beta2", "v2.0b3": "2.0b3",
		"v1.0.0-pre.1": "1.0.0-pre.1", "v4.1-dev3": "4.1-dev3", "V1.0a1": "1.0a1", "v2.0.0-alpha": "2.0.0-alpha", "1.2.3": "1.2.3"} {
		version := ReleaseVersion(tag, "owner/app")
		require.Equal(t, want, version, tag)
		if want != "1.2.3" {
			require.True(t, IsPrereleaseVersion(version), tag)
			require.NotEqual(t, VersionKey(strings.TrimRight(want[:len(want)-len(strings.TrimLeft(want, "0123456789."))], ".")), VersionKey(version), tag)
		}
	}
	require.NotEqual(t, VersionKey("2.0"), VersionKey("2.0b3"))
	require.Equal(t, VersionKey("3.0.0-RC1"), VersionKey("3.0-rc1"))

	f := NewFetcher("test")
	f.HTTP.Client.Transport = publicRepo(func(*http.Request) (*http.Response, error) {
		return reply(200, `[{"tag_name":"v1.0.0","prerelease":true,"body":"candidate"},{"tag_name":"v2.0","body":"new"},{"tag_name":"2.0.0","body":"old duplicate"},{"tag_name":"v3.0-RC1","prerelease":true,"body":"rc"}]`), nil
	})
	result, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "github_releases", Config: map[string]any{"repo": "owner/app"}})
	require.NoError(t, err)
	got := []string{}
	for _, release := range result.Releases {
		got = append(got, release.Version+"="+release.Body)
	}
	require.Equal(t, []string{"1.0.0-prerelease=candidate", "2.0=new", "3.0-rc1=rc"}, got)
}

func TestForbiddenIsRateLimitOnlyWhenQuotaSpent(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	var rate *RateLimitError
	require.False(t, errors.As(checkStatus(403, http.Header{"X-Ratelimit-Remaining": {"4000"}, "X-Ratelimit-Reset": {reset}}), &rate))
	var status *StatusError
	require.True(t, errors.As(checkStatus(403, http.Header{"X-Ratelimit-Remaining": {"4000"}, "X-Ratelimit-Reset": {reset}}), &status))
	require.True(t, errors.As(checkStatus(403, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {reset}}), &rate))
	require.True(t, errors.As(checkStatus(403, http.Header{"Retry-After": {"30"}}), &rate))
	require.True(t, errors.As(checkStatus(429, http.Header{}), &rate))
	soon := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	require.True(t, errors.As(checkStatus(429, http.Header{"X-Ratelimit-Reset": {soon}, "Retry-After": {"600"}}), &rate))
	require.WithinDuration(t, time.Now().Add(10*time.Minute), rate.Reset, 5*time.Second, "the later of reset and Retry-After")
	require.True(t, errors.As(checkStatus(429, http.Header{"X-Ratelimit-Reset": {reset}, "Retry-After": {"5"}}), &rate))
	require.WithinDuration(t, time.Now().Add(time.Hour), rate.Reset, 5*time.Second)
}

func TestResolveGitHubReleasesFromVerifiedRepo(t *testing.T) {
	r := NewResolver("test")
	r.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("resolve must not request upstream")
		return nil, nil
	})
	sources, stats, err := r.Resolve(context.Background(), Candidate{Kind: "cask", Token: "iina", RubySourcePath: "Casks/i/iina.rb", GitHubRepo: "iina/iina"})
	require.NoError(t, err)
	require.Len(t, sources, 2)
	require.Equal(t, "github_releases", sources[1].Type)
	require.Equal(t, "iina/iina", sources[1].Config["repo"])
	require.Equal(t, true, stats["github"])
	sources, _, err = r.Resolve(context.Background(), Candidate{Kind: "formula", Token: "x", GitHubRepo: "bad/../repo"})
	require.NoError(t, err)
	require.Empty(t, sources)
	sources, _, err = r.Resolve(context.Background(), Candidate{Kind: "link", Token: "x", GitHubRepo: "owner/app"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
}

func TestReleaseVersionWhitelist(t *testing.T) {
	for tag, want := range map[string]string{
		// Unknown suffixes stay in the version.
		"v1.2.3-hotfix": "1.2.3-hotfix", "v1.2.3-patch1": "1.2.3-patch1", "1.1.1w": "1.1.1w", "OpenSSL_1.1.1w": "1.1.1w",
		"2.0.0a": "2.0.0a", "v1.2.3+build.7": "1.2.3",
		// Known pre-release suffixes, unchanged from the previous round.
		"v3.0-RC1": "3.0-rc1", "v1.5.0-Beta2": "1.5.0-beta2", "v2.0b3": "2.0b3", "v1.0.0-pre.1": "1.0.0-pre.1",
		"v1.0.0-prerelease": "1.0.0-prerelease",
		// Not versions.
		"3a4f9c1": "", "Beta-2": "", "rc-3": "", "latest": "", "1.2.3_x!": "",
	} {
		require.Equal(t, want, ReleaseVersion(tag, "owner/openssl"), tag)
	}
	for _, v := range []string{"1.2.3-hotfix", "1.2.3-patch1", "1.1.1w", "2.0.0a"} {
		require.False(t, IsPrereleaseVersion(v), v)
	}
	for _, v := range []string{"3.0-rc1", "1.5.0-beta2", "2.0b3", "1.0.0-pre.1", "1.0.0-prerelease", "1.0a1"} {
		require.True(t, IsPrereleaseVersion(v), v)
	}
	require.NotEqual(t, VersionKey("1.2.3"), VersionKey("1.2.3-hotfix"))
	require.NotEqual(t, VersionKey("1.1.1v"), VersionKey("1.1.1w"))
	require.NotEqual(t, VersionKey("1.0.0-prerelease"), VersionKey("1.0.0-pre"))
	require.Equal(t, "", VersionKey("latest"))
	require.Equal(t, "", VersionKey(""))
}

func TestReleaseVersionAcceptsCommonUpstreamTagFormats(t *testing.T) {
	for _, testCase := range []struct{ name, tag, want string }{
		{"scoped package", "pkg@1.2.3", "1.2.3"},
		{"scoped npm package", "@scope/pkg@1.2.3", "1.2.3"},
		{"numeric revision", "v1.2.3-1", "1.2.3-1"},
		{"underscored core", "curl-8_5_0", "8.5.0"},
		{"underscored openssl", "OpenSSL_1_1_1w", "1.1.1w"},
		{"word prefix", "go1.22.0", "1.22.0"},
		{"short prefix", "r1.2", "1.2"},
		{"underscored revision", "v8.5.0_1", "8.5.0.1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, ReleaseVersion(testCase.tag, "owner/repo"))
		})
	}
}

func TestGitHubReleasesKeepSuffixedReleasesApart(t *testing.T) {
	f := NewFetcher("test")
	f.HTTP.Client.Transport = publicRepo(func(*http.Request) (*http.Response, error) {
		return reply(200, `[{"tag_name":"v1.2.3-hotfix","body":"hotfix"},{"tag_name":"v1.2.3","body":"real"},
{"tag_name":"1.2.3_x!","body":"invalid"},{"tag_name":"1.1.1w","body":"w"},{"tag_name":"1.1.1v","body":"v"},
{"tag_name":"3a4f9c1","body":"hash"},{"tag_name":"Beta-2","body":"beta"}]`), nil
	})
	result, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "github_releases", Config: map[string]any{"repo": "owner/openssl"}})
	require.NoError(t, err)
	got := []string{}
	for _, release := range result.Releases {
		got = append(got, release.Version+"="+release.Body)
	}
	require.Equal(t, []string{"1.2.3-hotfix=hotfix", "1.2.3=real", "1.1.1w=w", "1.1.1v=v"}, got)
}

func TestGitHubReleasesSkipPrivateRepository(t *testing.T) {
	f := NewFetcher("test")
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			return reply(200, `[{"tag_name":"v1.0","body":"private notes"}]`), nil
		}
		return reply(200, `{"private":true,"visibility":"private"}`), nil
	})
	result, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "github_releases", Config: map[string]any{"repo": "owner/app"}})
	var status *StatusError
	require.True(t, errors.As(err, &status))
	require.Equal(t, http.StatusNotFound, status.Status)
	require.Empty(t, result.Releases)
}

func TestGitHubReleasesVisibilityFailureStoresNothing(t *testing.T) {
	// The visibility read is the gate that keeps a token with private access from publishing private
	// notes. A non-200 answer must fail the fetch, not fall through to "assume public"; before this
	// test, swallowing the error and storing the releases left every case green.
	for _, testCase := range []struct {
		name        string
		status      int
		rateLimit   bool
		extraHeader map[string]string
	}{
		{name: "rate limited", status: http.StatusForbidden, rateLimit: true, extraHeader: map[string]string{"X-Ratelimit-Remaining": "0"}},
		{name: "server error", status: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			f := NewFetcher("test")
			f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/releases") {
					return reply(200, `[{"tag_name":"v1.0.0","body":"private notes"}]`), nil
				}
				response := reply(testCase.status, "no visibility")
				for key, value := range testCase.extraHeader {
					response.Header.Set(key, value)
				}
				return response, nil
			})
			result, err := f.Fetch(context.Background(), Candidate{}, FetchSource{
				Type: "github_releases", Config: map[string]any{"repo": "owner/app"},
			})
			require.Error(t, err)
			var rate *RateLimitError
			require.Equal(t, testCase.rateLimit, errors.As(err, &rate))
			require.Empty(t, result.Releases, "nothing may be stored from an ungated visibility read")
		})
	}
}

func TestGitHubReleasesBackfillsOnceThenSkipsTheVisibilityRequestForAnUnchangedList(t *testing.T) {
	page := func() string {
		items := make([]string, releasesPerPage)
		for i := range items {
			items[i] = fmt.Sprintf(`{"tag_name":"v1.%d.0","body":"notes"}`, i)
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	f := NewFetcher("test")
	listCalls, metaCalls := 0, 0
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/releases") {
			metaCalls++
			return reply(200, `{"private":false,"archived":true}`), nil
		}
		listCalls++
		if r.URL.Query().Get("page") == "2" {
			return reply(200, `[{"tag_name":"v0.9.0","body":"old"}]`), nil
		}
		return reply(200, page()), nil
	})
	source := FetchSource{Type: "github_releases", Config: map[string]any{"repo": "owner/app"}, Backfill: true}
	result, err := f.Fetch(context.Background(), Candidate{}, source)
	require.NoError(t, err)
	require.Len(t, result.Releases, releasesPerPage+1, "the first fetch reads older pages too")
	require.Equal(t, 2, listCalls)
	require.Equal(t, 1, metaCalls)
	require.NotNil(t, result.Archived)
	require.True(t, *result.Archived, "an archived repository is reported so the schedule can slow down")
	require.NotEmpty(t, result.ContentHash)

	// A later fetch reads page one only, recognises the same list by hash and returns before the
	// repository visibility request that only gates writes.
	source.Backfill, source.ContentHash = false, result.ContentHash
	result, err = f.Fetch(context.Background(), Candidate{}, source)
	require.NoError(t, err)
	require.True(t, result.NotModified)
	require.Empty(t, result.Releases)
	require.Equal(t, 3, listCalls)
	require.Equal(t, 1, metaCalls, "an unchanged list must not spend a second request")

	// A changed list still verifies visibility before anything may be stored.
	source.ContentHash = "stale"
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			return reply(200, `[{"tag_name":"v2.0.0","body":"new"}]`), nil
		}
		metaCalls++
		return reply(200, `{"private":false,"archived":false}`), nil
	})
	result, err = f.Fetch(context.Background(), Candidate{}, source)
	require.NoError(t, err)
	require.Len(t, result.Releases, 1)
	require.Equal(t, 2, metaCalls)
	require.NotNil(t, result.Archived)
	require.False(t, *result.Archived)
}
