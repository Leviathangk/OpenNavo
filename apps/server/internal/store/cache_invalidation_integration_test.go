//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestWorkerOutboxRecoversInvalidationWithoutRerunningSync(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	redis := st.Redis
	defer func() { st.Redis = redis }()
	st.Redis = testutil.RedisWithoutPublish(t, redis)
	deliver := func(context.Context, string, json.RawMessage) error {
		t.Fatal("cache notifications must not be enqueued as jobs")
		return nil
	}
	_, err = st.DispatchOutbox(ctx, deliver)
	require.ErrorContains(t, err, "publish cache invalidations")
	var count int64
	require.NoError(t, st.DB.Table("job_outbox").Count(&count).Error)
	require.EqualValues(t, 1, count)

	st.Redis = redis
	messages := redis.Subscribe(ctx, "cache:invalidate")
	defer func() { require.NoError(t, messages.Close()) }()
	_, err = messages.Receive(ctx)
	require.NoError(t, err)
	delivered, err := st.DispatchOutbox(ctx, deliver)
	require.NoError(t, err)
	require.Zero(t, delivered, "cache notifications do not inflate the enqueued job count")
	messageCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	message, err := messages.ReceiveMessage(messageCtx)
	require.NoError(t, err)
	var event struct {
		Patterns []string `json:"patterns"`
	}
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &event))
	require.Contains(t, event.Patterns, "c:pkg:*")
	require.Contains(t, event.Patterns, "c:rank:*")
	// Browse pages stay and serve while a rebuild replaces them; only their fresh marks go.
	require.Contains(t, event.Patterns, store.BrowseRefreshPattern)
	require.NotContains(t, event.Patterns, "c:browse:*")
	require.NoError(t, st.DB.Table("job_outbox").Count(&count).Error)
	require.Zero(t, count)
}

func TestCatalogAndChangelogRollBackWhenInvalidationCannotBePersisted(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	require.NoError(t, st.DispatchCacheInvalidations(ctx))
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	previous := rows["sample"]
	var sid int64
	require.NoError(t, st.DB.Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{}',90,'auto') RETURNING id`, previous.ID).Scan(&sid).Error)
	require.NoError(t, st.DB.Exec(`ALTER TABLE job_outbox ADD CONSTRAINT reject_cache CHECK (job_type<>'cache:invalidate')`).Error)

	p, err = homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"2"}`))
	require.NoError(t, err)
	require.Error(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p, Previous: previous}}))
	var version string
	require.NoError(t, st.DB.Raw("SELECT version FROM packages WHERE id=?", previous.ID).Scan(&version).Error)
	require.Equal(t, "1", version)
	require.Error(t, st.RemoveCatalogRows(ctx, []*store.CatalogRow{previous}))
	var active int64
	require.NoError(t, st.DB.Table("packages").Where("id=? AND removed_at IS NULL", previous.ID).Count(&active).Error)
	require.EqualValues(t, 1, active)

	now := time.Now().UTC()
	changed, err := st.SaveFetchedChangelog(ctx, store.ChangelogState{ID: sid, PackageID: previous.ID}, changelog.FetchResult{Versions: []changelog.BrewVersion{{Version: "0.9", SHA: "fixture", CommittedAt: now}}}, now.Add(time.Hour), now)
	require.Error(t, err)
	require.False(t, changed)
	var count int64
	require.NoError(t, st.DB.Table("package_versions").Where("package_id=?", previous.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("changelog_sources").Where("id=? AND last_fetched_at IS NULL", sid).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("job_outbox").Count(&count).Error)
	require.Zero(t, count, "downstream jobs roll back with the catalog update too")
}
