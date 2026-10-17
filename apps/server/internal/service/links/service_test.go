package links

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

type githubFake struct {
	t     *testing.T
	calls int
}

func (g *githubFake) Get(_ context.Context, target string, _ int64, h http.Header) ([]byte, int, http.Header, error) {
	require.Equal(g.t, "https://api.github.com/repos/owner/app/releases/latest", target)
	g.calls++
	if g.calls > 1 {
		require.Empty(g.t, h.Get("If-None-Match"))
		require.Equal(g.t, "Fri, 16 Oct 2026 00:00:00 GMT", h.Get("If-Modified-Since"))
		return nil, 304, http.Header{}, nil
	}
	return []byte(`{"tag_name":"v2.0","published_at":"2026-10-16T00:00:00Z","html_url":"https://github.com/owner/app/releases/tag/v2.0","body":"Release notes","assets":[{"name":"App-arm64.dmg","browser_download_url":"https://github.com/owner/app/releases/download/v2.0/App-arm64.dmg","size":123}]}`), 200, http.Header{"Etag": {`"github-1"`}, "Last-Modified": {"Fri, 16 Oct 2026 00:00:00 GMT"}}, nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestIndependentValidatorsAndNoInstallationDownloads(t *testing.T) {
	ctx := context.Background()
	g := &githubFake{t: t}
	calls := 0
	service := Service{GitHub: g, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://example.com/appcast.xml", r.URL.String())
		require.Empty(t, r.Header.Get("Authorization"))
		status, body := 200, `<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><pubDate>Thu, 15 Oct 2026 12:00:00 +0000</pubDate><enclosure url="https://example.com/App.zip" length="12" sparkle:version="1.9"/></item></channel></rss>`
		if calls > 1 {
			require.Equal(t, `"feed-1"`, r.Header.Get("If-None-Match"))
			status, body = 304, ""
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Etag": {`"feed-1"`}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	repo, feed := "owner/app", "https://example.com/appcast.xml"
	row := store.LinkApp{}
	row.GithubRepo, row.SparkleFeed = &repo, &feed
	first, source, body, etag, err := service.Read(ctx, row)
	require.NoError(t, err)
	require.Equal(t, "github_release", source)
	require.Equal(t, "2.0", first.Version)
	require.Equal(t, "arm64", *first.Assets[0].Arch)
	require.Equal(t, "Release notes", body)
	row.ETag = &etag
	second, _, _, etag2, err := service.Read(ctx, row)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.JSONEq(t, etag, etag2)
	require.Equal(t, 2, g.calls)
	require.Equal(t, 2, calls)
	var state cacheState
	require.NoError(t, json.Unmarshal([]byte(etag), &state))
	require.Len(t, state, 2)
}
func TestAppcastRejectsDeltasAndUnsafeAssets(t *testing.T) {
	raw := `<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><pubDate>Fri, 16 Oct 2026 12:00:00 +0000</pubDate><enclosure url="file:///tmp/App.zip" sparkle:version="3"/><enclosure url="https://example.com/delta.zip" sparkle:version="2" sparkle:deltaFrom="1"/></item></channel></rss>`
	_, _, err := parseAppcast([]byte(raw), "https://example.com/feed", nil)
	require.Error(t, err)
	_, _, err = parseGitHub([]byte(`{"tag_name":"v2","published_at":"2026-10-16T00:00:00Z","html_url":"https://github.com/o/r","assets":[{"name":"x","browser_download_url":"https://127.0.0.1/x"}]}`), nil)
	require.Error(t, err)
}
