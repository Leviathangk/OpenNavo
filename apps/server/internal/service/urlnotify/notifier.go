package urlnotify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	defaultSettle   = 20 * time.Second
	defaultBatch    = 2000
	invalidateChunk = 2000
	// Cloudflare takes up to 100 prefixes per purge request, and on the Free plan 5 requests a minute
	// (bursts of 25) for the whole account.
	purgeChunk    = 100
	purgeInterval = 12 * time.Second
	// Above this backlog size, coalesce work into one purge of the long-cached sections.
	maxPurgePrefixes = 1000
	indexNowChunk    = 10000
	lockKey          = "opennavo:url-notify:lock"
	// buildKey holds the web build whose release purge is done.
	buildKey = "opennavo:url-notify:web-build"
	// After a failed release purge, retryKey pauses further attempts for releaseRetryAfter, so a bad
	// token or an exhausted purge rate limit is not hit again every 30 seconds while changed pages
	// still need the same allowance.
	retryKey          = "opennavo:url-notify:web-build:retry"
	releaseRetryAfter = 5 * time.Minute
)

// Notifier runs the feed. One run handles up to Batch rows; the worker calls it every 30 seconds.
type Notifier struct {
	Store            *store.Store
	Config           config.Config
	HTTP             *http.Client
	Logger           *slog.Logger
	Now              func() time.Time
	Settle           time.Duration
	Batch            int
	IndexNowEndpoint string
	CloudflareAPI    string
	// Sleep waits between retries; tests replace it.
	Sleep func(time.Duration)
}

type Result struct {
	Changes, Paths, Purged, Announced int
}

func (n *Notifier) defaults() {
	if n.HTTP == nil {
		n.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if n.Logger == nil {
		n.Logger = slog.Default()
	}
	if n.Now == nil {
		n.Now = time.Now
	}
	if n.Settle == 0 {
		n.Settle = defaultSettle
	}
	if n.Batch == 0 {
		n.Batch = defaultBatch
	}
	if n.IndexNowEndpoint == "" {
		n.IndexNowEndpoint = "https://api.indexnow.org/indexnow"
	}
	if n.CloudflareAPI == "" {
		n.CloudflareAPI = "https://api.cloudflare.com/client/v4"
	}
	if n.Sleep == nil {
		n.Sleep = time.Sleep
	}
}

// Run handles one batch. The cursor advances after page caches are refreshed and pending edge
// purges are persisted. Cloudflare retries do not block later changes or IndexNow announcements.
func (n *Notifier) Run(ctx context.Context) (Result, error) {
	n.defaults()
	var result Result
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	owner := hex.EncodeToString(token)
	if n.Store.Redis != nil {
		ok, err := n.Store.Redis.SetNX(ctx, lockKey, owner, 5*time.Minute).Result()
		if err != nil || !ok {
			return result, err
		}
		defer func() {
			if value, err := n.Store.Redis.Get(context.WithoutCancel(ctx), lockKey).Result(); err == nil && value == owner {
				n.Store.Redis.Del(context.WithoutCancel(ctx), lockKey)
			}
		}()
	}
	// Go API caches first, so pages re-rendered below read fresh data.
	if err := n.Store.DispatchCacheInvalidations(ctx); err != nil {
		return result, err
	}
	state, err := n.Store.URLNotifyState(ctx)
	if err != nil {
		return result, err
	}
	// A new web release is purged before this run's changes, and also when there are none.
	release := n.releasePurge(ctx)
	if release != "" {
		state.LastPurgeStatus = release
		if err := n.Store.SaveURLNotifyState(ctx, state); err != nil {
			return result, err
		}
	}
	before := n.Now().Add(-n.Settle)
	end, err := n.Store.URLChangeCursorEnd(ctx, state.Cursor, before, n.Batch)
	if err != nil {
		return result, err
	}
	pages, err := n.Store.PendingContentURLChanges(ctx, state.ContentCursor, before, n.Batch)
	if err != nil {
		return result, err
	}
	if end == state.Cursor && len(pages) == 0 {
		if len(state.PendingPurgePrefixes) > 0 {
			state.LastPurgeStatus, result.Purged = n.drainPurges(ctx, &state)
			if release != "" {
				state.LastPurgeStatus = release + "; " + state.LastPurgeStatus
			}
			return result, n.Store.SaveURLNotifyState(ctx, state)
		}
		return result, nil
	}
	var plan Plan
	if end != state.Cursor {
		if plan, result.Changes, err = n.catalogPlan(ctx, state.Cursor, end, before); err != nil {
			return result, err
		}
	}
	// Articles and authors (WP14) share the same refresh, purge and announcement steps.
	plan = plan.Merge(ContentPlan(pages, n.Config.WebBaseURL))
	result.Changes += len(pages)
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return result, err
	}
	result.Paths = len(plan.Paths)
	now := n.Now().UTC()
	state.LastRunAt, state.LastURLs = &now, len(plan.Paths)
	if err := n.invalidate(ctx, plan.Paths); err != nil {
		state.LastError = "page cache: " + err.Error()
		_ = n.Store.SaveURLNotifyState(ctx, state)
		return result, err
	}
	state.LastError = ""
	if purgeEnabled(settings) {
		pending := Plan{PurgePrefixes: state.PendingPurgePrefixes}.Merge(Plan{PurgePrefixes: plan.PurgePrefixes})
		state.PendingPurgePrefixes = pending.PurgePrefixes
		if len(state.PendingPurgePrefixes) > maxPurgePrefixes {
			state.PendingPurgePrefixes = sectionPrefixes(n.Config.WebBaseURL)
		}
	}
	// Persist work before any external request or cursor advance. A crash can repeat a purge,
	// but cannot lose one. Failed tasks remain independent of both feed cursors.
	if err := n.Store.SaveURLNotifyState(ctx, state); err != nil {
		return result, err
	}
	state.LastPurgeStatus, result.Purged = n.drainPurges(ctx, &state)
	if release != "" {
		// The release purge stays visible next to the purge of this run's changes.
		state.LastPurgeStatus = strings.TrimSuffix(release+"; "+state.LastPurgeStatus, "; ")
	}
	if problem := n.probe(ctx, plan.IndexNowURLs); problem != "" && settings.Settings.IndexNow.Enabled {
		state.LastIndexnowStatus = "skipped: " + problem
	} else {
		state.LastIndexnowStatus, result.Announced = n.announce(ctx, settings, plan.IndexNowURLs)
	}
	state.Cursor = end
	if len(pages) > 0 {
		state.ContentCursor = pages[len(pages)-1].Seq
	}
	return result, n.Store.SaveURLNotifyState(ctx, state)
}

// catalogPlan turns the settled catalog changes up to end into paths and URLs.
func (n *Notifier) catalogPlan(ctx context.Context, cursor, end int64, before time.Time) (Plan, int, error) {
	changes, err := n.Store.PendingURLChanges(ctx, cursor, before, n.Batch)
	if err != nil {
		return Plan{}, 0, err
	}
	changes = filterThrough(changes, end)
	ids := []int64{}
	for _, change := range changes {
		ids = append(ids, change.PackageID)
	}
	current, err := n.Store.CurrentPackageTokens(ctx, ids)
	if err != nil {
		return Plan{}, 0, err
	}
	rows, err := n.Store.IndexRowsByID(ctx, ids)
	if err != nil {
		return Plan{}, 0, err
	}
	index := make(map[int64]store.IndexRow, len(rows))
	for _, row := range rows {
		index[row.ID] = row
	}
	policy, err := n.Store.IndexPolicy(ctx)
	if err != nil {
		return Plan{}, 0, err
	}
	return BuildPlan(changes, current, index, policy, n.Config.WebBaseURL), len(changes), nil
}

func filterThrough(changes []store.URLChange, end int64) []store.URLChange {
	out := changes[:0]
	for _, change := range changes {
		if change.Seq <= end {
			out = append(out, change)
		}
	}
	return out
}

func (n *Notifier) post(ctx context.Context, endpoint, bearer string, body any) (int, string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(text), nil
}

// invalidate removes page-cache entries through the web's internal port.
func (n *Notifier) invalidate(ctx context.Context, paths []string) error {
	if n.Config.WebInternalURL == "" || n.Config.WebInternalSecret == "" || len(paths) == 0 {
		return nil
	}
	endpoint := strings.TrimRight(n.Config.WebInternalURL, "/") + "/cache/invalidate"
	for start := 0; start < len(paths); start += invalidateChunk {
		status, _, err := n.post(ctx, endpoint, n.Config.WebInternalSecret, map[string]any{"paths": paths[start:min(start+invalidateChunk, len(paths))]})
		if err != nil {
			return errors.New("web cache endpoint unreachable")
		}
		if status != http.StatusOK {
			return fmt.Errorf("web cache endpoint returned %d", status)
		}
	}
	return nil
}

// probe renders up to three changed pages through the web's internal port before IndexNow hears
// about them; a 5xx means the public state is not ready, so this run does not announce.
func (n *Notifier) probe(ctx context.Context, urls []string) string {
	if n.Config.WebInternalURL == "" || n.Config.WebInternalSecret == "" || len(urls) == 0 {
		return ""
	}
	paths := []string{}
	for _, raw := range urls[:min(3, len(urls))] {
		if parsed, err := url.Parse(raw); err == nil {
			paths = append(paths, parsed.EscapedPath())
		}
	}
	status, body, err := n.post(ctx, strings.TrimRight(n.Config.WebInternalURL, "/")+"/cache/probe", n.Config.WebInternalSecret, map[string]any{"paths": paths})
	if err != nil || status != http.StatusOK {
		return "probe unavailable"
	}
	var reply struct {
		Statuses map[string]int `json:"statuses"`
	}
	if json.Unmarshal([]byte(body), &reply) != nil {
		return "probe unavailable"
	}
	for path, code := range reply.Statuses {
		if code >= 500 {
			return fmt.Sprintf("probe %s returned %d", path, code)
		}
	}
	return ""
}

// purgeEnabled reports whether Cloudflare may be purged; without HTML edge caching it holds no HTML.
func purgeEnabled(settings store.SEOSettingsRow) bool {
	cf := settings.Settings.Cloudflare
	return settings.Settings.EdgeCache.Enabled && cf.PurgeEnabled && cf.ZoneID != ""
}

// drainPurges retries durable work even when there are no new feed changes.
func (n *Notifier) drainPurges(ctx context.Context, state *store.URLNotifyState) (string, int) {
	prefixes := state.PendingPurgePrefixes
	if len(prefixes) == 0 {
		return "", 0
	}
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return "error: Cloudflare settings unavailable", 0
	}
	if !purgeEnabled(settings) {
		return "paused: Cloudflare purge disabled", 0
	}
	token, err := n.Store.SEOSecret(ctx, n.Config, "cloudflare")
	if err != nil || token == "" {
		return "error: Cloudflare token unavailable", 0
	}
	purged, status := n.purgePrefixes(ctx, settings, token, prefixes)
	state.PendingPurgePrefixes = prefixes[purged:]
	if purged < len(prefixes) {
		return fmt.Sprintf("pending: %d prefixes; Cloudflare returned %d after %d prefixes", len(prefixes)-purged, status, purged), purged
	}
	return fmt.Sprintf("ok: %d prefixes", purged), purged
}

// purgeSections purges the long-cached sections in every language with one request; label says
// what for in the status.
func (n *Notifier) purgeSections(ctx context.Context, settings store.SEOSettingsRow, label string) (string, int) {
	token, err := n.Store.SEOSecret(ctx, n.Config, "cloudflare")
	if err != nil || token == "" {
		return "error: Cloudflare token unavailable", 0
	}
	sections := sectionPrefixes(n.Config.WebBaseURL)
	if purged, status := n.purgePrefixes(ctx, settings, token, sections); purged < len(sections) {
		return fmt.Sprintf("error: Cloudflare returned %d for %s", status, label), 0
	}
	return fmt.Sprintf("ok: %s (%d prefixes)", label, len(sections)), len(sections)
}

// purgePrefixes sends prefixes to Cloudflare 100 per request and stops at the first failure. It
// returns how many were purged and the last response status (0 when Cloudflare was unreachable).
func (n *Notifier) purgePrefixes(ctx context.Context, settings store.SEOSettingsRow, token string, prefixes []string) (int, int) {
	endpoint := n.CloudflareAPI + "/zones/" + url.PathEscape(settings.Settings.Cloudflare.ZoneID) + "/purge_cache"
	purged := 0
	for chunk := range slices.Chunk(prefixes, purgeChunk) {
		allowed, err := n.Store.ReserveURLPurge(ctx, n.Now(), purgeInterval)
		if err != nil {
			return purged, 0
		}
		if !allowed {
			return purged, http.StatusTooManyRequests
		}
		// Do not retry immediately: every attempt uses the shared sustained allowance.
		status, body, err := n.post(ctx, endpoint, token, map[string]any{"prefixes": chunk})
		var reply struct {
			Success bool `json:"success"`
		}
		if err != nil || status != http.StatusOK || json.Unmarshal([]byte(body), &reply) != nil || !reply.Success {
			return purged, status
		}
		purged += len(chunk)
	}
	return purged, http.StatusOK
}

// releasePurge purges the long-cached sections once per web release, since the edge would otherwise
// serve pages of the previous build for hours. The build is remembered only once handled, so a
// failed purge is retried by the next run. It returns the purge status, empty when none was sent.
func (n *Notifier) releasePurge(ctx context.Context) string {
	// Without Redis the build cannot be remembered, and every run would purge.
	if n.Store.Redis == nil {
		return ""
	}
	build := n.webBuild(ctx)
	if build == "" {
		return ""
	}
	if seen, err := n.Store.Redis.Get(ctx, buildKey).Result(); seen == build || (err != nil && !errors.Is(err, redis.Nil)) {
		return ""
	}
	if paused, err := n.Store.Redis.Exists(ctx, retryKey).Result(); err != nil || paused > 0 {
		return ""
	}
	settings, err := n.Store.SEOSettings(ctx)
	if err != nil {
		return ""
	}
	status := ""
	if purgeEnabled(settings) {
		var purged int
		if status, purged = n.purgeSections(ctx, settings, "release "+build[:min(8, len(build))]); purged == 0 {
			n.Store.Redis.Set(ctx, retryKey, build, releaseRetryAfter)
			return status
		}
	}
	n.Store.Redis.Set(ctx, buildKey, build, 0)
	return status
}

// webBuild asks the web's internal port which build it serves; empty when the port is not
// configured or gives no build.
func (n *Notifier) webBuild(ctx context.Context) string {
	if n.Config.WebInternalURL == "" || n.Config.WebInternalSecret == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(n.Config.WebInternalURL, "/")+"/cache/build", http.NoBody)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+n.Config.WebInternalSecret)
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var reply struct {
		Build string `json:"build"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&reply) != nil {
		return ""
	}
	return reply.Build
}

// announce submits changed URLs to IndexNow (Bing, Yandex and others; Google does not use it).
func (n *Notifier) announce(ctx context.Context, settings store.SEOSettingsRow, urls []string) (string, int) {
	if !settings.Settings.IndexNow.Enabled || len(urls) == 0 {
		return "", 0
	}
	key, err := n.Store.SEOSecret(ctx, n.Config, "indexnow")
	if err != nil || key == "" {
		return "error: IndexNow key unavailable", 0
	}
	site, err := url.Parse(n.Config.WebBaseURL)
	if err != nil {
		return "error: invalid WEB_BASE_URL", 0
	}
	announced := 0
	for start := 0; start < len(urls); start += indexNowChunk {
		batch := urls[start:min(start+indexNowChunk, len(urls))]
		status, _, err := n.withRetry(func() (int, string, error) {
			return n.post(ctx, n.IndexNowEndpoint, "", map[string]any{
				"host": site.Host, "key": key, "keyLocation": strings.TrimRight(n.Config.WebBaseURL, "/") + "/" + key + ".txt", "urlList": batch,
			})
		})
		switch {
		case err != nil:
			return "error: IndexNow unreachable", announced
		case status == http.StatusOK:
			announced += len(batch)
		case status == http.StatusAccepted:
			// Accepted while the key file is being verified; track separately from plain success.
			announced += len(batch)
			return fmt.Sprintf("accepted: %d URLs, key verification pending", announced), announced
		default:
			return fmt.Sprintf("error: IndexNow returned %d", status), announced
		}
	}
	return fmt.Sprintf("ok: %d URLs", announced), announced
}

// withRetry retries rate limits and server errors with backoff, up to three attempts.
func (n *Notifier) withRetry(call func() (int, string, error)) (int, string, error) {
	var status int
	var body string
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		status, body, err = call()
		if err == nil && status != http.StatusTooManyRequests && status < 500 {
			return status, body, nil
		}
		if attempt < 2 {
			n.Sleep(time.Duration(1<<(2*attempt)) * time.Second)
		}
	}
	return status, body, err
}
