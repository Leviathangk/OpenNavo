package management

import (
	"context"
	"strings"
	"time"
)

// Edge lifetimes of HTML pages (max-age); they mirror LONG_EDGE_CACHE_VALUE and SHORT_EDGE_CACHE_VALUE in
// apps/web/server/utils/edgeCache.ts (app, article and author pages are purged on change; the rest expire).
// Only this read-only view uses them: purges do not depend on the lifetimes.
const (
	edgeCacheLongSeconds  = 172800
	edgeCacheShortSeconds = 600
)

// siteIndexSettings is the agents' read-only view of the site settings that decide indexing: the page
// policy, crawler rules, the edge cache switch and lifetimes, whether IndexNow announces, the public
// analytics ID and the URL change feed. It never contains credentials or their ciphertexts, nor CDN
// identifiers; agents cannot change any of it (decision D3).
func (s *Service) siteIndexSettings(ctx context.Context) (any, error) {
	row, err := s.Store.SEOSettings(ctx)
	if err != nil {
		return nil, err
	}
	feed, err := s.changeFeedView(ctx, s.Store)
	if err != nil {
		return nil, err
	}
	settings := row.Settings
	return map[string]any{
		"siteUrl":    strings.TrimRight(s.Config.WebBaseURL, "/"),
		"revision":   row.Revision,
		"updatedAt":  row.UpdatedAt.UTC().Format(time.RFC3339),
		"pages":      settings.Pages,
		"crawlers":   settings.Crawlers,
		"edgeCache":  map[string]any{"enabled": settings.EdgeCache.Enabled, "longTtlSeconds": edgeCacheLongSeconds, "shortTtlSeconds": edgeCacheShortSeconds},
		"indexNow":   map[string]any{"enabled": settings.IndexNow.Enabled},
		"analytics":  map[string]any{"usageEnabled": settings.Analytics.UsageEnabled, "ga4Enabled": settings.Analytics.GA4Enabled, "ga4MeasurementId": settings.Analytics.GA4MeasurementID},
		"changeFeed": feed,
	}, nil
}
