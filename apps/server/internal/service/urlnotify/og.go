package urlnotify

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
)

// shareImagePath is the origin and CDN prefix for nuxt-og-image output. The encoded
// image parameters contain the package token, so Cloudflare cannot purge one app
// without the prefix.
const shareImagePath = "/_og/"

// shareImagePurgePrefix is the Cloudflare prefix covering every share image.
func shareImagePurgePrefix(siteURL string) string {
	return purgeHost(siteURL) + shareImagePath
}

// shareImagePaths lists the app pages whose origin cache holds a share image in
// every site language. The page path is the stable part of the nuxt-og-image key.
func shareImagePaths(token string) []string {
	paths := make([]string, 0, len(i18n.SiteLocales))
	for _, locale := range i18n.SiteLocales {
		paths = append(paths, i18n.Metadata[locale].Prefix+"/apps/"+token)
	}
	return paths
}

// QueueShareImagePurge makes a visibility change durable before returning: the
// outbox retries the og:true web invalidation, and the existing purge queue
// retries the Cloudflare /_og/ purge. Neither is awaited by the admin write.
func (n *Notifier) QueueShareImagePurge(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("share image purge requires a package token")
	}
	return n.QueueShareImagePaths(ctx, shareImagePaths(token))
}

// QueueShareImagePaths enqueues the same work for callers that already resolved
// the app page paths.
func (n *Notifier) QueueShareImagePaths(ctx context.Context, paths []string) error {
	n.defaults()
	seen := make(map[string]bool, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if !seen[path] {
			seen[path] = true
			unique = append(unique, path)
		}
	}
	paths = unique
	if len(paths) == 0 {
		return nil
	}
	prefix := shareImagePurgePrefix(n.Config.WebBaseURL)
	if err := n.Store.QueueURLPurges(ctx, []string{prefix}, maxPurgePrefixes, sectionPrefixes(n.Config.WebBaseURL)); err != nil {
		return fmt.Errorf("queue share image purge: %w", err)
	}
	base := strings.TrimRight(n.Config.WebBaseURL, "/")
	var problems []error
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			problems = append(problems, fmt.Errorf("invalid share image path %q", path))
			continue
		}
		note := store.URLNotification{
			// The synthetic URL keeps the share-image obligation distinct from the
			// normal page notification for the same path; the outbox upserts by URL.
			URL:         base + "/_og" + path,
			Path:        path,
			Locale:      notificationLocale(path),
			Reason:      "updated",
			PurgePrefix: prefix,
			Announce:    false,
		}
		if err := store.QueueURLNotification(ctx, n.Store.DB, note); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// notificationLocale finds the site language encoded in a localized page path.
func notificationLocale(path string) string {
	for _, locale := range i18n.SiteLocales {
		prefix := i18n.Metadata[locale].Prefix
		if prefix != "" && (path == prefix || strings.HasPrefix(path, prefix+"/")) {
			return string(locale)
		}
	}
	return string(i18n.DefaultLocale)
}
