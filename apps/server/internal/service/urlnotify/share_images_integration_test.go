//go:build integration

package urlnotify_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/stretchr/testify/require"
)

func shareImageFixture(t *testing.T, f feed, id int64, token string) {
	t.Helper()
	require.NoError(t, f.st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
VALUES(?, 'cask', ?, ?, 'homebrew/cask', 'Share image fixture', '1', '1', '{}', 'fixture')`, id, token, token).Error)
	require.NoError(t, f.st.DB.Exec("INSERT INTO package_meta(package_id) VALUES(?)", id).Error)
}

func TestVisibilityChangeQueuesShareImagePurge(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	_, err := f.run(ctx)
	require.NoError(t, err)
	shareImageFixture(t, f, 1995, "share-image-app")
	svc := &management.Service{Store: f.st, Config: config.Config{WebBaseURL: "https://opennavo.example"}, InvalidateShareImages: func(ctx context.Context, token string) error {
		return f.notifier.QueueShareImagePurge(ctx, token)
	}}
	_, err = svc.Execute(ctx, "UpdatePackageMeta", management.Input{ID: 1995, Body: map[string]any{"private": true, "visibilityReason": "Visibility fixture"}})
	require.NoError(t, err)

	var private bool
	require.NoError(t, f.st.DB.Raw("SELECT private FROM package_meta WHERE package_id=1995").Scan(&private).Error)
	require.True(t, private)
	prefix := "opennavo.example/_og/"
	require.Contains(t, queuedPurges(t, f.st), prefix)

	var queued int64
	require.NoError(t, f.st.DB.Table("url_notify_outbox").Where("purge_prefix=? AND state='pending'", prefix).Count(&queued).Error)
	require.EqualValues(t, len(i18n.SiteLocales), queued, "each site language has an app path to invalidate")

	_, err = f.run(ctx)
	require.NoError(t, err)
	require.Empty(t, queuedPurges(t, f.st), "the durable purge queue drains after success")

	foundPurge := false
	for _, request := range f.cloudflare.requests {
		for _, value := range request["prefixes"].([]any) {
			if value == prefix {
				foundPurge = true
			}
		}
	}
	require.True(t, foundPurge, "Cloudflare receives the share-image prefix")

	foundInvalidation := false
	for _, request := range f.web.requests {
		if request["og"] != true {
			continue
		}
		if paths, ok := request["paths"].([]any); ok {
			for _, value := range paths {
				if strings.HasSuffix(value.(string), "/apps/share-image-app") {
					foundInvalidation = true
				}
			}
		}
	}
	require.True(t, foundInvalidation, "the web receives og:true for the app paths")
}

func TestShareImagePurgeFailureDoesNotBlockVisibilityChange(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	_, err := f.run(ctx)
	require.NoError(t, err)
	shareImageFixture(t, f, 1996, "share-image-failure")
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })
	f.notifier.Logger = logger
	svc := &management.Service{Store: f.st, Config: config.Config{WebBaseURL: "https://opennavo.example"}, InvalidateShareImages: func(ctx context.Context, token string) error {
		return f.notifier.QueueShareImagePurge(ctx, token)
	}}
	_, err = svc.Execute(ctx, "UpdatePackageMeta", management.Input{ID: 1996, Body: map[string]any{"private": true, "visibilityReason": "Visibility fixture"}})
	require.NoError(t, err, "the visibility write commits before delivery")

	prefix := "opennavo.example/_og/"
	f.web.status, f.cloudflare.status = 500, 500
	_, err = f.run(ctx)
	require.Error(t, err)
	require.Contains(t, logged.String(), "queued URL purge not completed")
	require.Contains(t, logged.String(), "web cache invalidation failed")
	require.Contains(t, queuedPurges(t, f.st), prefix, "the CDN purge waits for retry")
	var state string
	var attempts int
	require.NoError(t, f.st.DB.Raw("SELECT state,attempts FROM url_notify_outbox WHERE purge_prefix=? ORDER BY id DESC LIMIT 1", prefix).Row().Scan(&state, &attempts))
	require.Equal(t, "pending", state, "the origin invalidation waits for retry")
	require.Positive(t, attempts)
	var private bool
	require.NoError(t, f.st.DB.Raw("SELECT private FROM package_meta WHERE package_id=1996").Scan(&private).Error)
	require.True(t, private)

	// A hook failure is logged too, but the committed visibility change still succeeds.
	failing := &management.Service{Store: f.st, InvalidateShareImages: func(context.Context, string) error {
		return errors.New("share image endpoint unavailable")
	}}
	_, err = failing.Execute(ctx, "UpdatePackageMeta", management.Input{ID: 1996, Body: map[string]any{"hidden": true, "visibilityReason": "Visibility fixture"}})
	require.NoError(t, err)
	require.Contains(t, logged.String(), "share image invalidation failed")

	f.web.status, f.cloudflare.status = 200, 200
	for attempt := 0; attempt < 4 && state != "announced"; attempt++ {
		_, err = f.run(ctx)
		require.NoError(t, err)
		require.NoError(t, f.st.DB.Raw("SELECT state FROM url_notify_outbox WHERE purge_prefix=? ORDER BY id DESC LIMIT 1", prefix).Scan(&state).Error)
	}
	require.Empty(t, queuedPurges(t, f.st))
	require.Equal(t, "announced", state, "the retry completes the queued origin invalidation")
}
