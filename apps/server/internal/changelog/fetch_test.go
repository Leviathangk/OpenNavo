package changelog

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTagVersions(t *testing.T) {
	for tag, want := range map[string]string{"v1.2.3": "1.2.3", "V1.2": "1.2", "release-2.0.0-rc.1": "2.0.0-rc.1", "release/2.4": "2.4", "editor_3.1": "3.1", "editor-3.1": "3.1", "tip": "", "nightly": "", "latest": ""} {
		require.Equal(t, want, TagVersion(tag, "owner/editor"))
	}

}

func TestGitHubConditionalRequestIgnoresChangingETag(t *testing.T) {
	f := NewFetcher("test")
	modified := "Fri, 16 Oct 2026 00:00:00 GMT"
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Empty(t, r.Header.Get("If-None-Match"))
		require.Equal(t, modified, r.Header.Get("If-Modified-Since"))
		return reply(http.StatusNotModified, ""), nil
	})
	result, err := f.Fetch(context.Background(), Candidate{Token: "sample"}, FetchSource{
		Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/s/sample.rb"},
		ETag: "download-count-changed", LastModified: modified,
	})
	require.NoError(t, err)
	require.True(t, result.NotModified)
	modified = "" // Old ETag-only records must refresh unconditionally once.
	_, _, _, err = f.get(context.Background(), "https://api.github.com/repos/example/app/releases/latest", 1024,
		FetchSource{ETag: "legacy-tag"})
	require.NoError(t, err)
}

func TestHomebrewCommitHistoryAndBottleExclusion(t *testing.T) {
	f := NewFetcher("test")
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "Casks/v/visual-studio-code.rb", r.URL.Query().Get("path"))
		return reply(200, `[{"sha":"abc","commit":{"message":"visual-studio-code 1.140.0\n\nBump","committer":{"date":"2026-09-30T01:00:00Z"}}},{"sha":"def","commit":{"message":"visual-studio-code: update to 1.139.0","author":{"date":"2026-09-23T01:00:00Z"}}},{"commit":{"message":"visual-studio-code: update 1.140.0 bottle."}},{"commit":{"message":"Unrelated cleanup"}}]`), nil
	})
	source := FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/v/visual-studio-code.rb"}}
	result, err := f.Fetch(context.Background(), Candidate{Token: "visual-studio-code"}, source)
	require.NoError(t, err)
	require.Len(t, result.Versions, 2)
	require.Equal(t, "1.140.0", result.Versions[0].Version)
	require.False(t, result.Versions[1].CommittedAt.IsZero())
	since := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	source.Since = &since
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "2026-09-30T00:00:00Z", r.URL.Query().Get("since"))
		return reply(304, ""), nil
	})
	result, err = f.Fetch(context.Background(), Candidate{}, source)
	require.NoError(t, err)
	require.True(t, result.NotModified)
}
func TestHomebrewCommitTitlesWithAnnotationsAndNewCaskFiles(t *testing.T) {
	created, missing := strings.Repeat("1", 40), strings.Repeat("2", 40)
	commits := `[{"sha":"a","commit":{"message":"app 2.3 (new cask) (#456)","committer":{"date":"2026-09-30T01:00:00Z"}}},` +
		`{"sha":"b","commit":{"message":"app 2.2 (arm only)","committer":{"date":"2026-09-20T01:00:00Z"}}},` +
		`{"sha":"c","commit":{"message":"app 2.1 (#123)","committer":{"date":"2026-09-10T01:00:00Z"}}},` +
		`{"sha":"d","commit":{"message":"app: add livecheck (#7)","committer":{"date":"2026-09-05T01:00:00Z"}}},` +
		`{"sha":"e","commit":{"message":"app latest (new cask)","committer":{"date":"2026-09-04T01:00:00Z"}}},` +
		`{"sha":"` + missing + `","commit":{"message":"Add App (new cask)","committer":{"date":"2026-09-02T01:00:00Z"}}},` +
		`{"sha":"` + created + `","commit":{"message":"feat(app): add cask (new cask)","committer":{"date":"2026-09-01T01:00:00Z"}}}]`
	rawStatus := http.StatusOK
	f := NewFetcher("test")
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			return reply(200, commits), nil
		case "raw.githubusercontent.com":
			if r.URL.Path == "/Homebrew/homebrew-cask/"+created+"/Casks/a/app.rb" {
				return reply(rawStatus, "cask \"app\" do\n  version \"0.9-beta.1\"\nend\n"), nil
			}
			return reply(404, ""), nil
		}
		t.Fatalf("unexpected request %s", r.URL)
		return nil, nil
	})
	source := FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/a/app.rb"}}
	result, err := f.Fetch(context.Background(), Candidate{Token: "app"}, source)
	require.NoError(t, err)
	versions := []string{}
	for _, version := range result.Versions {
		versions = append(versions, version.Version)
	}
	require.Equal(t, []string{"2.3", "2.2", "2.1", "0.9-beta.1"}, versions)
	require.Equal(t, created, result.Versions[3].SHA)
	require.Equal(t, time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC), result.Versions[3].CommittedAt)

	// A failed file read fails the fetch, so the bounded history is read again on retry.
	rawStatus = http.StatusBadGateway
	_, err = f.Fetch(context.Background(), Candidate{Token: "app"}, source)
	require.Error(t, err)
}
func TestFetchErrorsAndMarkdownSanitizing(t *testing.T) {
	f := NewFetcher("test")
	for _, source := range []FetchSource{{Type: "invalid"}, {Type: "github_releases", Config: map[string]any{"repo": "../bad"}}, {Type: "homebrew_commits", Config: map[string]any{"repo": "bad"}}, {Type: "sparkle", Config: map[string]any{"url": "http://127.0.0.1/private"}}, {Type: "webpage"}} {
		_, err := f.Fetch(context.Background(), Candidate{}, source)
		require.Error(t, err)
	}
	for _, typ := range []string{"github_releases", "homebrew_commits", "sparkle", "webpage"} {
		source := FetchSource{Type: typ, Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/a/app.rb", "url": "https://example.com/feed", "urlTemplate": "https://example.com/{version}"}}
		for _, status := range []int{404, 403, 429} {
			f.HTTP.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				r := reply(status, "invalid")
				r.Header.Set("X-Ratelimit-Reset", "9999999999")
				return r, nil
			})
			_, err := f.Fetch(context.Background(), Candidate{}, source)
			require.Error(t, err)
		}
		f.HTTP.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return reply(200, "invalid"), nil })
		_, err := f.Fetch(context.Background(), Candidate{}, source)
		require.Error(t, err)
	}
	source := FetchSource{Type: "github_releases", Config: map[string]any{"repo": "a/b"}}
	f.HTTP.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("network unavailable") })
	_, err := f.Fetch(context.Background(), Candidate{}, source)
	require.Error(t, err)
	normalized, hash := NormalizeMarkdown("<!-- comment -->\n<b>Text</b>\n\n\n![alt](https://example.com/img)\n`<T>`\n```cpp\n<T>\n```")
	require.Len(t, hash, 64)
	require.NotContains(t, normalized, "comment")
	require.NotContains(t, normalized, "<b>")
	require.Contains(t, normalized, "[Image: alt]")
	require.Contains(t, normalized, "`<T>`")
	require.Contains(t, normalized, "```cpp\n<T>")
	require.NotContains(t, normalized, "\n\n\n")
	large, _ := NormalizeMarkdown(strings.Repeat("中", 100000))
	require.LessOrEqual(t, len(large), 200*1024)
	require.True(t, strings.HasSuffix(large, "(Source text truncated because it is too long)"))
}
