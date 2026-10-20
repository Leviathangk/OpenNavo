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
	"strings"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"golang.org/x/net/html"
)

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
		if d, ok := plan.Decisions[raw]; ok {
			note.Robots, note.CanonicalPath = d.Robots, d.CanonicalPath
		}
		// Articles announce only the language snapshots included by the public sitemap.
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
				note.Announce = slices.Contains(ready, note.Locale)
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
			note.Announce = slices.Contains(settings.Settings.Pages.OpenLocales, note.Locale)
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
				token, e := n.Store.SEOSecret(ctx, n.Config, "cloudflare")
				err = e
				if err == nil && token == "" {
					err = errors.New("cloudflare credential unavailable")
				}
				if err == nil {
					count, status := n.purgePrefixes(ctx, settings, token, prefixes)
					purged += count
					if count != len(prefixes) {
						err = fmt.Errorf("edge purge incomplete: status %d", status)
					}
				}
			}
			for _, row := range pending {
				if e := n.Store.CompleteURLNotification(ctx, row, "invalidated", err, n.Now()); e != nil {
					return purged, announced, e
				}
			}
			if err != nil {
				resultErr = err
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
			keep, decisionErr := n.retainTroubleshootingAnnouncement(ctx, row)
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
				if e := n.Store.CompleteURLNotification(ctx, row, "probed", err, n.Now()); e != nil {
					return purged, announced, e
				}
				if err != nil {
					resultErr = err
				}
			case "probed":
				if !row.Announce {
					if e := n.Store.CompleteURLNotification(ctx, row, "announced", nil, n.Now()); e != nil {
						return purged, announced, e
					}
					continue
				}
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
					keep, err := n.retainTroubleshootingAnnouncement(ctx, row)
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
			status, count := n.announce(ctx, settings, urls)
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

// A persistent job may outlive the issue evidence, even after its public probe.
// Read the same decision again immediately before delivery; expiry never announces.
func (n *Notifier) retainTroubleshootingAnnouncement(ctx context.Context, row store.URLNotificationRow) (bool, error) {
	path := strings.TrimPrefix(row.Path, localePrefix(row.Locale))
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if !row.Announce || len(parts) != 3 || parts[0] != "apps" || parts[2] != string(seo.PageTroubleshooting) {
		return true, nil
	}
	policy, err := n.Store.IndexPolicy(ctx)
	if err != nil {
		return false, err
	}
	index, found, err := n.Store.TroubleshootingIndexRow(ctx, parts[1], n.Now())
	if err != nil {
		return false, err
	}
	f := seo.Facts{Token: parts[1], Hidden: true}
	if found {
		f = index.Facts()
	}
	d := seo.DecidePage(f, seo.PageRef{Kind: seo.PageTroubleshooting, Locale: row.Locale}, policy)
	if d.Announce {
		return true, nil
	}
	return false, n.Store.SuppressURLAnnouncement(ctx, row, d.Robots, d.CanonicalPath, n.Now())
}

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, row.URL, http.NoBody)
	if err != nil {
		return err
	}
	client := *n.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return errors.New("public page probe unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if row.Robots == "" && (response.StatusCode >= 300 && response.StatusCode < 400 || response.StatusCode == 404 || response.StatusCode == 410) {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("public page probe returned %d", response.StatusCode)
	}
	if row.Robots == "" {
		return nil
	}
	canonical, robots := "", "index, follow"
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
		if token.Data == "meta" && strings.EqualFold(attrs["name"], "robots") {
			robots = attrs["content"]
		}
	}
	want := row.URL
	if row.CanonicalPath != "" {
		want = strings.TrimRight(n.Config.WebBaseURL, "/") + row.CanonicalPath
	}
	if canonical != want || strings.ReplaceAll(strings.ToLower(robots), " ", "") != strings.ReplaceAll(row.Robots, " ", "") {
		return errors.New("public canonical or robots differs from page decision")
	}
	return nil
}
