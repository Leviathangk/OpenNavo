package assets

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/store"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

type domainRate struct {
	limiter *rate.Limiter
	last    time.Time
}
type LimitedTransport struct {
	Base    http.RoundTripper
	mu      sync.Mutex
	domains map[string]domainRate
}

func (t *LimitedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := fetch.ValidateURL(request.URL); err != nil {
		return nil, err
	}
	now := time.Now()
	t.mu.Lock()
	if t.domains == nil {
		t.domains = map[string]domainRate{}
	}
	for host, bucket := range t.domains {
		if now.Sub(bucket.last) > time.Hour {
			delete(t.domains, host)
		}
	}
	host := request.URL.Hostname()
	bucket := t.domains[host]
	if bucket.limiter == nil {
		bucket.limiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	}
	bucket.last = now
	t.domains[host] = bucket
	t.mu.Unlock()
	if err := bucket.limiter.Wait(request.Context()); err != nil {
		return nil, err
	}
	return t.Base.RoundTrip(request)
}

type SizeFetcher interface {
	Size(context.Context, string) (*int64, error)
}
type HEADFetcher struct {
	Client    *http.Client
	UserAgent string
}

func NewHEADFetcher(agent string) *HEADFetcher {
	client := fetch.NewClient(10 * time.Second)
	client.Transport = &LimitedTransport{Base: client.Transport}
	return &HEADFetcher{Client: client, UserAgent: agent}
}
func (f *HEADFetcher) Size(ctx context.Context, target string) (*int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return nil, err
	}
	if err := fetch.ValidateURL(r.URL); err != nil {
		return nil, err
	}
	r.Header.Set("User-Agent", f.UserAgent)
	response, err := f.Client.Do(r)
	if err != nil {
		return nil, errors.New("download size upstream unavailable")
	}
	_ = response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 && response.ContentLength > 0 {
		return &response.ContentLength, nil
	}
	r, err = http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("User-Agent", f.UserAgent)
	r.Header.Set("Range", "bytes=0-0")
	r.Header.Set("Accept-Encoding", "identity")
	response, err = f.Client.Do(r)
	if err != nil {
		return nil, errors.New("download size upstream unavailable")
	}
	// Do not read response bodies, especially when servers ignore Range and return entire installers.
	cancel()
	_ = response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return nil, nil
	}
	parts := contentRange.FindStringSubmatch(response.Header.Get("Content-Range"))
	if parts == nil {
		return nil, nil
	}
	total, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || total <= 0 {
		return nil, nil
	}
	return &total, nil
}

var contentRange = regexp.MustCompile(`^bytes 0-0/([0-9]+)$`)

type SizeService struct {
	Store   *store.Store
	Fetcher SizeFetcher
}

func (s *SizeService) Run(ctx context.Context, id int64) (map[string]any, error) {
	if id == 0 {
		return s.Batch(ctx)
	}
	p, err := s.Store.DownloadSizePackage(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Catalog syncs can hide or remove a package after this job was queued. Nothing is left to
		// measure, and retrying cannot bring the row back; treat it as an idempotent skip.
		return map[string]any{"skipped": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if p.Kind != "cask" || p.IsFont || p.Disabled || p.Meta.Hidden {
		return map[string]any{"skipped": true}, nil
	}
	var value *int64
	var fetchErr error
	if p.DownloadURL != nil {
		value, fetchErr = s.Fetcher.Size(ctx, *p.DownloadURL)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	changed, err := s.Store.SetDownloadSize(ctx, p, value)
	if err != nil {
		return nil, err
	}
	if changed && s.Store.Redis != nil {
		if err := cache.PublishInvalidation(ctx, s.Store.Redis, "c:*"); err != nil {
			return nil, err
		}
	}
	return map[string]any{"found": value != nil, "upstreamUnavailable": fetchErr != nil, "changed": changed}, nil
}

func (s *SizeService) Batch(ctx context.Context) (map[string]any, error) {
	// Batch jobs persist only per-package jobs so thousands of network requests cannot exceed the job timeout.
	count, err := s.Store.ScheduleDownloadSizes(ctx)
	return map[string]any{"enqueued": count}, err
}
