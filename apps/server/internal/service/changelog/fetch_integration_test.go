//go:build integration

package changelog_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/changelog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type queuedSources struct {
	Count int
	Type  string
}

func (q *queuedSources) Enqueue(context.Context, string, jobs.Payload, jobs.Metadata) (string, error) {
	q.Count++
	return "queued", nil
}
func TestCommitFetchIdempotenceHistoryAndBackoff(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, sid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='sample'").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("DELETE FROM job_outbox").Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw("INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{\"repo\":\"Homebrew/homebrew-cask\",\"path\":\"Casks/s/sample.rb\"}',90,'auto') RETURNING id", pid).Scan(&sid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE packages SET rank_30d=100 WHERE id=?", pid).Error)
	body := `[{"sha":"first","commit":{"message":"sample 1.0.0","committer":{"date":"2026-09-30T00:00:00Z"}}}]`
	status := 200
	requests := 0
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: status, Header: http.Header{"Etag": {`"v1"`}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	now := time.Now().UTC()
	svc := &changelog.Service{Store: st, Fetcher: fetcher, Now: func() time.Time { return now }}
	stats, err := svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 1, stats["versions"])
	var changes int64
	require.NoError(t, st.DB.WithContext(ctx).Table("catalog_changes").Count(&changes).Error)
	_, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	var repeated int64
	require.NoError(t, st.DB.WithContext(ctx).Table("catalog_changes").Count(&repeated).Error)
	require.Equal(t, changes, repeated)
	status = 304
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, true, stats["notModified"])
	var state struct {
		LastStatus  string
		ETag        string
		NextFetchAt time.Time
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT last_status,etag,next_fetch_at FROM changelog_sources WHERE id=?", sid).Scan(&state).Error)
	require.Equal(t, "not_modified", state.LastStatus)
	require.WithinDuration(t, now.Add(3*time.Hour), state.NextFetchAt, time.Second)
	status = 404
	_, err = svc.Fetch(ctx, sid)
	require.Error(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT last_status,next_fetch_at FROM changelog_sources WHERE id=?", sid).Scan(&state).Error)
	require.Equal(t, "not_found", state.LastStatus)
	require.WithinDuration(t, now.Add(2*time.Hour), state.NextFetchAt, time.Second)
	status = 200
	body = `[{"sha":"abc","commit":{"message":"sample 0.9.0","committer":{"date":"2026-09-01T00:00:00Z"}}}]`
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET type='homebrew_commits',config='{\"repo\":\"Homebrew/homebrew-cask\",\"path\":\"Casks/s/sample.rb\"}' WHERE id=?", sid).Error)
	_, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	var sha string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT brew_commit_sha FROM package_versions WHERE package_id=? AND version='0.9.0'", pid).Scan(&sha).Error)
	require.Equal(t, "abc", sha)
	queue := &queuedSources{}
	svc.Queue = queue
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET next_fetch_at=? WHERE id=?", now.Add(-time.Minute), sid).Error)
	stats, err = svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["enqueued"])
	require.Equal(t, 1, queue.Count)
	var retiredID int64
	require.NoError(t, st.DB.Raw("INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by,next_fetch_at) VALUES(?,'github_releases','{}',20,'auto',?) RETURNING id", pid, now.Add(-time.Hour)).Scan(&retiredID).Error)
	before := requests
	_, err = svc.Fetch(ctx, retiredID)
	require.Error(t, err)
	require.Equal(t, before, requests, "retired sources must never reach the transport")
	queue.Count = 0
	stats, err = svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["sources"])
	require.Equal(t, 1, queue.Count, "only the Homebrew source is scheduled")
}

func TestCommitTitlesWithAnnotationsDateVersionsAndFullReread(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1.2.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, sid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='sample'").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw("INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{\"repo\":\"Homebrew/homebrew-cask\",\"path\":\"Casks/s/sample.rb\"}',90,'auto') RETURNING id", pid).Scan(&sid).Error)
	body := `[{"sha":"c","commit":{"message":"sample 1.2.0 (#300)","committer":{"date":"2026-09-30T00:00:00Z"}}},` +
		`{"sha":"b","commit":{"message":"sample 1.1.0 (arm only)","committer":{"date":"2026-09-20T00:00:00Z"}}},` +
		`{"sha":"a","commit":{"message":"sample 1.0.0 (new cask)","committer":{"date":"2026-09-10T00:00:00Z"}}}]`
	var requests []*http.Request
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r)
		return &http.Response{StatusCode: 200, Header: http.Header{"Etag": {`"v1"`}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	svc := &changelog.Service{Store: st, Fetcher: fetcher, Now: func() time.Time { return now }}
	stats, err := svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 3, stats["versions"])
	var dated []string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT version FROM package_versions WHERE package_id=? AND brew_committed_at IS NOT NULL ORDER BY version", pid).Scan(&dated).Error)
	require.Equal(t, []string{"1.0.0", "1.1.0", "1.2.0"}, dated)

	// With the current version dated, later reads are incremental.
	_, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.NotEmpty(t, requests[len(requests)-1].URL.Query().Get("since"))

	// Dropping the cursor (as migration 00027 does) reads the bounded history in full once more.
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET etag=NULL,last_modified=NULL,last_fetched_at=NULL WHERE id=?", sid).Error)
	_, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	last := requests[len(requests)-1]
	require.Empty(t, last.URL.Query().Get("since"))
	require.Empty(t, last.Header.Get("If-None-Match"))
}
