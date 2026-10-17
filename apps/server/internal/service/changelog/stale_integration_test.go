//go:build integration

package changelog_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/changelog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCurrentVersionDateBackfillStaleTransitionsAndFailures(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	item, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1,20","ruby_source_path":"Casks/s/sample.rb"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: item}}))
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	pid := rows["sample"].ID
	var sid int64
	require.NoError(t, st.DB.Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by,last_fetched_at,last_status,etag)
 VALUES(?,'homebrew_commits','{"repo":"Homebrew/homebrew-cask","path":"Casks/s/sample.rb"}',90,'auto',?,'ok','old-validator') RETURNING id`, pid, now.Add(-time.Hour)).Scan(&sid).Error)
	// A date for another build of the same base version is insufficient.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at) VALUES(?,'1,19','1',?)`, pid, now.Add(-120*24*time.Hour)).Error)
	// Old fetches may have stored a zero date; a valid backfill must repair it.
	require.NoError(t, st.DB.Exec(`UPDATE package_versions SET brew_committed_at=? WHERE package_id=? AND version='1,20'`, time.Time{}, pid).Error)
	status, requests := http.StatusOK, 0
	body := `[{"sha":"old","commit":{"message":"sample 1,20","committer":{"date":"2026-06-01T12:00:00Z"}}},{"sha":"invalid","commit":{"message":"sample 0.0"}},{"sha":"future","commit":{"message":"sample 9.0","committer":{"date":"2099-01-01T00:00:00Z"}}}]`
	wantFullHistory := true
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if wantFullHistory {
			require.Empty(t, r.URL.Query().Get("since"))
			require.Empty(t, r.Header.Get("If-None-Match"))
			require.Empty(t, r.Header.Get("If-Modified-Since"))
		} else {
			require.Equal(t, now.Add(-24*time.Hour).Format(time.RFC3339), r.URL.Query().Get("since"))
		}
		header := http.Header{"Etag": {"new-validator"}}
		if status == http.StatusTooManyRequests {
			header.Set("X-Ratelimit-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	svc := &changelog.Service{Store: st, Fetcher: fetcher, Now: func() time.Time { return now }}
	stats, err := svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 1, stats["autoDisabled"])
	require.Zero(t, stats["missingVersionDate"])
	p, err := st.PublicPackage(ctx, "cask", "sample", "en-US")
	require.NoError(t, err)
	require.True(t, p.StaleDisabled)
	var invalidDates int64
	require.NoError(t, st.DB.Table("package_versions").Where("package_id=? AND version IN ('0.0','9.0')", pid).Count(&invalidDates).Error)
	require.Zero(t, invalidDates)
	// A stale app remains eligible for background commit fetching.
	require.NoError(t, st.DB.Exec(`UPDATE changelog_sources SET next_fetch_at=? WHERE id=?`, now.Add(-time.Minute), sid).Error)
	due, err := st.ChangelogDue(ctx, now)
	require.NoError(t, err)
	require.Contains(t, due, sid)
	wantFullHistory, status = false, http.StatusTooManyRequests
	stats, err = svc.Fetch(ctx, sid)
	require.Error(t, err)
	require.Equal(t, "rate_limited", stats["status"])
	p, err = st.PublicPackage(ctx, "cask", "sample", "en-US")
	require.NoError(t, err)
	require.True(t, p.StaleDisabled, "a fetch failure cannot erase a known commit date")
	// Full version changes reactivate only the local policy, including build bumps.
	rows, err = st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	item, err = homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1,21","ruby_source_path":"Casks/s/sample.rb"}`))
	require.NoError(t, err)
	lifecycle, err := st.ApplyCatalogBatchWithLifecycle(ctx, []store.CatalogMutation{{Package: item, Previous: rows["sample"]}}, now)
	require.NoError(t, err)
	require.Equal(t, 1, lifecycle.Reactivated)
	wantFullHistory, status = true, http.StatusOK
	body = `[{"sha":"new","commit":{"message":"sample 1,21","committer":{"date":"2026-10-09T12:00:00Z"}}}]`
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Zero(t, stats["autoDisabled"])
	p, err = st.PublicPackage(ctx, "cask", "sample", "en-US")
	require.NoError(t, err)
	require.False(t, p.Disabled)
	// A future date is also unknown and can be repaired by a valid response.
	require.NoError(t, st.DB.Exec(`UPDATE package_versions SET brew_committed_at=? WHERE package_id=? AND version='1,21'`, now.Add(time.Hour), pid).Error)
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.True(t, stats["changed"].(bool))
	state, err := st.ChangelogState(ctx, sid)
	require.NoError(t, err)
	require.NotNil(t, state.CurrentVersionCommittedAt)
	require.True(t, state.CurrentVersionCommittedAt.Equal(now.Add(-24*time.Hour)))
	// Unknown dates remain active on 404 and on a successful unmatched response.
	rows, err = st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	item, err = homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"2","ruby_source_path":"Casks/s/sample.rb"}`))
	require.NoError(t, err)
	_, err = st.ApplyCatalogBatchWithLifecycle(ctx, []store.CatalogMutation{{Package: item, Previous: rows["sample"]}}, now)
	require.NoError(t, err)
	status = http.StatusNotFound
	_, err = svc.Fetch(ctx, sid)
	require.Error(t, err)
	p, err = st.PublicPackage(ctx, "cask", "sample", "en-US")
	require.NoError(t, err)
	require.False(t, p.Disabled)
	status, body = http.StatusOK, `[]`
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 1, stats["missingVersionDate"])
	// Respect the administrator's explicit history exclusion without HTTP calls.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,changelog_excluded) VALUES(?,true)`, pid).Error)
	before := requests
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, true, stats["skipped"])
	require.Equal(t, before, requests)
	due, err = st.ChangelogDue(ctx, now.Add(72*time.Hour))
	require.NoError(t, err)
	require.NotContains(t, due, sid)
}

func TestLatestVersionPreservesNormalHistoryFetching(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	item, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"latest-app","version":"latest","ruby_source_path":"Casks/l/latest-app.rb"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: item}}))
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	pid := rows["latest-app"].ID
	var sid int64
	modified := now.Add(-time.Hour).Format(http.TimeFormat)
	require.NoError(t, st.DB.Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by,last_fetched_at,last_status,etag,last_modified)
 VALUES(?,'homebrew_commits','{"repo":"Homebrew/homebrew-cask","path":"Casks/l/latest-app.rb"}',90,'auto',?,'error','latest-validator',?) RETURNING id`, pid, now.Add(-time.Hour), modified).Scan(&sid).Error)
	requests := 0
	var previousFetch time.Time
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			// Without an incremental window, GitHub uses the saved modification date;
			// its changing representation ETag must not override that validator.
			require.Empty(t, r.URL.Query().Get("since"))
			require.Empty(t, r.Header.Get("If-None-Match"))
			require.Equal(t, modified, r.Header.Get("If-Modified-Since"))
		} else {
			require.Equal(t, previousFetch.Add(-24*time.Hour).Format(time.RFC3339), r.URL.Query().Get("since"))
			// The fetcher intentionally clears validators when the query has Since.
			require.Empty(t, r.Header.Get("If-None-Match"))
			require.Empty(t, r.Header.Get("If-Modified-Since"))
		}
		status, body, header := http.StatusNotModified, "", http.Header{}
		if requests == 2 {
			status, body = http.StatusOK, `[]`
			header.Set("ETag", "updated-validator")
			header.Set("Last-Modified", now.Format(http.TimeFormat))
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	svc := &changelog.Service{Store: st, Fetcher: fetcher, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		stats, err := svc.Fetch(ctx, sid)
		require.NoError(t, err)
		require.Zero(t, stats["autoDisabled"])
		require.Equal(t, i != 1, stats["notModified"])
		state, err := st.ChangelogState(ctx, sid)
		require.NoError(t, err)
		require.Nil(t, state.CurrentVersionCommittedAt)
		if i == 0 {
			require.Equal(t, "latest-validator", *state.ETag)
			require.Equal(t, modified, *state.LastModified)
		} else {
			require.Equal(t, "updated-validator", *state.ETag)
		}
		previousFetch = now
		now = now.Add(48 * time.Hour)
	}
	require.Equal(t, 3, requests)
	p, err := st.PublicPackage(ctx, "cask", "latest-app", "en-US")
	require.NoError(t, err)
	require.False(t, p.Disabled)
}
