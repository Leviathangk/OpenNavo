//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Link apps record catalog events like casks: added, a version never shipped before, removed. Editing a
// link's definition is not an event.
func TestLinkAppsRecordCatalogEvents(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	repo := "owner/example"
	homepage := "https://example.com/app"
	input := domain.LinkInput{Token: "event-link", Name: "Event Link", Homepage: &homepage, GithubRepo: &repo, SourceLocale: "en-US"}
	write := func(id int64, in domain.LinkInput) int64 {
		t.Helper()
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			id, err = (&store.Store{DB: tx}).WriteLinkApp(ctx, id, in)
			return err
		}))
		return id
	}
	type event struct {
		Op                         string
		EventType, Version, Before *string
	}
	events := func(id int64) []event {
		t.Helper()
		var rows []event
		require.NoError(t, st.DB.Raw("SELECT op,event_type,version,previous_version AS before FROM catalog_changes WHERE package_id=? AND reason='link' ORDER BY seq", id).Scan(&rows).Error)
		return rows
	}
	text := func(v string) *string { return &v }
	id := write(0, input)
	require.Equal(t, []event{{Op: "upsert", EventType: text("created")}}, events(id))

	input.Name = "Event Link Renamed"
	write(id, input)
	require.Nil(t, events(id)[1].EventType, "a definition edit is not a catalog event")

	sync := func(version string, published time.Time) {
		t.Helper()
		before, err := st.LinkApp(ctx, id)
		require.NoError(t, err)
		release := &domain.GithubRelease{Tag: "v" + version, Version: version, PublishedAt: published, URL: "https://github.com/owner/example/releases/tag/v" + version}
		require.NoError(t, st.LinkSyncResult(ctx, *before, release, "github_release", "Notes", "", published, nil))
	}
	now := time.Now().UTC().Truncate(time.Second)
	sync("2.0", now.Add(-2*time.Hour))
	sync("2.1", now.Add(-time.Hour))
	sync("2.1", now.Add(-time.Hour)) // unchanged release: nothing new
	sync("2.0", now)                 // a version shipped before is not an update
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return (&store.Store{DB: tx}).RemoveLinkApp(ctx, id) }))
	got := events(id)
	require.Len(t, got, 6)
	require.Equal(t, event{Op: "upsert", EventType: text("updated"), Version: text("2.0")}, got[2], "the first version a link finds is an update without a previous one")
	require.Equal(t, event{Op: "upsert", EventType: text("updated"), Version: text("2.1"), Before: text("2.0")}, got[3])
	require.Equal(t, event{Op: "upsert", Version: text("2.0"), Before: text("2.1")}, got[4])
	require.Equal(t, event{Op: "delete", EventType: text("removed"), Version: text("2.0")}, got[5])
}
