package changelog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommitFallbackKeepsHundredEntriesAndClearsValidators(t *testing.T) {
	f := NewFetcher("test")
	requests := 0
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		size, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if size == 100 {
			return reply(500, "upstream private response"), nil
		}
		require.Empty(t, r.Header.Get("If-None-Match"))
		require.Empty(t, r.Header.Get("If-Modified-Since"))
		require.Equal(t, 10, size)
		entries := []map[string]any{}
		for n := (page - 1) * size; n < page*size; n++ {
			entries = append(entries, map[string]any{"sha": fmt.Sprint(n), "commit": map[string]any{"message": fmt.Sprintf("sample 1.%d", n)}})
		}
		raw, err := json.Marshal(entries)
		require.NoError(t, err)
		response := reply(200, string(raw))
		response.Header.Set("Link", `<https://evil.example/steal>; rel="next"`)
		response.Header.Set("ETag", "page-etag")
		require.Equal(t, "api.github.com", r.URL.Host)
		return response, nil
	})
	result, err := f.Fetch(context.Background(), Candidate{Token: "sample"}, FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/s/sample.rb"}, ETag: "old", LastModified: "old-date"})
	require.NoError(t, err)
	require.Len(t, result.Versions, 100)
	require.Equal(t, 11, requests)
	require.Empty(t, result.ETag)
	require.Empty(t, result.LastModified)
}

func TestCommitFallbackFailureIsAtomicAndBounded(t *testing.T) {
	f := NewFetcher("test")
	calls := 0
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("per_page") == "10" && r.URL.Query().Get("page") == "1" {
			entries := []map[string]any{}
			for n := 0; n < 10; n++ {
				entries = append(entries, map[string]any{"sha": fmt.Sprint(n), "commit": map[string]any{"message": fmt.Sprintf("sample 1.%d", n)}})
			}
			raw, err := json.Marshal(entries)
			require.NoError(t, err)
			response := reply(200, string(raw))
			response.Header.Set("Link", `<https://api.github.com/>; rel="next"`)
			return response, nil
		}
		return reply(502, "secret body"), nil
	})
	result, err := f.Fetch(context.Background(), Candidate{Token: "sample"}, FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/s/sample.rb"}})
	require.Error(t, err)
	require.Empty(t, result.Versions)
	// Three page sizes, then one request per shrinking history window.
	require.Equal(t, 4+len(historyWindows), calls)
	category, status := Failure(err)
	require.Equal(t, "upstream_server_error", category)
	require.Equal(t, 502, status)
	require.NotContains(t, err.Error(), "secret")
}

func TestCommitClientErrorsDoNotMultiplyRequests(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			f := NewFetcher("test")
			calls := 0
			f.HTTP.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { calls++; return reply(status, "private body"), nil })
			_, err := f.Fetch(context.Background(), Candidate{}, FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/s/sample.rb"}})
			require.Error(t, err)
			require.Equal(t, 1, calls)
			require.NotContains(t, FailureMessage(err), "private")
		})
	}
}

func TestCommitSmallestFallbackAndDeduplication(t *testing.T) {
	f := NewFetcher("test")
	calls := 0
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("per_page") != "1" {
			return reply(503, ""), nil
		}
		page := r.URL.Query().Get("page")
		response := reply(200, `[{"sha":"same","commit":{"message":"sample 1.0"}}]`)
		if page == "1" {
			response.Header.Set("Link", `<https://api.github.com/>; rel="next"`)
		}
		return response, nil
	})
	result, err := f.Fetch(context.Background(), Candidate{Token: "sample"}, FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/s/sample.rb"}})
	require.NoError(t, err)
	require.Len(t, result.Versions, 1)
	require.Equal(t, 4, calls)
}

// GitHub answers 500 for some file histories that cross a huge commit; a window starting after it reads fine.
func TestCommitHistoryServerErrorRetriesShrinkingWindows(t *testing.T) {
	f := NewFetcher("test")
	var windows []time.Time
	f.HTTP.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		since := r.URL.Query().Get("since")
		if since == "" {
			return reply(500, `{"message":"Server Error"}`), nil
		}
		start, err := time.Parse(time.RFC3339, since)
		require.NoError(t, err)
		windows = append(windows, start)
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		require.Empty(t, r.Header.Get("If-None-Match"))
		require.Empty(t, r.Header.Get("If-Modified-Since"))
		// The offending commit is 18 months old: only windows starting after it succeed.
		if start.Before(time.Now().Add(-18 * 30 * 24 * time.Hour)) {
			return reply(500, `{"message":"Server Error"}`), nil
		}
		response := reply(200, `[{"sha":"aaaa","commit":{"message":"sample 2.0","committer":{"date":"2026-09-01T00:00:00Z"}}},{"sha":"bbbb","commit":{"message":"sample 1.9"}}]`)
		response.Header.Set("ETag", "window-etag")
		return response, nil
	})
	result, err := f.Fetch(context.Background(), Candidate{Token: "sample"}, FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/t/sample.rb"}, ETag: "old"})
	require.NoError(t, err)
	require.Len(t, result.Versions, 2)
	require.Equal(t, "2.0", result.Versions[0].Version)
	require.Empty(t, result.ETag, "a window's validator never stands in for the full history's")
	require.Len(t, windows, 3, "3 years and 2 years fail, 1 year succeeds")
	require.True(t, windows[0].Before(windows[1]) && windows[1].Before(windows[2]))

	// An incremental read (since already set) does not widen into windows.
	calls := 0
	f.HTTP.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { calls++; return reply(500, ""), nil })
	since := time.Now().Add(-48 * time.Hour)
	_, err = f.Fetch(context.Background(), Candidate{Token: "sample"}, FetchSource{Type: "homebrew_commits", Config: map[string]any{"repo": "Homebrew/homebrew-cask", "path": "Casks/t/sample.rb"}, Since: &since})
	require.Error(t, err)
	require.Equal(t, 3, calls)
}
