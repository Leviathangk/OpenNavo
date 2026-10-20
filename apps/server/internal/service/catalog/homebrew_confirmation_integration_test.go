//go:build integration

package catalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCatalogConfirmationKeepsUnchangedFactsVerified(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-facts.StaleAfter - 24*time.Hour)
	raw := `{"token":"confirmed-cask","name":["Confirmed"],"version":"1","supported_platforms":["arm64_sonoma"],"depends_on":{"macos":{">=":["14"]}},"artifacts":[{"uninstall":[{"launchctl":"org.example.confirmed"}]},{"zap":[{"trash":"~/Library/Confirmed"}]}]}`
	p, err := homebrew.Normalize("cask", json.RawMessage(raw))
	require.NoError(t, err)
	_, err = st.ApplyCatalogBatchWithLifecycle(ctx, []store.CatalogMutation{{Package: p}}, old)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE packages SET updated_at=? WHERE token=?", old, p.Token).Error)
	row, err := st.CatalogRow(ctx, "cask", p.Token)
	require.NoError(t, err)
	public := &PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	assertFacts := func(status string, checked time.Time) {
		t.Helper()
		detail, err := public.Detail(ctx, "cask", p.Token, "en-US")
		require.NoError(t, err)
		require.NotEmpty(t, detail.Facts.Requirements)
		require.NotEmpty(t, detail.Facts.Uninstall)
		for _, requirement := range detail.Facts.Requirements {
			require.Equal(t, status, string(requirement.Evidence.Status))
			require.WithinDuration(t, checked, *requirement.Evidence.CheckedAt, time.Microsecond)
			require.WithinDuration(t, checked.Add(facts.StaleAfter), *requirement.Evidence.ValidUntil, time.Microsecond)
		}
		for _, entry := range detail.Facts.Uninstall {
			require.Equal(t, status, string(entry.Evidence.Status))
			require.WithinDuration(t, checked, *entry.Evidence.CheckedAt, time.Microsecond)
		}
		compatibility, err := st.VersionRequirements(ctx, row.ID, "1", now)
		require.NoError(t, err)
		require.NotEmpty(t, compatibility)
		for _, r := range compatibility {
			require.Equal(t, status, r.Evidence.Status)
			require.WithinDuration(t, checked, *r.Evidence.CheckedAt, time.Microsecond)
		}
		owners, err := st.BackgroundOwners(ctx, "org.example.confirmed")
		require.NoError(t, err)
		require.Len(t, owners, 1)
		require.WithinDuration(t, checked, owners[0].CheckedAt, time.Microsecond)
	}
	assertFacts("stale", old)
	var before struct {
		UpdatedAt, VersionChangedAt time.Time
	}
	require.NoError(t, st.DB.Raw("SELECT updated_at,version_changed_at FROM packages WHERE id=?", row.ID).Scan(&before).Error)
	var changesBefore, changesAfter int64
	require.NoError(t, st.DB.Table("catalog_changes").Count(&changesBefore).Error)
	require.NoError(t, st.DB.Exec("DELETE FROM package_page_dirty").Error)
	source := &testSource{Data: map[string][]string{"cask": {raw}}, Previous: map[string]homebrew.Conditional{}}
	sync := &Service{Store: st, Source: source, Queue: &testQueue{}, Now: func() time.Time { return now }}
	// Exercise the real cache invalidation outbox through local Redis pub/sub.
	subscription := st.Redis.Subscribe(ctx, "cache:invalidate")
	t.Cleanup(func() { require.NoError(t, subscription.Close()) })
	_, err = subscription.Receive(ctx)
	require.NoError(t, err)
	stats, err := sync.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["kinds"].([]Stats)[0].Unchanged)
	require.Zero(t, stats["kinds"].([]Stats)[0].Updated)
	messageCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	message, err := subscription.ReceiveMessage(messageCtx)
	require.NoError(t, err)
	var invalidation struct{ Patterns []string }
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &invalidation))
	require.Contains(t, invalidation.Patterns, "c:pkg:*")
	require.NoError(t, cache.Invalidate(ctx, st.Redis, invalidation.Patterns))
	assertFacts("verified", now)
	var after struct {
		UpdatedAt, VersionChangedAt time.Time
	}
	require.NoError(t, st.DB.Raw("SELECT updated_at,version_changed_at FROM packages WHERE id=?", row.ID).Scan(&after).Error)
	require.Equal(t, before, after)
	require.NoError(t, st.DB.Table("catalog_changes").Count(&changesAfter).Error)
	require.Equal(t, changesBefore, changesAfter)
	var dirty int64
	require.NoError(t, st.DB.Table("package_page_dirty").Count(&dirty).Error)
	require.Zero(t, dirty)

	now = now.Add(24 * time.Hour)
	source.NotModified = true
	stats, err = sync.Run(ctx)
	require.NoError(t, err)
	require.True(t, stats["kinds"].([]Stats)[0].NotModified)
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{"c:pkg:*"}))
	assertFacts("verified", now)
	lastSuccess := now
	now = now.Add(time.Hour)
	source.NotModified, source.Fail = false, true
	_, err = sync.Run(ctx)
	require.ErrorContains(t, err, "interrupted download")
	assertFacts("verified", lastSuccess)
	// Local normalization and stale/mismatched observations are not new evidence.
	_, err = st.ApplyCatalogBatchWithLifecycle(ctx, []store.CatalogMutation{{Package: p, Previous: row, LocalOnly: true}}, now)
	require.NoError(t, err)
	require.NoError(t, st.ConfirmHomebrewDefinitions(ctx, []store.HomebrewConfirmation{{Token: p.Token, Tap: p.Tap, RawHash: "different-definition"}}, now))
	now = lastSuccess.Add(facts.StaleAfter + time.Hour)
	assertFacts("stale", lastSuccess)
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=? WHERE id=?", now, row.ID).Error)
	require.NoError(t, st.ConfirmHomebrewDefinitions(ctx, []store.HomebrewConfirmation{{Token: p.Token, Tap: p.Tap, RawHash: p.RawHash}}, now))
	var confirmed time.Time
	require.NoError(t, st.DB.Raw("SELECT homebrew_checked_at FROM packages WHERE id=?", row.ID).Scan(&confirmed).Error)
	require.WithinDuration(t, lastSuccess, confirmed, time.Microsecond)
}

func TestHomebrewConfirmationKeepsHistoricalVersionObservation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	first := time.Now().UTC().Truncate(time.Second).Add(-48 * time.Hour)
	for _, version := range []string{"1", "2"} {
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"version-confirmation","name":["Version"],"version":"`+version+`","depends_on":{"macos":{">=":["14"]}}}`))
		require.NoError(t, err)
		previous, err := st.CatalogRow(ctx, "cask", p.Token)
		require.NoError(t, err)
		at := first
		if version == "2" {
			at = first.Add(24 * time.Hour)
		}
		_, err = st.ApplyCatalogBatchWithLifecycle(ctx, []store.CatalogMutation{{Package: p, Previous: previous}}, at)
		require.NoError(t, err)
	}
	row, err := st.CatalogRow(ctx, "cask", "version-confirmation")
	require.NoError(t, err)
	for version, at := range map[string]time.Time{"1": first, "2": first.Add(24 * time.Hour)} {
		requirements, err := st.VersionRequirements(ctx, row.ID, version, time.Now())
		require.NoError(t, err)
		require.NotEmpty(t, requirements)
		require.WithinDuration(t, at, *requirements[0].Evidence.CheckedAt, time.Microsecond)
	}
}

func TestHomebrewConfirmationInvalidatesOnlyExpiringProof(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"confirmation-expiry","name":["Fixture"],"version":"1"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	for _, tc := range []struct {
		name string
		age  time.Duration
		want int64
	}{
		{"unknown", 0, 1},
		{"stale", facts.StaleAfter + time.Hour, 1},
		{"expires_during_cache_lifetime", facts.StaleAfter - cache.PackageDetailTTL/2, 1},
		{"fresh", 24 * time.Hour, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, st.DB.Exec("DELETE FROM job_outbox WHERE job_type='cache:invalidate'").Error)
			var previous *time.Time
			if tc.age > 0 {
				at := now.Add(-tc.age)
				previous = &at
			}
			require.NoError(t, st.DB.Exec("UPDATE packages SET homebrew_checked_at=? WHERE token=?", previous, p.Token).Error)
			require.NoError(t, st.ConfirmHomebrewDefinitions(ctx, []store.HomebrewConfirmation{{Token: p.Token, Tap: p.Tap, RawHash: p.RawHash}}, now))
			var count int64
			require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&count).Error)
			require.Equal(t, tc.want, count)
			if tc.want > 0 {
				var patterns []string
				require.NoError(t, st.DB.Raw("SELECT jsonb_array_elements_text(payload) FROM job_outbox WHERE job_type='cache:invalidate'").Scan(&patterns).Error)
				require.Equal(t, []string{"c:pkg:*"}, patterns, "confirmation alone must not evict catalog lists")
			}
			var checked time.Time
			require.NoError(t, st.DB.Raw("SELECT homebrew_checked_at FROM packages WHERE token=?", p.Token).Scan(&checked).Error)
			require.True(t, now.Equal(checked), "fresh confirmations still advance even without invalidation")
		})
	}
}
