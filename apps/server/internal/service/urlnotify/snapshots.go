package urlnotify

import (
	"context"
	"errors"
	"fmt"
)

// ErrPurgeDeferred reports a purge that is durably queued but could not run now: Cloudflare is
// unconfigured or unavailable. Snapshot revocation treats it as a warning and retries from the
// queue; only a failure to queue belongs on the blocking side.
var ErrPurgeDeferred = errors.New("snapshot CDN purge deferred")

// PurgeSnapshots queues the prefix first, then tries the existing Cloudflare quota and transport.
// Unlike HTML caching, immutable snapshot objects require a purge even when the site's optional
// HTML edge cache is disabled. A queue write failure is returned as-is (blocking); a missing or
// failing CDN purger returns ErrPurgeDeferred and keeps the durable debt.
func (n *Notifier) PurgeSnapshots(ctx context.Context, prefixes []string) error {
	n.defaults()
	if err := n.Store.QueueURLPurges(ctx, prefixes, maxPurgePrefixes, sectionPrefixes(n.Config.WebBaseURL)); err != nil {
		return err
	}
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return fmt.Errorf("%w: read purge settings: %w", ErrPurgeDeferred, err)
	}
	if !settings.Settings.Cloudflare.PurgeEnabled || settings.Settings.Cloudflare.ZoneID == "" {
		return fmt.Errorf("%w: Cloudflare purge disabled", ErrPurgeDeferred)
	}
	if _, err := n.purgeNow(ctx, settings, prefixes); err != nil {
		return fmt.Errorf("%w: %w", ErrPurgeDeferred, err)
	}
	// The purge succeeded, so clear the debt now instead of leaving it for the next drain to purge
	// again. If clearing fails the prefixes stay queued and a retry is harmless.
	if err := n.Store.DequeueURLPurges(ctx, prefixes); err != nil {
		return fmt.Errorf("%w: clear purge queue: %w", ErrPurgeDeferred, err)
	}
	return nil
}
