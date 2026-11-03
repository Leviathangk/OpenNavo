//go:build integration

package changelog_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
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

func TestGitHubReleasesResolveFetchAndConditionalRefresh(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"rectangle","version":"2.0.0","homepage":"https://rectangleapp.com/","url":"https://github.com/rxhanson/Rectangle/releases/download/v2.0/Rectangle2.0.dmg","ruby_source_path":"Casks/r/rectangle.rb","tap_git_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='rectangle'").Scan(&pid).Error)
	// Without a verified public repository only Homebrew history is resolved.
	unresolved, err := st.UnresolvedHomebrewSources(ctx)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
	require.Empty(t, unresolved[0].GitHubRepo)
	resolver := pipeline.NewResolver("test")
	resolver.Client = &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("resolve must not request upstream")
		return nil, nil
	})}
	svc := &changelog.Service{Store: st, Resolver: resolver}
	stats, err := svc.Resolve(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, 1, stats["sources"])
	unresolved, err = st.UnresolvedHomebrewSources(ctx)
	require.NoError(t, err)
	require.Empty(t, unresolved)

	// The source facts job (#64) verified the repository public; the schedule now picks it up.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data) VALUES(?,'github','rxhanson/Rectangle','rxhanson/Rectangle','none','{"repo":"rxhanson/Rectangle"}')`, pid).Error)
	unresolved, err = st.UnresolvedHomebrewSources(ctx)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
	require.Equal(t, "rxhanson/Rectangle", unresolved[0].GitHubRepo)
	stats, err = svc.Resolve(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, 2, stats["sources"])
	require.Equal(t, true, stats["github"])
	var sid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM changelog_sources WHERE package_id=? AND type='github_releases' AND config->>'repo'='rxhanson/Rectangle' AND priority=20", pid).Scan(&sid).Error)
	require.NotZero(t, sid)
	due, err := st.ChangelogDue(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Contains(t, due, sid, "github_releases is no longer filtered out")

	fixture, err := os.ReadFile("../../changelog/testdata/github_releases/rectangle.json")
	require.NoError(t, err)
	status, etag, requests := http.StatusOK, `W/"r1"`, 0
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/repos/rxhanson/Rectangle" {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"private":false,"visibility":"public"}`))}, nil
		}
		requests++
		require.Equal(t, "/repos/rxhanson/Rectangle/releases", r.URL.Path)
		if requests > 1 {
			require.Equal(t, `W/"r1"`, r.Header.Get("If-None-Match"))
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Etag": {etag}}, Body: io.NopCloser(strings.NewReader(string(fixture)))}, nil
	})}
	now := time.Now().UTC()
	svc = &changelog.Service{Store: st, Fetcher: fetcher, Now: func() time.Time { return now }}
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 5, stats["releases"])
	require.Equal(t, true, stats["changed"])
	var rows []struct {
		SourceKey, Version string
		BodyMarkdown       *string
		SourceURL          string
		IsPrerelease       bool
	}
	require.NoError(t, st.DB.Raw("SELECT source_key,version,body_markdown,source_url,is_prerelease FROM releases WHERE package_id=? AND source='github_release' ORDER BY published_at DESC", pid).Scan(&rows).Error)
	require.Len(t, rows, 5)
	require.Equal(t, "v2.0.4", rows[0].SourceKey)
	require.Equal(t, "2.0.4", rows[0].Version)
	require.Equal(t, "v2.0", rows[4].SourceKey)
	require.Equal(t, "2.0.0", rows[4].Version, "tag v2.0 joins the package version 2.0.0")
	for _, row := range rows {
		require.NotNil(t, row.BodyMarkdown)
		require.NotEmpty(t, *row.BodyMarkdown)
		require.True(t, strings.HasPrefix(row.SourceURL, "https://github.com/rxhanson/Rectangle/releases/"))
	}

	// 304: no extraction, ETag kept, nothing rewritten.
	status = http.StatusNotModified
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, true, stats["notModified"])
	require.Equal(t, 0, stats["releases"])
	require.Equal(t, false, stats["changed"])
	var state struct {
		LastStatus string
		ETag       string `gorm:"column:etag"`
	}
	require.NoError(t, st.DB.Raw("SELECT last_status,etag FROM changelog_sources WHERE id=?", sid).Scan(&state).Error)
	require.Equal(t, "not_modified", state.LastStatus)
	require.Equal(t, `W/"r1"`, state.ETag)

	// An editor's hidden flag and title survive a refresh; identical notes are not rewritten.
	require.NoError(t, st.DB.Exec("UPDATE releases SET hidden=true,title='Edited' WHERE package_id=? AND source_key='v2.0.4'", pid).Error)
	status, etag = http.StatusOK, `W/"r1"`
	stats, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, false, stats["changed"])
	var edited struct {
		Hidden bool
		Title  string
	}
	require.NoError(t, st.DB.Raw("SELECT hidden,title FROM releases WHERE package_id=? AND source_key='v2.0.4'", pid).Scan(&edited).Error)
	require.True(t, edited.Hidden)
	require.Equal(t, "Edited", edited.Title)

	// Homebrew commit history keeps its own source alongside github_releases.
	var homebrewSources int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM changelog_sources WHERE package_id=? AND type='homebrew_commits'", pid).Scan(&homebrewSources).Error)
	require.EqualValues(t, 1, homebrewSources)

	// The repository turns private: the facts job clears the subject. The auto source is no longer due
	// and cannot be fetched, so no request (with the token) is made; a manual source stays readable.
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE package_source_facts SET subject='',status='none' WHERE package_id=? AND source='github'", pid).Error)
	due, err = st.ChangelogDue(ctx, time.Now().Add(30*24*time.Hour))
	require.NoError(t, err)
	require.NotContains(t, due, sid)
	before := requests
	_, err = svc.Fetch(ctx, sid)
	require.Error(t, err)
	require.Equal(t, before, requests, "no request for a repository no longer verified public")
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET resolved_by='manual' WHERE id=?", sid).Error)
	due, err = st.ChangelogDue(ctx, time.Now().Add(30*24*time.Hour))
	require.NoError(t, err)
	require.Contains(t, due, sid, "manual sources are not gated")
}

func TestGitHubReleaseFetchStoresNothingWhenVisibilityFails(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"gated","version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, sid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='gated'").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data)
 VALUES(?,'github','homepage','owner/gated','found','{"repo":"owner/gated"}')`, pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by)
 VALUES(?,'github_releases','{"repo":"owner/gated"}',20,'auto') RETURNING id`, pid).Scan(&sid).Error)

	for _, testCase := range []struct {
		name    string
		status  int
		headers map[string]string
	}{
		{name: "rate limited", status: http.StatusForbidden, headers: map[string]string{"X-Ratelimit-Remaining": "0"}},
		{name: "missing", status: http.StatusNotFound},
		{name: "server error", status: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fetcher := pipeline.NewFetcher("test")
			fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/releases") {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"tag_name":"v1.0.0","body":"private notes"}]`))}, nil
				}
				headers := http.Header{}
				for key, value := range testCase.headers {
					headers.Set(key, value)
				}
				return &http.Response{StatusCode: testCase.status, Header: headers, Body: io.NopCloser(strings.NewReader("no visibility"))}, nil
			})}
			svc := &changelog.Service{Store: st, Fetcher: fetcher}
			_, err := svc.Fetch(ctx, sid)
			require.Error(t, err)
			var count int64
			require.NoError(t, st.DB.WithContext(ctx).Table("releases").Where("package_id=?", pid).Count(&count).Error)
			require.Zero(t, count, "an ungated visibility read must not store a release")
		})
	}
}
