// Package links reads release metadata for manually curated external apps.
// It never downloads installation assets or executes upstream code.
package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/store"
)

type Service struct {
	Store     *store.Store
	GitHub    taps.GitHub
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time
}
type cachedSource struct {
	URL          string                `json:"url"`
	LastModified string                `json:"lastModified"`
	ETag         string                `json:"etag"`
	Release      *domain.GithubRelease `json:"release"`
	Body         string                `json:"body"`
}
type cacheState map[string]cachedSource

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) Sync(ctx context.Context, id int64) (map[string]any, error) {
	rows, err := s.Store.LinkApps(ctx, true)
	if err != nil {
		return nil, err
	}
	checked, failed := 0, 0
	for _, row := range rows {
		if id > 0 && row.ID != id {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		latest, source, body, etag, readErr := s.Read(ctx, row)
		if err := s.Store.LinkSyncResult(ctx, row, latest, source, body, etag, s.now(), readErr); err != nil {
			return nil, err
		}
		checked++
		if readErr != nil {
			failed++
		}
	}
	return map[string]any{"checked": checked, "failed": failed}, nil
}

func (s *Service) Read(ctx context.Context, row store.LinkApp) (*domain.GithubRelease, string, string, string, error) {
	state := cacheState{}
	if row.ETag != nil {
		_ = json.Unmarshal([]byte(*row.ETag), &state)
		if state == nil {
			state = cacheState{}
		}
	}
	targets := map[string]string{}
	if row.GithubRepo != nil {
		targets["github_release"] = "https://api.github.com/repos/" + *row.GithubRepo + "/releases/latest"
	}
	if row.SparkleFeed != nil {
		targets["sparkle"] = *row.SparkleFeed
	}
	var latest *domain.GithubRelease
	selected, body := "", ""
	// Deterministic tie-breaking and independent ETags for the two upstream sources.
	for _, source := range []string{"github_release", "sparkle"} {
		target, ok := targets[source]
		if !ok {
			delete(state, source)
			continue
		}
		if source == "github_release" && row.HasGithubFilters() {
			release, notes, err := s.filteredGithub(ctx, row)
			if err != nil {
				return nil, "", "", "", err
			}
			// Filtered lists are intentionally fetched without conditional cache
			// validators. Keep their cache identity separate from /releases/latest.
			state[source] = cachedSource{URL: strings.TrimSuffix(target, "/latest"), Release: release, Body: notes}
			latest, selected, body = release, source, notes
			continue
		}
		cached := state[source]
		if cached.URL != target {
			cached = cachedSource{URL: target}
		}
		headers := http.Header{}
		if source == "github_release" {
			if cached.LastModified != "" {
				headers.Set("If-Modified-Since", cached.LastModified)
			}
		} else if cached.ETag != "" {
			headers.Set("If-None-Match", cached.ETag)
		}
		var data []byte
		var status int
		var response http.Header
		var err error
		if source == "github_release" {
			if s.GitHub == nil {
				return nil, "", "", "", errors.New("GitHub reader is not configured")
			}
			data, status, response, err = s.GitHub.Get(ctx, target, 4<<20, headers)
		} else {
			data, status, response, err = s.appcast(ctx, target, headers)
		}
		if err != nil {
			return nil, "", "", "", err
		}
		switch status {
		case http.StatusNotModified:
			if cached.Release == nil {
				return nil, "", "", "", errors.New("upstream returned 304 without a cached release")
			}
		case http.StatusOK:
			if source == "github_release" {
				cached.Release, cached.Body, err = parseGitHub(data, row.AssetRules)
			} else {
				cached.Release, cached.Body, err = parseAppcast(data, target, row.AssetRules)
			}
			if err != nil {
				return nil, "", "", "", err
			}
			cached.ETag = response.Get("ETag")
			cached.LastModified = response.Get("Last-Modified")
		case http.StatusNotFound:
			// A repository without releases is still a useful link. Cache no release;
			// retaining the prior good metadata is preferable to blanking the catalog.
			continue
		default:
			return nil, "", "", "", fmt.Errorf("release metadata returned HTTP %d", status)
		}
		state[source] = cached
		if cached.Release != nil && (latest == nil || cached.Release.PublishedAt.After(latest.PublishedAt)) {
			latest, selected, body = cached.Release, source, cached.Body
		}
	}
	raw, err := json.Marshal(state)
	return latest, selected, body, string(raw), err
}
func (s *Service) appcast(ctx context.Context, target string, headers http.Header) ([]byte, int, http.Header, error) {
	if !domain.LinkHTTPS(target) {
		return nil, 0, nil, errors.New("appcast must be a public HTTPS URL")
	}
	client := s.HTTP
	if client == nil {
		client = fetch.NewClient(30 * time.Second)
	}
	copy := *client
	previous := copy.CheckRedirect
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !domain.LinkHTTPS(req.URL.String()) {
			return errors.New("appcast redirect must remain public HTTPS")
		}
		if len(via) > 5 {
			return errors.New("too many appcast redirects")
		}
		if previous != nil {
			return previous(req, via)
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	request.Header = headers.Clone()
	request.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	if s.UserAgent != "" {
		request.Header.Set("User-Agent", s.UserAgent)
	}
	response, err := copy.Do(request)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if len(data) > 4<<20 {
		return nil, 0, nil, errors.New("appcast response too large")
	}
	return data, response.StatusCode, response.Header, err
}

func releaseVersion(tag string) string { return strings.TrimPrefix(strings.TrimSpace(tag), "v") }
