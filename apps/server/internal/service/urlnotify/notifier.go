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
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
)

const (
	defaultSettle   = 20 * time.Second
	defaultBatch    = 2000
	invalidateChunk = 2000
	purgeChunk      = 30
	// Above this many URLs per run, Cloudflare purging is skipped and edge TTLs refresh pages.
	maxPurgeURLs  = 3000
	indexNowChunk = 10000
	lockKey       = "opennavo:url-notify:lock"
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

// Run handles one batch. The cursor only advances once page caches are refreshed; Cloudflare and
// IndexNow failures are recorded for the admin page and do not block later changes.
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
	before := n.Now().Add(-n.Settle)
	end, err := n.Store.URLChangeCursorEnd(ctx, state.Cursor, before, n.Batch)
	if err != nil {
		return result, err
	}
	pages, err := n.Store.PendingContentURLChanges(ctx, state.ContentCursor, before, n.Batch)
	if err != nil || end == state.Cursor && len(pages) == 0 {
		return result, err
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
	state.LastPurgeStatus, result.Purged = n.purge(ctx, settings, plan.PurgeURLs)
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

// purge clears Cloudflare's copies while HTML edge caching is on; without it Cloudflare holds no HTML.
func (n *Notifier) purge(ctx context.Context, settings store.SEOSettingsRow, urls []string) (string, int) {
	cf := settings.Settings.Cloudflare
	if !settings.Settings.EdgeCache.Enabled || !cf.PurgeEnabled || cf.ZoneID == "" || len(urls) == 0 {
		return "", 0
	}
	if len(urls) > maxPurgeURLs {
		return fmt.Sprintf("skipped: %d URLs (edge TTL refreshes them)", len(urls)), 0
	}
	token, err := n.Store.SEOSecret(ctx, n.Config, "cloudflare")
	if err != nil || token == "" {
		return "error: Cloudflare token unavailable", 0
	}
	endpoint := n.CloudflareAPI + "/zones/" + url.PathEscape(cf.ZoneID) + "/purge_cache"
	purged := 0
	for start := 0; start < len(urls); start += purgeChunk {
		status, body, err := n.withRetry(func() (int, string, error) {
			return n.post(ctx, endpoint, token, map[string]any{"files": urls[start:min(start+purgeChunk, len(urls))]})
		})
		if err != nil || status != http.StatusOK || !strings.Contains(body, `"success":true`) {
			return fmt.Sprintf("error: Cloudflare returned %d after %d URLs", status, purged), purged
		}
		purged += min(purgeChunk, len(urls)-start)
	}
	return fmt.Sprintf("ok: %d URLs", purged), purged
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
