package urlnotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"golang.org/x/net/html"
)

const (
	// The page refresher runs every two minutes; allow it another cycle to catch up.
	contentGateDirtyWait = 3 * time.Minute
	// A stalled refresher must not hold announcements indefinitely.
	contentGateDirtyStalled = 6 * time.Hour
	// Merge version, download-size and release-note updates arriving close together.
	contentGateQuiet = 30 * time.Minute
)

type contentGateStats struct {
	skippedUnchanged, waitingPageState, waitingQuiet int
}

// contentGatePage maps public app paths to the same first-page dates as sitemap.
// Intent pages share overview dates; supporting tabs stay outside this gate.
func contentGatePage(path string) (token, kind string, ok bool) {
	for _, locale := range seo.Locales {
		prefix := localePrefix(locale)
		if prefix != "" && strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if !strings.HasPrefix(path, "/") || len(parts) < 2 || len(parts) > 3 || parts[0] != "apps" || parts[1] == "" {
		return "", "", false
	}
	kind = "overview"
	if len(parts) == 3 {
		switch parts[2] {
		case "versions":
			kind = "versions"
		case "alternatives", "uninstall", "troubleshooting":
		default:
			return "", "", false
		}
	}
	return parts[1], kind, true
}

func (n *Notifier) retainChangedAnnouncement(ctx context.Context, row *store.URLNotificationRow) (bool, error) {
	row.ContentBaseline = time.Time{}
	if !n.Config.IndexNowContentGate || !row.Announce || row.Robots == "" {
		return true, nil
	}
	token, kind, ok := contentGatePage(row.Path)
	if !ok {
		return true, nil
	}
	state, err := n.Store.IndexNowPageState(ctx, token, row.Locale, kind, row.URL)
	if err != nil {
		return false, err
	}
	if state.PackageID == 0 {
		return true, nil
	}
	now := n.Now()
	if state.ChangedAt != nil && !state.ChangedAt.IsZero() {
		if !state.ChangedAt.Before(now.Add(-contentGateDirtyStalled)) {
			err := n.Store.DeferURLNotification(ctx, *row, now, now.Add(contentGateDirtyWait))
			if err == nil {
				n.contentGate.waitingPageState++
			}
			return false, err
		}
		n.Logger.Warn("IndexNow page state refresh stalled", "url", row.URL, "changed_at", *state.ChangedAt)
		return true, nil
	}
	if state.ContentModifiedAt == nil {
		return true, nil
	}
	if state.AnnouncedAt != nil && state.Robots != "" && seo.DecisionRobots(state.Robots) == seo.DecisionRobots(row.Robots) &&
		state.CanonicalPath == row.CanonicalPath && !state.ContentModifiedAt.After(*state.AnnouncedAt) {
		skipped, err := n.Store.SkipUnchangedURLAnnouncement(ctx, *row, now)
		if skipped {
			n.contentGate.skippedUnchanged++
		}
		return false, err
	}
	if due := state.ContentModifiedAt.Add(contentGateQuiet); due.After(now) {
		err := n.Store.DeferURLNotification(ctx, *row, now, due)
		if err == nil {
			n.contentGate.waitingQuiet++
		}
		return false, err
	}
	row.ContentBaseline = *state.ContentModifiedAt
	return true, nil
}

func (n *Notifier) notifications(ctx context.Context, plan Plan) ([]store.URLNotification, error) {
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(n.Config.WebBaseURL, "/")
	byURL := map[string]store.URLNotification{}
	for _, path := range plan.Paths {
		locale := "en-US"
		for _, code := range seo.Locales {
			prefix := localePrefix(code)
			if prefix != "" && (path == prefix || strings.HasPrefix(path, prefix+"/")) {
				locale = code
				break
			}
		}
		prefix := ""
		for _, p := range plan.PurgePrefixes {
			if p == purgeHost(base)+path || strings.HasPrefix(purgeHost(base)+path, p+"/") {
				prefix = p
				break
			}
		}
		byURL[base+path] = store.URLNotification{URL: base + path, Path: path, Locale: locale, Reason: "updated", PurgePrefix: prefix}
	}
	for _, raw := range plan.IndexNowURLs {
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		note, ok := byURL[raw]
		if !ok {
			parent := raw
			for _, page := range []string{"versions", "dependencies", "details", "alternatives", "uninstall", "troubleshooting"} {
				if strings.HasSuffix(raw, "/"+page) {
					parent = strings.TrimSuffix(raw, "/"+page)
					break
				}
			}
			note = byURL[parent]
			note.URL, note.Path = raw, parsed.Path
		}
		note.Announce = true
		if plan.BackfillURLs[raw] {
			note.Reason = "index"
		}
		if d, ok := plan.Decisions[raw]; ok {
			note.Robots, note.CanonicalPath = d.Robots, d.CanonicalPath
		}
		// Articles announce only the language snapshots included by the public sitemap: ready, and open
		// for indexing.
		path := strings.TrimPrefix(note.Path, localePrefix(note.Locale))
		if strings.HasPrefix(path, "/articles/") {
			article, found, err := n.Store.PublicArticle(ctx, strings.TrimPrefix(path, "/articles/"), note.Locale)
			if err != nil {
				return nil, err
			}
			var ready []string
			if found {
				if err := json.Unmarshal([]byte(article.ReadyLocales), &ready); err != nil {
					return nil, err
				}
				note.Announce = slices.Contains(ready, note.Locale) && seo.LocaleOpen(settings.Settings.Pages.OpenLocales, note.Locale)
				if note.Announce {
					note.Robots = "index, follow"
				}
			} else {
				state, _, err := n.Store.ArticleState(ctx, strings.TrimPrefix(path, "/articles/"))
				if err != nil {
					return nil, err
				}
				note.Announce = state == "moved" || state == "withdrawn"
			}
		} else if !strings.HasPrefix(path, "/apps/") {
			note.Announce = seo.LocaleOpen(settings.Settings.Pages.OpenLocales, note.Locale)
		}
		byURL[raw] = note
	}
	keys := make([]string, 0, len(byURL))
	for key := range byURL {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]store.URLNotification, 0, len(keys))
	for _, key := range keys {
		out = append(out, byURL[key])
	}
	return out, nil
}

func (n *Notifier) drainOutbox(ctx context.Context, owner string) (purged, announced int, resultErr error) {
	n.contentGate = contentGateStats{}
	defer func() {
		stats := n.contentGate
		if stats.skippedUnchanged+stats.waitingPageState+stats.waitingQuiet > 0 {
			n.Logger.Info("IndexNow content gate", "skipped_unchanged", stats.skippedUnchanged,
				"waiting_page_state", stats.waitingPageState, "waiting_quiet", stats.waitingQuiet)
		}
	}()
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return 0, 0, err
	}
	// Three bounded passes allow a healthy batch to finish all phases in one worker invocation.
	for pass := 0; pass < 3; pass++ {
		rows, err := n.Store.ClaimURLNotifications(ctx, owner, n.Now(), min(n.Batch, 100))
		if err != nil {
			return purged, announced, err
		}
		if len(rows) == 0 {
			break
		}
		pending := []store.URLNotificationRow{}
		paths, prefixes := []string{}, []string{}
		for _, row := range rows {
			if row.State == "pending" {
				owned, err := n.Store.RenewURLNotification(ctx, row, n.Now())
				if err != nil {
					return purged, announced, err
				}
				if !owned {
					continue
				}
				pending = append(pending, row)
				paths = append(paths, row.Path)
				if row.PurgePrefix != "" && !slices.Contains(prefixes, row.PurgePrefix) {
					prefixes = append(prefixes, row.PurgePrefix)
				}
			}
		}
		if len(pending) > 0 {
			err = n.invalidate(ctx, paths)
			if err == nil && purgeEnabled(settings) && len(prefixes) > 0 {
				// A failed edge purge is not these rows' to retry: the prefixes left join the durable purge queue,
				// which every run retries first and without a deadline (drainPurges). The rows spend no attempt,
				// so a long Cloudflare outage cannot kill their probe or IndexNow submission. While the queue
				// holds work, new prefixes join it at once rather than wait behind it for the shared interval.
				state, e := n.Store.URLNotifyState(ctx)
				if e != nil {
					return purged, announced, e
				}
				left, purgeErr := prefixes, error(nil)
				if len(state.PendingPurgePrefixes) == 0 {
					var count int
					count, purgeErr = n.purgeNow(ctx, settings, prefixes)
					purged += count
					left = prefixes[count:]
				}
				var wait *purgeWait
				if errors.As(purgeErr, &wait) {
					// Waiting for purge capacity (the shared interval or Cloudflare's 429) defers the rows.
					err = purgeErr
				} else if len(left) > 0 {
					if e := n.Store.QueueURLPurges(ctx, left, maxPurgePrefixes, sectionPrefixes(n.Config.WebBaseURL)); e != nil {
						return purged, announced, e
					}
					if purgeErr != nil {
						resultErr = purgeErr
					}
				}
			}
			for _, row := range pending {
				if wait := (*purgeWait)(nil); errors.As(err, &wait) {
					if e := n.Store.DeferURLNotification(ctx, row, n.Now(), wait.until); e != nil {
						return purged, announced, e
					}
					continue
				}
				next := "invalidated"
				if !row.Announce {
					// A refresh-only obligation is fulfilled by successful invalidation and
					// edge purge. Do not consume two more claims for no-op probe/submission.
					next = "announced"
				}
				if e := n.Store.CompleteURLNotification(ctx, row, next, err, n.Now()); e != nil {
					return purged, announced, e
				}
			}
			if err != nil {
				var wait *purgeWait
				if !errors.As(err, &wait) {
					resultErr = err
				}
			}
		}
		ready := []store.URLNotificationRow{}
		urls := []string{}
		for _, row := range rows {
			if row.State == "pending" {
				continue
			}
			owned, err := n.Store.RenewURLNotification(ctx, row, n.Now())
			if err != nil {
				return purged, announced, err
			}
			if !owned {
				continue
			}
			if !row.Announce {
				// Finish refresh-only rows left in intermediate states by older workers.
				if err := n.Store.CompleteURLNotification(ctx, row, "announced", nil, n.Now()); err != nil {
					return purged, announced, err
				}
				continue
			}
			keep, decisionErr := n.retainAnnouncement(ctx, row, settings)
			if decisionErr == nil && keep {
				keep, decisionErr = n.retainChangedAnnouncement(ctx, &row)
			}
			if decisionErr != nil {
				if e := n.Store.CompleteURLNotification(ctx, row, row.State, decisionErr, n.Now()); e != nil {
					return purged, announced, e
				}
				resultErr = decisionErr
				continue
			}
			if !keep {
				continue
			}
			switch row.State {
			case "invalidated":
				err = n.probeNotification(ctx, row)
				var wait *probeWait
				if errors.As(err, &wait) {
					if e := n.Store.DeferURLNotification(ctx, row, n.Now(), wait.until); e != nil {
						return purged, announced, e
					}
					continue
				}
				if e := n.Store.CompleteURLNotification(ctx, row, "probed", err, n.Now()); e != nil {
					return purged, announced, e
				}
				if err != nil {
					resultErr = err
				}
			case "probed":
				if !settings.Settings.IndexNow.Enabled {
					if e := n.Store.PauseURLNotification(ctx, row, n.Now()); e != nil {
						return purged, announced, e
					}
					continue
				}
				ready = append(ready, row)
				urls = append(urls, row.URL)
			}
		}
		if len(ready) > 0 {
			// Probe loops may take time; fence again immediately before announcement.
			active, activeURLs := []store.URLNotificationRow{}, []string{}
			for i, row := range ready {
				owned, err := n.Store.RenewURLNotification(ctx, row, n.Now())
				if err != nil {
					return purged, announced, err
				}
				if owned {
					keep, err := n.retainAnnouncement(ctx, row, settings)
					if err == nil && keep {
						keep, err = n.retainChangedAnnouncement(ctx, &row)
					}
					if err != nil {
						return purged, announced, err
					}
					if !keep {
						continue
					}
					active = append(active, row)
					activeURLs = append(activeURLs, urls[i])
				}
			}
			ready, urls = active, activeURLs
			sort.SliceStable(ready, func(i, j int) bool { return ready[i].Reason != "index" && ready[j].Reason == "index" })
			for i, row := range ready {
				urls[i] = row.URL
			}
			if len(ready) == 0 {
				continue
			}
			settings, err = n.Store.SEOSettings(ctx)
			if err != nil {
				return purged, announced, err
			}
			if !settings.Settings.IndexNow.Enabled {
				for _, row := range ready {
					if err := n.Store.PauseURLNotification(ctx, row, n.Now()); err != nil {
						return purged, announced, err
					}
				}
				continue
			}
			// Ready-only ordering is insufficient: newer work may still be invalidating,
			// probing, retrying, or leased by another worker. Keep backfill out of
			// the budget until that work has had its turn.
			freshIDs := []int64{}
			for _, row := range ready {
				if row.Reason != "index" {
					freshIDs = append(freshIDs, row.ID)
				}
			}
			waiting, err := n.Store.HasUnreadyFreshNotifications(ctx, freshIDs)
			if err != nil {
				return purged, announced, err
			}
			if waiting {
				kept := ready[:0]
				for _, row := range ready {
					if row.Reason == "index" {
						if err := n.Store.DeferURLNotification(ctx, row, n.Now(), n.Now().Add(time.Minute)); err != nil {
							return purged, announced, err
						}
					} else {
						kept = append(kept, row)
					}
				}
				ready, urls = kept, urls[:len(kept)]
				if len(ready) == 0 {
					continue
				}
			}
			budgetDay := n.Now().UTC()
			limit := n.Config.IndexNowDailyLimit
			if limit <= 0 {
				limit = 5000
			}
			allowed, err := n.Store.ReserveIndexNowBudget(ctx, budgetDay, limit, len(urls))
			if err != nil {
				return purged, announced, err
			}
			tomorrow := time.Date(budgetDay.Year(), budgetDay.Month(), budgetDay.Day()+1, 0, 0, 0, 0, time.UTC)
			for _, row := range ready[allowed:] {
				if err := n.Store.DeferURLNotification(ctx, row, n.Now(), tomorrow); err != nil {
					return purged, announced, err
				}
			}
			ready, urls = ready[:allowed], urls[:allowed]
			if allowed == 0 {
				continue
			}
			status, count := n.announce(ctx, settings, urls)
			if err := n.Store.RecordIndexNowURLs(ctx, ready[:count]); err != nil {
				return purged, announced, err
			}
			total, recordErr := n.Store.RecordIndexNowAccepted(ctx, budgetDay, count)
			if recordErr != nil {
				return purged, announced, recordErr
			}
			n.Logger.Info("IndexNow daily submissions", "utc_day", budgetDay.Format("2006-01-02"), "accepted", total, "daily_limit", limit)

			announced += count
			for i, row := range ready {
				var problem error
				if i >= count {
					problem = errors.New(status)
					resultErr = problem
				}
				if err := n.Store.CompleteURLNotification(ctx, row, "announced", problem, n.Now()); err != nil {
					return purged, announced, err
				}
			}
		}
	}
	return purged, announced, resultErr
}

// purgeNow purges prefixes with the Cloudflare token and returns how many it purged. A missing or unreadable
// token is a failure like any other.
func (n *Notifier) purgeNow(ctx context.Context, settings store.SEOSettingsRow, prefixes []string) (int, error) {
	token, err := n.Store.SEOSecret(ctx, n.Config, "cloudflare")
	if err == nil && token == "" {
		err = errors.New("cloudflare credential unavailable")
	}
	if err != nil {
		return 0, err
	}
	count, _, err := n.purgePrefixes(ctx, settings, token, prefixes)
	return count, err
}

// A persistent job may outlive the issue evidence, even after its public probe, and its language may
// have closed for indexing since it was queued. Read the same decision again immediately before
// delivery; expiry never announces. Retired app and article URLs (queued without robots) still
// announce in every language, so engines can drop them.
func (n *Notifier) retainAnnouncement(ctx context.Context, row store.URLNotificationRow, settings store.SEOSettingsRow) (bool, error) {
	if !row.Announce {
		return true, nil
	}
	path := strings.TrimPrefix(row.Path, localePrefix(row.Locale))
	parts := strings.Split(strings.Trim(path, "/"), "/")
	live := row.Robots != "" || (parts[0] != "apps" && parts[0] != "articles")
	if live && !seo.LocaleOpen(settings.Settings.Pages.OpenLocales, row.Locale) {
		return false, n.Store.SuppressURLAnnouncement(ctx, row, "noindex, follow", row.CanonicalPath, n.Now())
	}
	// Retire already queued overview probes after the brand route became a redirect.
	if len(parts) == 2 && parts[0] == "apps" && parts[1] == "opennavo" {
		return false, n.Store.SuppressURLAnnouncement(ctx, row, "noindex, follow", localePrefix(row.Locale)+"/download", n.Now())
	}
	if len(parts) != 3 || parts[0] != "apps" || (parts[2] != string(seo.PageTroubleshooting) && parts[2] != string(seo.PageDependencies) && parts[2] != string(seo.PageVersions)) {
		return true, nil
	}
	policy, err := n.Store.IndexPolicy(ctx)
	if err != nil {
		return false, err
	}
	var index store.IndexRow
	var found bool
	if parts[2] != string(seo.PageTroubleshooting) {
		index, found, err = n.Store.IndexRowByToken(ctx, parts[1])
	} else {
		index, found, err = n.Store.TroubleshootingIndexRow(ctx, parts[1], n.Now())
	}
	if err != nil {
		return false, err
	}
	// Notes/translations can change while a queued notification waits for budget.
	// Recompute the same first-page evidence used by SSR before probing or sending.
	if found && parts[2] == string(seo.PageVersions) {
		rows := []store.IndexRow{index}
		if err := n.Store.PopulatePageNotes(ctx, rows, 1); err != nil {
			return false, err
		}
		index = rows[0]
	}
	f := seo.Facts{Token: parts[1], Hidden: true}
	if found {
		f = index.Facts()
	}
	d := seo.DecidePage(f, seo.PageRef{Kind: seo.PageKind(parts[2]), Locale: row.Locale}, policy)
	if d.Announce {
		return true, nil
	}
	return false, n.Store.SuppressURLAnnouncement(ctx, row, d.Robots, d.CanonicalPath, n.Now())
}

type probeWait struct{ until time.Time }

func (e *probeWait) Error() string { return "waiting for public probe cooldown" }

// Probe the same public URL an indexer will fetch, after the edge purge completed.
func (n *Notifier) probeNotification(ctx context.Context, row store.URLNotificationRow) error {
	if !row.Announce {
		return nil
	}
	target, err := url.Parse(row.URL)
	if err != nil {
		return err
	}
	site, err := url.Parse(n.Config.WebBaseURL)
	if err != nil {
		return err
	}
	if target.Host != site.Host || target.Scheme != site.Scheme {
		return errors.New("notification outside configured site")
	}
	due, err := n.Store.URLProbeReadyAt(ctx)
	if err != nil {
		return err
	}
	if due.After(n.Now()) {
		return &probeWait{until: due}
	}
	// Run remains globally serialized; retries never add parallel public requests.
	response, err := n.probeHTTP(ctx, row.URL)
	if err != nil {
		retryAt := n.Now()
		var wait *probeWait
		if errors.As(err, &wait) {
			retryAt = wait.until
		}
		if e := n.Store.RecordURLProbe(ctx, n.Now(), retryAt, true); e != nil {
			return e
		}
		due, e := n.Store.URLProbeReadyAt(ctx)
		if e != nil {
			return e
		}
		return &probeWait{until: maxProbeTime(due, retryAt)}
	}
	defer func() { _ = response.Body.Close() }()
	overloaded := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	retryAt := n.Now()
	if overloaded {
		retryAt = purgeRetryAt(response.Header.Get("Retry-After"), n.Now())
	}
	if err := n.Store.RecordURLProbe(ctx, n.Now(), retryAt, overloaded); err != nil {
		return err
	}
	if row.Robots == "" && (response.StatusCode >= 300 && response.StatusCode < 400 || response.StatusCode == 404 || response.StatusCode == 410) {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("public page probe returned %d", response.StatusCode)
	}
	if row.Robots == "" {
		return nil
	}
	canonical := ""
	var robots seo.Robots
	for _, value := range response.Header.Values("X-Robots-Tag") {
		robots.Header(value)
	}
	tokenizer := html.NewTokenizer(io.LimitReader(response.Body, 2<<20))
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		attrs := map[string]string{}
		for _, a := range token.Attr {
			attrs[strings.ToLower(a.Key)] = a.Val
		}
		if token.Data == "link" && attrs["rel"] == "canonical" {
			canonical = attrs["href"]
		}
		if token.Data == "meta" {
			robots.Meta(attrs["name"], attrs["content"])
		}
	}
	want := row.URL
	if row.CanonicalPath != "" {
		want = strings.TrimRight(n.Config.WebBaseURL, "/") + row.CanonicalPath
	}
	// Compare what indexers act on: extra preview controls (max-image-preview on articles) are fine,
	// a noindex in the markup or the X-Robots-Tag header is not.
	got, decided := robots.Intent(), seo.DecisionRobots(row.Robots)
	if !sameURL(canonical, want) || got != decided {
		return fmt.Errorf("public canonical or robots differs from page decision: canonical %q, want %q; robots %q, want %q", clip(canonical, 300), want, got, decided)
	}
	return nil
}

// sameURL compares canonical URLs as URLs: scheme and host case-insensitively, the path decoded.
func sameURL(got, want string) bool {
	a, err := url.Parse(got)
	if err != nil {
		return false
	}
	b, err := url.Parse(want)
	if err != nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) && a.Path == b.Path && a.RawQuery == b.RawQuery && a.Fragment == b.Fragment
}

// clip bounds untrusted page text kept in last_error without splitting a UTF-8 sequence.
func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "")
}
