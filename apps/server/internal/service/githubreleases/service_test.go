package githubreleases

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

type snapshotStore struct {
	token    string
	tokenErr error
	writes   int
	assets   []store.GitHubReleaseAsset
	at       time.Time
}

func (s *snapshotStore) GitHubToken(context.Context, config.Config) (string, error) {
	return s.token, s.tokenErr
}
func (s *snapshotStore) RecordGitHubReleaseSnapshot(_ context.Context, assets []store.GitHubReleaseAsset, at time.Time) error {
	s.writes++
	s.assets, s.at = assets, at
	return nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(t *testing.T, status int, data any) *http.Response {
	t.Helper()
	body, err := json.Marshal(data)
	require.NoError(t, err)
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}
}

func TestSnapshotPaginatesAllReleasesAndAssetsAndReloadsToken(t *testing.T) {
	ctx := context.Background()
	st := &snapshotStore{token: "first-test-credential"}
	at := time.Date(2026, 10, 29, 16, 0, 0, 0, time.UTC)
	var paths []string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "api.github.com", r.URL.Host)
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer "+st.token, r.Header.Get("Authorization"))
		require.Equal(t, "2022-11-28", r.Header.Get("X-GitHub-Api-Version"))
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/repos/opennavo/OpenNavo/releases":
			if r.URL.Query().Get("page") == "1" {
				batch := make([]release, 100)
				for i := range batch {
					batch[i] = release{ID: int64(i + 1), Tag: "server-v1"}
				}
				batch[0].Tag = "desktop-v0.1.3"
				return reply(t, 200, batch), nil
			}
			return reply(t, 200, []release{{ID: 101, Tag: "desktop-v0.1.4"}}), nil
		case "/repos/opennavo/OpenNavo/releases/1/assets":
			if r.URL.Query().Get("page") == "1" {
				batch := make([]map[string]any, 100)
				for i := range batch {
					batch[i] = map[string]any{"id": i + 1, "name": "SHA256SUMS", "download_count": i}
				}
				return reply(t, 200, batch), nil
			}
			return reply(t, 200, []map[string]any{{"id": 101, "name": "OpenNavo_x64.app.tar.gz", "download_count": 582}}), nil
		case "/repos/opennavo/OpenNavo/releases/101/assets":
			return reply(t, 200, []map[string]any{{"id": 102, "name": "OpenNavo_0.1.4_universal.dmg", "download_count": 953}}), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
			return nil, errors.New("unexpected request")
		}
	})}
	svc := &Service{Store: st, HTTP: client, Now: func() time.Time { return at }}
	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"releases": 2, "assets": 102}, stats)
	require.Len(t, paths, 5)
	require.Len(t, st.assets, 102, "all assets are persisted, including excluded categories")
	require.Equal(t, at, st.at)
	st.token = "rotated-test-credential"
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, st.writes)
	st.token = ""
	_, err = svc.Run(ctx)
	require.Error(t, err)
	require.Len(t, paths, 10, "a missing token makes no API call")
	require.Equal(t, 2, st.writes)
}

func TestSnapshotIncludesOnlyPublishedStableAndPrereleaseDesktopReleases(t *testing.T) {
	st := &snapshotStore{token: strings.Repeat("x", 16)}
	expected := []store.GitHubReleaseAsset{
		{ReleaseTag: "desktop-v0.1.4", AssetID: 11, AssetName: "OpenNavo_0.1.4_universal.dmg", DownloadCount: 100},
		{ReleaseTag: "desktop-v0.2.0-beta.1", AssetID: 21, AssetName: "OpenNavo_0.2.0-beta.1_universal.dmg", DownloadCount: 20},
		{ReleaseTag: "desktop-v0.2.0-rc.1", AssetID: 31, AssetName: "OpenNavo_aarch64.app.tar.gz", DownloadCount: 5},
	}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/repos/opennavo/OpenNavo/releases":
			// Use GitHub's wire format to cover both publication flags.
			return reply(t, 200, []map[string]any{
				{"id": 1, "tag_name": "desktop-v0.1.4", "prerelease": false, "draft": false},
				{"id": 2, "tag_name": "desktop-v0.2.0-beta.1", "prerelease": true, "draft": false},
				{"id": 3, "tag_name": "desktop-v0.2.0-rc.1", "prerelease": true, "draft": false},
				{"id": 4, "tag_name": "server-v0.2.0-beta.1", "prerelease": true},
				{"id": 5, "tag_name": "desktop-v0.2.0", "prerelease": false, "draft": true},
				{"id": 6, "tag_name": "desktop-v0.3.0-beta.1", "prerelease": true, "draft": true},
			}), nil
		case "/repos/opennavo/OpenNavo/releases/1/assets":
			return reply(t, 200, []map[string]any{{"id": 11, "name": expected[0].AssetName, "download_count": 100}}), nil
		case "/repos/opennavo/OpenNavo/releases/2/assets":
			return reply(t, 200, []map[string]any{{"id": 21, "name": expected[1].AssetName, "download_count": 20}}), nil
		case "/repos/opennavo/OpenNavo/releases/3/assets":
			return reply(t, 200, []map[string]any{{"id": 31, "name": expected[2].AssetName, "download_count": 5}}), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
			return nil, errors.New("unexpected request")
		}
	})}
	stats, err := (&Service{Store: st, HTTP: client}).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]any{"releases": 3, "assets": 3}, stats)
	require.Equal(t, expected, st.assets, "stable, beta and RC assets enter the same snapshot")
	require.Equal(t, 1, st.writes)
}

func TestSnapshotWithOnlyDraftReleasesDoesNotWrite(t *testing.T) {
	st := &snapshotStore{token: "draft-test-credential"}
	calls := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "/repos/opennavo/OpenNavo/releases", r.URL.Path, "draft assets must not be fetched")
		return reply(t, 200, []map[string]any{{"id": 1, "tag_name": "desktop-v0.2.0", "draft": true}}), nil
	})}
	_, err := (&Service{Store: st, HTTP: client}).Run(context.Background())
	require.Error(t, err)
	require.Equal(t, 1, calls)
	require.Zero(t, st.writes)
}

func TestSnapshotFailuresNeverWritePartialDataOrEchoCredentials(t *testing.T) {
	for _, failure := range []string{"missing token", "token read", "connection", "403", "500", "malformed", "null", "missing count", "empty", "later asset page"} {
		t.Run(failure, func(t *testing.T) {
			st := &snapshotStore{token: "private-test-credential"}
			if failure == "missing token" {
				st.token = ""
			}
			if failure == "token read" {
				st.tokenErr = errors.New("private-test-credential")
			}
			calls := 0
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return reply(t, 200, []release{{ID: 1, Tag: "desktop-v0.1.3"}, {ID: 2, Tag: "desktop-v0.1.4"}}), nil
				}
				if failure == "connection" {
					return nil, errors.New("private-test-credential")
				}
				if failure == "403" {
					return reply(t, 403, "private-test-credential"), nil
				}
				if failure == "500" {
					return reply(t, 500, "private-test-credential"), nil
				}
				if failure == "malformed" {
					return reply(t, 200, "private-test-credential"), nil
				}
				if failure == "null" {
					return reply(t, 200, nil), nil
				}
				if failure == "missing count" {
					return reply(t, 200, []map[string]any{{"id": 1, "name": "file"}}), nil
				}
				if failure == "empty" {
					return reply(t, 200, []asset{}), nil
				}
				if failure == "later asset page" && calls == 3 {
					return reply(t, 503, "private-test-credential"), nil
				}
				return reply(t, 200, []map[string]any{{"id": 1, "name": "OpenNavo_x64.app.tar.gz", "download_count": 1}}), nil
			})}
			_, err := (&Service{Store: st, HTTP: client}).Run(context.Background())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-test-credential")
			require.Zero(t, st.writes)
		})
	}
}
