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

func TestVersionsOutboxRechecksNotesBeforeAnnouncement(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en)
 VALUES(903,'cask','versions-outbox','versions-outbox','homebrew/cask','Fixture','1','1','{}','fixture','Useful fixture')`).Error)
	n := &Notifier{Store: st, Config: config.Config{WebBaseURL: "https://site.example.test"}, Batch: 10, Now: func() time.Time { return now }}
	for _, state := range []string{"invalidated", "probed"} {
		t.Run(state, func(t *testing.T) {
			note := store.URLNotification{URL: "https://site.example.test/apps/versions-outbox/versions", Path: "/apps/versions-outbox/versions", Locale: "en-US", Reason: "updated", Announce: true, Robots: "index, follow"}
			require.NoError(t, store.QueueURLNotification(ctx, st.DB, note))
			require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state=?,next_attempt_at=? WHERE url=? AND state<>'announced'", state, now, note.URL).Error)
			rows, err := st.ClaimURLNotifications(ctx, "fixture-owner", now, 1, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			settings, err := st.SEOSettings(ctx)
			require.NoError(t, err)
			keep, err := n.retainAnnouncement(ctx, rows[0], settings)
			require.NoError(t, err)
			require.False(t, keep, "stale index decision must not announce a versions page without sufficient notes")
			var saved store.URLNotificationRow
			require.NoError(t, st.DB.Raw("SELECT * FROM url_notify_outbox WHERE id=?", rows[0].ID).Scan(&saved).Error)
			require.Equal(t, "announced", saved.State)
			require.False(t, saved.Announce)
			require.Equal(t, "noindex, follow", saved.Robots)
		})
	}
}
