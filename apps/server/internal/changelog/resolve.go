package changelog

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/fetch"
)

type Candidate struct {
	ID                                                                           int64
	Kind, Token, Tap, Homepage, DownloadURL, RepoURL, RubySourcePath, TapGitHead string
}
type Source struct {
	Type     string
	Config   map[string]any
	Priority int
}
type RateLimitError struct{ Reset time.Time }

func (e *RateLimitError) Error() string { return "GitHub public request quota exhausted" }

type Resolver struct {
	ObserveQuota  func(context.Context, int64)
	TokenProvider func(context.Context) (string, error)
	Client        *http.Client
	UserAgent     string
}

func NewResolver(agent string) *Resolver {
	client := fetch.NewClient(15 * time.Second)
	// Commit requests do not follow redirects, preventing PAT forwarding to another domain or plaintext connection.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Resolver{Client: client, UserAgent: agent}
}

var rubySafe = regexp.MustCompile(`^[A-Za-z0-9_./+@-]+[.]rb$`)

func HomebrewSource(p Candidate) (Source, bool) {
	if p.Kind == "link" {
		return Source{}, false
	}
	// Third-party taps keep their own history; the tap sync dates their versions instead.
	if p.Tap != "" && !strings.HasPrefix(p.Tap, "homebrew/") {
		return Source{}, false
	}
	if !rubySafe.MatchString(p.RubySourcePath) || strings.Contains(p.RubySourcePath, "..") {
		return Source{}, false
	}
	repo := "Homebrew/homebrew-core"
	if p.Kind == "cask" {
		repo = "Homebrew/homebrew-cask"
	}
	return Source{Type: "homebrew_commits", Priority: 90, Config: map[string]any{"repo": repo, "path": p.RubySourcePath}}, true
}

func (r *Resolver) readRequest(ctx context.Context, target string, maximum int64, headers http.Header) ([]byte, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	if err := validateSourceURL(req.URL); err != nil {
		return nil, 0, nil, err
	}
	for key, values := range headers {
		req.Header[key] = values
	}
	req.Header.Set("User-Agent", r.UserAgent)
	if req.URL.Scheme == "https" && req.URL.Hostname() == "api.github.com" && (req.URL.Port() == "" || req.URL.Port() == "443") {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if r.TokenProvider != nil {
			token, err := r.TokenProvider(ctx)
			if err != nil {
				return nil, 0, nil, &Error{Kind: "credential_unavailable", Cause: err}
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
		}
	}
	response, err := r.Client.Do(req)
	if err != nil {
		return nil, 0, nil, &Error{Kind: "connection_failed", Cause: err}
	}
	defer func() { _ = response.Body.Close() }()
	if req.URL.Hostname() == "api.github.com" && r.ObserveQuota != nil {
		if value, err := strconv.ParseInt(response.Header.Get("X-Ratelimit-Remaining"), 10, 64); err == nil && value >= 0 {
			r.ObserveQuota(ctx, value)
		}
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotModified {
		return nil, response.StatusCode, response.Header, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, response.StatusCode, response.Header, &Error{Kind: "response_read_failed", Cause: err}
	}
	if int64(len(body)) > maximum {
		return nil, response.StatusCode, response.Header, &Error{Kind: "response_too_large"}
	}
	return body, response.StatusCode, response.Header, nil
}
func validateSourceURL(u *url.URL) error { return fetch.ValidateURL(u) }

// Get is readRequest for other GitHub readers (source facts), sharing the token, the quota
// observer and the no-redirect rule.
func (r *Resolver) Get(ctx context.Context, target string, maximum int64, headers http.Header) ([]byte, int, http.Header, error) {
	return r.readRequest(ctx, target, maximum, headers)
}

// Derive sources only from Homebrew definition paths; do not discover websites or upstream repositories over the network.
func (r *Resolver) Resolve(ctx context.Context, p Candidate) ([]Source, map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	sources := []Source{}
	if p.Kind == "cask" {
		if source, ok := HomebrewSource(p); ok {
			sources = append(sources, source)
		}
	}
	return sources, map[string]any{"homebrew": len(sources) > 0}, nil
}
