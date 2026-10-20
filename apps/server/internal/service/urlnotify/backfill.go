package urlnotify

import (
	"context"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// indexBackfill names the one-time announcement of the pages the 2026-10-18 index rule returned to
// search: apps paused only by the 90-day rule, which Homebrew still installs. No catalog change
// announces them, since only the rule changed. A later rule change that adds pages the same way
// gets a new name.
const indexBackfill = "2026-10-18-paused-apps"

const (
	// While this key exists an unfinished backfill waits, instead of refreshing thousands of pages
	// and purging Cloudflare every 30 seconds.
	backfillRetryKey   = "opennavo:url-notify:index-backfill:retry"
	backfillRetryAfter = 10 * time.Minute
	// API processes coalesce invalidations for up to 10 seconds (cache.invalidationInterval); page
	// renders before then could still read details cached under the old rule.
	backfillCacheSettle = 15 * time.Second
)

// backfill refreshes and announces the pages indexBackfill covers, once. It runs in the worker,
// which a deploy restarts only after the new API and web serve traffic, and follows the feed's
// order: Go API caches (details cached under the old rule would render noindex again), page
// caches, Cloudflare, then IndexNow. The announcement waits until Cloudflare holds no stale copy,
// so crawlers that follow it fetch pages that are indexable now.
func (n *Notifier) backfill(ctx context.Context, state *store.URLNotifyState) error {
	if state.IndexBackfill == indexBackfill || n.backfillPaused(ctx) {
		return nil
	}
	rows, err := n.Store.PausedIndexRows(ctx)
	if err != nil {
		return err
	}
	policy, err := n.Store.IndexPolicy(ctx)
	if err != nil {
		return err
	}
	changes, index, ids := []store.URLChange{}, map[int64]store.IndexRow{}, []int64{}
	for _, row := range rows {
		// The sitemap's rule: Homebrew must still offer the app, whatever the policy mode.
		facts := row.Facts()
		if row.Hidden || !seo.DecidePage(facts, seo.PageRef{Kind: seo.PageOverview, Locale: row.SourceLocale, Page: 1}, policy).Announce {
			continue
		}
		changes = append(changes, store.URLChange{PackageID: row.ID, Kind: row.Kind, Token: row.Token, Op: "upsert", Reason: "index"})
		index[row.ID] = row
		ids = append(ids, row.ID)
	}
	if len(changes) == 0 {
		state.IndexBackfill = indexBackfill
		return n.Store.SaveURLNotifyState(ctx, *state)
	}
	// One pattern, as for an index policy change: a pattern per app would rescan Redis per app.
	if err := n.Store.WithTx(ctx, func(tx *gorm.DB) error {
		return store.QueueCacheInvalidation(ctx, tx, "url-notify:index-backfill", cache.PackageDetailPrefix+"*")
	}); err != nil {
		return err
	}
	if err := n.Store.DispatchCacheInvalidations(ctx); err != nil {
		return err
	}
	n.Sleep(backfillCacheSettle)
	current, err := n.Store.CurrentPackageTokens(ctx, ids)
	if err != nil {
		return err
	}
	plan := BuildPlan(changes, current, index, policy, n.Config.WebBaseURL)
	if err := n.invalidate(ctx, plan.Paths); err != nil {
		state.LastError = "index backfill page cache: " + err.Error()
		n.pauseBackfill(ctx)
		_ = n.Store.SaveURLNotifyState(ctx, *state)
		return err
	}
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return err
	}
	purging := purgeEnabled(settings)
	if purging && state.IndexBackfill != indexBackfill+":purging" {
		pending := Plan{PurgePrefixes: state.PendingPurgePrefixes}.Merge(Plan{PurgePrefixes: plan.PurgePrefixes})
		state.PendingPurgePrefixes = pending.PurgePrefixes
		if len(state.PendingPurgePrefixes) > maxPurgePrefixes {
			state.PendingPurgePrefixes = sectionPrefixes(n.Config.WebBaseURL)
		}
		// Persist the phase with the queue so retries never re-add completed prefixes.
		state.IndexBackfill = indexBackfill + ":purging"
		// Persist the purge before any external request, as the feed does.
		if err := n.Store.SaveURLNotifyState(ctx, *state); err != nil {
			return err
		}
	}
	if purging {
		state.LastPurgeStatus, _ = n.drainPurges(ctx, state)
	}
	var status string
	switch {
	case purging && len(state.PendingPurgePrefixes) > 0:
		status = "waiting: Cloudflare purge pending"
	default:
		if problem := n.probe(ctx, plan.IndexNowURLs); problem != "" && settings.Settings.IndexNow.Enabled {
			status = "skipped: " + problem
		} else {
			var announced int
			status, announced = n.announce(ctx, settings, plan.IndexNowURLs)
			if !strings.HasPrefix(status, "error") &&
				(!settings.Settings.IndexNow.Enabled || announced == len(plan.IndexNowURLs)) {
				state.IndexBackfill = indexBackfill
			}
		}
	}
	if status != "" {
		state.LastIndexnowStatus = "index backfill " + status
	}
	if state.IndexBackfill != indexBackfill {
		n.pauseBackfill(ctx)
	}
	return n.Store.SaveURLNotifyState(ctx, *state)
}

func (n *Notifier) backfillPaused(ctx context.Context) bool {
	if n.Store.Redis == nil {
		return false
	}
	paused, err := n.Store.Redis.Exists(ctx, backfillRetryKey).Result()
	return err != nil || paused > 0
}

func (n *Notifier) pauseBackfill(ctx context.Context) {
	if n.Store.Redis != nil {
		n.Store.Redis.Set(context.WithoutCancel(ctx), backfillRetryKey, indexBackfill, backfillRetryAfter)
	}
}
