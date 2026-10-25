//go:build integration

package urlnotify

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestClosedLocalesAreNotAnnounced(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, st.DB.Exec(`
 INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) OVERRIDING SYSTEM VALUE VALUES(9201,'closed-fixture','published',9201,9201,now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type) OVERRIDING SYSTEM VALUE VALUES(9201,9201,1,'en-US','news');
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status) VALUES(9201,'en-US','Fixture article','en-US','source'),(9201,'de-DE','Artikel','en-US','machine');
 UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US","zh-CN","ja-JP","ru-RU","es-ES"]}'::jsonb)`).Error)
	const base = "https://site.example.test"
	n := &Notifier{Store: st, Config: config.Config{WebBaseURL: base}, Batch: 10, Now: func() time.Time { return now }}
	paths := []string{"/articles/closed-fixture", "/de/articles/closed-fixture", "/collections/editors", "/de/collections/editors", "/pt/collections/editors"}
	announced := func() map[string]bool {
		t.Helper()
		plan := Plan{Paths: paths}
		for _, path := range paths {
			plan.IndexNowURLs = append(plan.IndexNowURLs, base+path)
		}
		notes, err := n.notifications(ctx, plan)
		require.NoError(t, err)
		out := map[string]bool{}
		for _, note := range notes {
			out[note.Path] = note.Announce
		}
		return out
	}
	// The German article text is ready, but German is closed; so are the lists in closed languages.
	require.Equal(t, map[string]bool{"/articles/closed-fixture": true, "/de/articles/closed-fixture": false, "/collections/editors": true,
		"/de/collections/editors": false, "/pt/collections/editors": false}, announced())

	// Jobs queued before a language closed are suppressed at delivery; retired URLs still announce.
	for _, note := range []store.URLNotification{
		{URL: base + "/de/apps/closed-app", Path: "/de/apps/closed-app", Locale: "de-DE", Reason: "updated", Announce: true, Robots: "index, follow"},
		{URL: base + "/de/articles/closed-fixture", Path: "/de/articles/closed-fixture", Locale: "de-DE", Reason: "updated", Announce: true, Robots: "index, follow"},
		{URL: base + "/de/collections/editors", Path: "/de/collections/editors", Locale: "de-DE", Reason: "updated", Announce: true},
		{URL: base + "/de/apps/removed-app", Path: "/de/apps/removed-app", Locale: "de-DE", Reason: "updated", Announce: true},
		{URL: base + "/apps/opennavo", Path: "/apps/opennavo", Locale: "en-US", Reason: "updated", Announce: true, Robots: "index, follow"},
		{URL: base + "/apps/closed-app", Path: "/apps/closed-app", Locale: "en-US", Reason: "updated", Announce: true, Robots: "index, follow"},
	} {
		require.NoError(t, store.QueueURLNotification(ctx, st.DB, note))
	}
	require.NoError(t, st.DB.Exec(`UPDATE url_notify_outbox SET state='probed',next_attempt_at=?`, now).Error)
	rows, err := st.ClaimURLNotifications(ctx, "fixture-owner", now, 10)
	require.NoError(t, err)
	require.Len(t, rows, 6)
	settings, err := st.SEOSettings(ctx)
	require.NoError(t, err)
	for _, row := range rows {
		keep, err := n.retainAnnouncement(ctx, row, settings)
		require.NoError(t, err)
		require.Equal(t, row.Path == "/de/apps/removed-app" || row.Path == "/apps/closed-app", keep, row.Path)
		var persisted store.URLNotificationRow
		require.NoError(t, st.DB.Raw("SELECT * FROM url_notify_outbox WHERE id=?", row.ID).Scan(&persisted).Error)
		if !keep {
			require.Equal(t, "announced", persisted.State, row.Path)
			require.False(t, persisted.Announce, row.Path)
			require.Equal(t, "noindex, follow", persisted.Robots, row.Path)
		}
	}

	// Without a list every site language is open.
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages,openLocales}','null'::jsonb)`).Error)
	for path, announce := range announced() {
		require.True(t, announce, path)
	}
}
