//go:build integration

package urlnotify

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTroubleshootingOutboxRechecksEvidenceAfterProbe(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en)
 VALUES(902,'cask','issue-outbox','issue-outbox','homebrew/cask','Fixture','1','1','{}','fixture','Useful fixture')`).Error)
	proof, _ := json.Marshal(facts.NewProof("https://vendor.example.test/help", now.Add(-time.Minute), time.Hour, "verified", now))
	require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence)
 SELECT 902,'issue','issue-'||n,'{"locale":"en-US","title":"Fixture issue","symptom":"Import stops","appVersions":["1"],"macosVersions":["15.0"],"steps":["Restart"],"communityLinks":[]}'::jsonb,?::jsonb FROM generate_series(1,3)n`, string(proof)).Error)
	n := &Notifier{Store: st, Config: config.Config{WebBaseURL: "https://site.example.test"}, Batch: 10, Now: func() time.Time { return now }}
	note := store.URLNotification{URL: "https://site.example.test/apps/issue-outbox/troubleshooting", Path: "/apps/issue-outbox/troubleshooting", Locale: "en-US", Reason: "updated", Announce: true, Robots: "index, follow"}
	plan, err := n.notifications(ctx, Plan{Paths: []string{"/apps/issue-outbox"}, IndexNowURLs: []string{note.URL}})
	require.NoError(t, err)
	require.Equal(t, "en-US", plan[1].Locale, "subpage inherits locale from parent")
	require.NoError(t, store.QueueURLNotification(ctx, st.DB, note))
	require.NoError(t, st.DB.Exec(`UPDATE url_notify_outbox SET state='probed',next_attempt_at=?`, now).Error)
	rows, err := st.ClaimURLNotifications(ctx, "fixture-owner", now, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	keep, err := n.retainAnnouncement(ctx, rows[0])
	require.NoError(t, err)
	require.True(t, keep)
	now = now.Add(2 * time.Hour)
	// Exercise the real drain after a restart with an expired lease. The probed
	// task must be suppressed without calling IndexNow or probing the page again.
	purged, announced, err := n.drainOutbox(ctx, "fixture-owner-next")
	require.NoError(t, err)
	require.Zero(t, purged)
	require.Zero(t, announced)
	var persisted store.URLNotificationRow
	require.NoError(t, st.DB.Raw("SELECT * FROM url_notify_outbox WHERE id=?", rows[0].ID).Scan(&persisted).Error)
	require.Equal(t, "announced", persisted.State)
	require.False(t, persisted.Announce)
	require.Equal(t, "/apps/issue-outbox", persisted.CanonicalPath)
}
