package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

type githubFunc func(context.Context, string, int64, http.Header) ([]byte, int, http.Header, error)

func (f githubFunc) Get(ctx context.Context, target string, limit int64, headers http.Header) ([]byte, int, http.Header, error) {
	return f(ctx, target, limit, headers)
}

// Names and prerelease flags observed through the public GitHub API on 2026-10-29.
// Bodies and URLs below are small, public-safe fixtures; no live calls are made.
func releaseFixture(tag, date string, prerelease bool, names ...string) map[string]any {
	assets := []map[string]any{}
	for _, name := range names {
		assets = append(assets, map[string]any{"name": name, "browser_download_url": "https://example.com/" + name, "size": 12})
	}
	return map[string]any{"tag_name": tag, "published_at": date, "prerelease": prerelease, "html_url": "https://github.com/owner/app/releases", "body": "Notes for " + tag, "assets": assets}
}

func TestGithubFiltersSelectDesktopReleases(t *testing.T) {
	p := func(s string) *string { return &s }
	tensorEngine := releaseFixture("v1.1.8", "2026-10-28T07:52:42Z", false, "tensorfold-1.1.8-macos-arm64.tar.gz")
	tensorBeta := releaseFixture("v1.0.0-beta.10", "2026-10-25T19:41:46Z", true, "TensorFold-1.0.0-beta.10-arm64.dmg", "TensorFold-1.0.0-beta.10-arm64.zip", "SHA256SUMS")
	tensorOlder := releaseFixture("v1.0.0-beta.9", "2026-10-22T20:40:19Z", true, "TensorFold-1.0.0-beta.9-arm64.dmg")
	rapidEngine := releaseFixture("v0.16.10", "2026-10-26T23:56:20Z", false, "install.sh", "SHA256SUMS.txt")
	rapidApp := releaseFixture("rapid-mac-v0.16.10", "2026-10-26T23:03:27Z", false, "rapid-mlx-desktop.dmg")
	draft := releaseFixture("v9.0-beta.1", "2026-10-29T00:00:00Z", true, "draft.dmg")
	draft["draft"] = true
	for _, tc := range []struct {
		name                string
		input               domain.LinkInput
		pages               [][]map[string]any
		tag, version, asset string
		noMatch             bool
	}{
		{"tensor beta opt in", domain.LinkInput{GithubAssetPattern: p("*.dmg"), IncludePrereleases: true}, [][]map[string]any{{tensorEngine, tensorBeta, tensorOlder, draft}}, "v1.0.0-beta.10", "1.0.0-beta.10", "TensorFold-1.0.0-beta.10-arm64.dmg", false},
		{"beta excluded", domain.LinkInput{GithubAssetPattern: p("*.dmg")}, [][]map[string]any{{tensorEngine, tensorBeta}}, "", "", "", true},
		{"tensor regex", domain.LinkInput{GithubTagRegex: p(`^v1\.0\.0-beta\.[0-9]+$`), GithubAssetPattern: p("*.dmg"), IncludePrereleases: true}, [][]map[string]any{{tensorEngine}, {tensorOlder, tensorBeta}}, "v1.0.0-beta.10", "1.0.0-beta.10", "TensorFold-1.0.0-beta.10-arm64.dmg", false},
		{"rapid prefix", domain.LinkInput{GithubTagPrefix: p("rapid-mac-v"), GithubAssetPattern: p("*.dmg")}, [][]map[string]any{{rapidEngine, rapidApp}}, "rapid-mac-v0.16.10", "0.16.10", "rapid-mlx-desktop.dmg", false},
		{"rapid asset only", domain.LinkInput{GithubAssetPattern: p("*.dmg")}, [][]map[string]any{{rapidEngine, rapidApp}}, "rapid-mac-v0.16.10", "rapid-mac-v0.16.10", "rapid-mlx-desktop.dmg", false},
		{"all filters required", domain.LinkInput{GithubTagPrefix: p("rapid-mac-v"), GithubAssetPattern: p("*.pkg")}, [][]map[string]any{{rapidEngine, rapidApp}}, "", "", "", true},
		{"case sensitive", domain.LinkInput{GithubTagPrefix: p("Rapid-mac-v")}, [][]map[string]any{{rapidEngine, rapidApp}}, "", "", "", true},
		{"include prereleases only", domain.LinkInput{IncludePrereleases: true}, [][]map[string]any{{tensorBeta, draft}}, "v1.0.0-beta.10", "1.0.0-beta.10", "", false},
		{"no releases", domain.LinkInput{GithubAssetPattern: p("*.dmg")}, [][]map[string]any{{}}, "", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			svc := Service{GitHub: githubFunc(func(_ context.Context, target string, limit int64, headers http.Header) ([]byte, int, http.Header, error) {
				calls++
				require.Equal(t, fmt.Sprintf("https://api.github.com/repos/owner/app/releases?per_page=100&page=%d", calls), target)
				require.Equal(t, int64(4<<20), limit)
				require.Empty(t, headers)
				require.LessOrEqual(t, calls, len(tc.pages))
				h := http.Header{}
				if calls < len(tc.pages) {
					h.Set("Link", `<https://untrusted.example/ignored>; rel="next"`)
				}
				data, err := json.Marshal(tc.pages[calls-1])
				return data, 200, h, err
			})}
			tc.input.GithubRepo = p("owner/app")
			// Old cached metadata and a Sparkle source must not mask a no-match.
			row := store.LinkApp{LinkInput: tc.input, ETag: p(`{"github_release":{"url":"https://api.github.com/repos/owner/app/releases/latest","lastModified":"old"}}`)}
			row.SparkleFeed = p("https://example.com/feed.xml")
			svc.HTTP = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				if tc.noMatch {
					t.Fatal("no-match must not fall back to Sparkle")
				}
				return &http.Response{StatusCode: 404, Header: http.Header{}, Body: http.NoBody}, nil
			})}
			got, source, body, _, err := svc.Read(context.Background(), row)
			require.Equal(t, len(tc.pages), calls)
			if tc.noMatch {
				require.ErrorIs(t, err, ErrNoMatchingRelease)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "github_release", source)
			require.Equal(t, tc.tag, got.Tag)
			require.Equal(t, tc.version, got.Version)
			require.Equal(t, "Notes for "+tc.tag, body)
			if tc.asset != "" {
				require.Len(t, got.Assets, 1)
				require.Equal(t, tc.asset, got.Assets[0].Name)
			}
		})
	}
}

func TestGithubFilterFailuresNeverReturnPartialSelection(t *testing.T) {
	p := func(s string) *string { return &s }
	for _, tc := range []struct {
		name     string
		status   int
		data     string
		next     bool
		fetchErr error
		want     string
	}{
		{"not found", 404, "", false, nil, "github_release_no_match"},
		{"rate limited", 403, "", false, nil, "HTTP 403"},
		{"unexpected cached response", 304, "", false, nil, "HTTP 304"},
		{"malformed", 200, "[", false, nil, "unexpected end"},
		{"transport", 0, "", false, errors.New("read failed"), "read failed"},
		{"scan limit", 200, "[]", true, nil, "github_release_search_limit"},
		{"matching candidate at scan limit", 200, `[ {"tag_name":"v1","published_at":"2026-10-26T00:00:00Z","assets":[{"name":"a.dmg"}]} ]`, true, nil, "github_release_search_limit"},
		{"unsafe matching asset", 200, `[ {"tag_name":"v1","published_at":"2026-10-26T00:00:00Z","html_url":"https://github.com/owner/app","assets":[{"name":"a.dmg","browser_download_url":"file:///a.dmg"}]} ]`, false, nil, "invalid release asset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			svc := Service{GitHub: githubFunc(func(context.Context, string, int64, http.Header) ([]byte, int, http.Header, error) {
				calls++
				h := http.Header{}
				if tc.next {
					h.Set("Link", `<https://example.com>; rel="next"`)
				}
				return []byte(tc.data), tc.status, h, tc.fetchErr
			})}
			got, _, _, _, err := svc.Read(context.Background(), store.LinkApp{LinkInput: domain.LinkInput{GithubRepo: p("owner/app"), GithubAssetPattern: p("*.dmg")}})
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, got)
			if tc.next {
				require.Equal(t, githubReleasePageLimit, calls)
			}
		})
	}
}

func TestGithubFilteredSyncRefetchesAllPages(t *testing.T) {
	repo, pattern := "owner/app", "*.dmg"
	calls := 0
	svc := Service{GitHub: githubFunc(func(_ context.Context, target string, _ int64, h http.Header) ([]byte, int, http.Header, error) {
		calls++
		require.Empty(t, h)
		if strings.HasSuffix(target, "page=1") {
			return []byte("[]"), 200, http.Header{"Link": {`<https://example.com>; rel="next"`}}, nil
		}
		tag := "v1"
		if calls > 2 {
			tag = "v2"
		}
		data, err := json.Marshal([]any{releaseFixture(tag, "2026-10-26T00:00:00Z", false, "App.dmg")})
		return data, 200, http.Header{}, err
	})}
	row := store.LinkApp{LinkInput: domain.LinkInput{GithubRepo: &repo, GithubAssetPattern: &pattern}}
	first, _, _, etag, err := svc.Read(context.Background(), row)
	require.NoError(t, err)
	row.ETag = &etag
	second, _, _, _, err := svc.Read(context.Background(), row)
	require.NoError(t, err)
	require.Equal(t, "v1", first.Tag)
	require.Equal(t, "v2", second.Tag)
	require.Equal(t, 4, calls)
}

func TestGithubFilterUsesRE2OnAdversarialTags(t *testing.T) {
	repo, pattern := "owner/app", `^(a+)+$`
	svc := Service{GitHub: githubFunc(func(context.Context, string, int64, http.Header) ([]byte, int, http.Header, error) {
		// This near miss triggers exponential backtracking in engines without
		// RE2's linear-time guarantee. No timing assertion is needed.
		data, err := json.Marshal([]any{releaseFixture(strings.Repeat("a", 64000)+"!", "2026-10-26T00:00:00Z", false)})
		return data, 200, http.Header{}, err
	})}
	got, _, _, _, err := svc.Read(context.Background(), store.LinkApp{LinkInput: domain.LinkInput{GithubRepo: &repo, GithubTagRegex: &pattern}})
	require.ErrorIs(t, err, ErrNoMatchingRelease)
	require.Nil(t, got)
}
