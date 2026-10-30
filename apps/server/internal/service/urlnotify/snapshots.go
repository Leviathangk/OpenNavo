package urlnotify

import (
	"context"
	"errors"
)

// PurgeSnapshots uses the existing Cloudflare quota, retry queue and transport.
// Unlike HTML caching, immutable snapshot objects require a purge even when the
// site's optional HTML edge cache is disabled. Failed purges remain durable.
func (n *Notifier) PurgeSnapshots(ctx context.Context, prefixes []string) error {
	n.defaults()
	if err := n.Store.QueueURLPurges(ctx, prefixes, maxPurgePrefixes, sectionPrefixes(n.Config.WebBaseURL)); err != nil {
		return err
	}
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Settings.Cloudflare.PurgeEnabled || settings.Settings.Cloudflare.ZoneID == "" {
		return errors.New("snapshot revocation requires CDN purge configuration")
	}
	_, err = n.purgeNow(ctx, settings, prefixes)
	return err
}
