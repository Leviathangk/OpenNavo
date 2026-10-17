//go:build integration

package updatefeeds

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRulesDryRunValidationETagAndDisableSurviveAutomaticImport(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	admin := &management.Service{Store: st}
	input := management.Input{Body: map[string]any{"bundleId": "com.example.App", "source": "sparkle", "feedUrl": "https://example.org/feed.xml", "channel": "stable"}}
	preview := domain.WithOperation(ctx, &domain.Operation{DryRun: true, RequestID: "feed-preview"})
	_, err := admin.Execute(preview, "UpsertUpdateFeed", input)
	require.NoError(t, err)
	rows, err := st.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Empty(t, rows)
	var audits int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM audit_logs").Scan(&audits).Error)
	require.Zero(t, audits)
	_, err = admin.Execute(ctx, "UpsertUpdateFeed", input)
	require.NoError(t, err)
	catalog := &snapshot.Service{Store: st}
	before, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Empty(t, before.Items)
	archive := testArchive(t)
	requests := 0
	notModified := false
	broken := false
	service := &Service{Store: st, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var data string
		status := 200
		headers := http.Header{}
		if r.URL.Host == "codeload.github.com" {
			data = string(archive)
		} else {
			require.Equal(t, "https://example.org/feed.xml", r.URL.String())
			requests++
			data = fixtureFeed
			headers.Set("ETag", `"v2"`)
			if broken {
				data = "<rss/>"
			}
			if notModified && !broken {
				require.Equal(t, `"v2"`, r.Header.Get("If-None-Match"))
				status = 304
				data = ""
			}
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	stats, err := service.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats["failed"])
	require.Equal(t, 1, requests)
	after, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Len(t, after.Items, 1)
	require.NotEqual(t, before.ETag, after.ETag)
	notModified = true
	_, err = service.Run(ctx)
	require.NoError(t, err)
	again, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Equal(t, after, again)
	broken = true
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["failed"])
	hidden, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Empty(t, hidden.Items)
	broken = false
	_, err = service.Run(ctx)
	require.NoError(t, err)
	recovered, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Equal(t, after, recovered)
	_, err = admin.Execute(preview, "DisableUpdateFeed", input)
	require.NoError(t, err)
	same, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Equal(t, after, same)
	_, err = admin.Execute(ctx, "DisableUpdateFeed", input)
	require.NoError(t, err)
	disabled, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Empty(t, disabled.Items)
	key := domain.UpdateFeedKey{BundleID: "com.example.App", Source: "sparkle"}
	row, err := st.UpdateFeed(ctx, key)
	require.NoError(t, err)
	require.NoError(t, st.WriteUpdateFeed(ctx, row.UpdateFeedInput, "homebrew"))
	row, err = st.UpdateFeed(ctx, key)
	require.NoError(t, err)
	require.False(t, row.Enabled)
	require.Equal(t, "manual", row.Origin)
	// A delayed successful response from an edited URL cannot mark the replacement verified.
	old := *row
	input.Body["feedUrl"] = "https://example.org/next.xml"
	_, err = admin.Execute(ctx, "UpsertUpdateFeed", input)
	require.NoError(t, err)
	require.NoError(t, st.CheckedUpdateFeed(ctx, old, `"stale"`, nil))
	row, err = st.UpdateFeed(ctx, key)
	require.NoError(t, err)
	require.Nil(t, row.VerifiedAt)
}

func TestM4VendorSuspensionRecoveryAndLinkGithubAutoImport(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	input := domain.UpdateFeedInput{UpdateFeedKey: domain.UpdateFeedKey{BundleID: "test.vendor.App", Source: "vendor"}, FeedURL: "https://vendor.example.org/current.json", TeamID: ptr("TESTTEAM01"), Resolver: &domain.VendorResolver{Mode: "json", VersionPath: "version", DownloadPath: "url", SHA256Path: "sha256"}}
	require.NoError(t, st.WriteUpdateFeed(ctx, input, "manual"))
	link := domain.LinkInput{Token: "m4-app", Name: "M4 App", GithubRepo: ptr("example/app"), BundleIDs: []string{"test.example.App"}, TeamID: ptr("TESTTEAM01"), Pricing: "free"}
	require.NoError(t, link.Validate())
	_, err := st.WriteLinkApp(ctx, 0, link)
	require.NoError(t, err)
	bad := false
	github := &githubReader{t: t}
	service := &Service{Store: st, GitHub: github, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		data := string(testArchive(t))
		if r.URL.Host == "vendor.example.org" {
			data = `{"version":"2.0","url":"https://vendor.example.org/App.dmg","sha256":"` + strings.Repeat("a", 64) + `"}`
			if bad {
				data = `{"version":"2.0","url":"http://localhost/App.dmg"}`
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	stats, err := service.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats["failed"])
	catalog := &snapshot.Service{Store: st}
	first, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	bad = true
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["failed"])
	hidden, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Len(t, hidden.Items, 1)
	require.Equal(t, "github", hidden.Items[0].Source)
	bad = false
	_, err = service.Run(ctx)
	require.NoError(t, err)
	recovered, err := catalog.UpdateFeeds(ctx)
	require.NoError(t, err)
	require.Equal(t, first, recovered)
	old, err := st.UpdateFeed(ctx, input.UpdateFeedKey)
	require.NoError(t, err)
	input.Resolver.VersionPath = "otherVersion"
	require.NoError(t, st.WriteUpdateFeed(ctx, input, "manual"))
	require.NoError(t, st.CheckedResolvedUpdateFeed(ctx, *old, "", "", old.Release, nil))
	changed, err := st.UpdateFeed(ctx, input.UpdateFeedKey)
	require.NoError(t, err)
	require.Nil(t, changed.VerifiedAt)
	require.NoError(t, st.DisableUpdateFeed(ctx, domain.UpdateFeedKey{BundleID: "test.example.App", Source: "github"}))
	_, err = service.Run(ctx)
	require.NoError(t, err)
	disabled, err := st.UpdateFeed(ctx, domain.UpdateFeedKey{BundleID: "test.example.App", Source: "github"})
	require.NoError(t, err)
	require.False(t, disabled.Enabled)
}
