package sourcefacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrRateLimited stops a batch: the source refused more requests for now.
var ErrRateLimited = errors.New("source rate limited")

// OpenSource is the published license fact for an app.
type OpenSource struct {
	Repo     string     `json:"repo"`
	URL      string     `json:"url"`
	License  string     `json:"license"`
	Archived bool       `json:"archived"`
	Stars    *int64     `json:"stars,omitempty"`
	PushedAt *time.Time `json:"pushedAt,omitempty"`
}

// Repository is the part of GitHub's repository response the decision needs.
type Repository struct {
	FullName string  `json:"full_name"`
	HTMLURL  string  `json:"html_url"`
	Private  bool    `json:"private"`
	Archived bool    `json:"archived"`
	Language *string `json:"language"`
	// StargazersCount and PushedAt are read from the same repository response as the license.
	// PushedAt is nullable in GitHub's API for repositories with no commits.
	StargazersCount *int64     `json:"stargazers_count"`
	PushedAt        *time.Time `json:"pushed_at"`
	License         *struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`
}

// OSI-approved licenses as GitHub reports them (GitHub uses "GPL-3.0" for GPL-3.0-only).
var osiLicenses = map[string]bool{
	"0BSD": true, "AFL-3.0": true, "AGPL-3.0": true, "AGPL-3.0-only": true, "AGPL-3.0-or-later": true, "Apache-2.0": true,
	"Artistic-2.0": true, "BlueOak-1.0.0": true, "BSD-2-Clause": true, "BSD-2-Clause-Patent": true, "BSD-3-Clause": true,
	"BSL-1.0": true, "CECILL-2.1": true, "ECL-2.0": true, "EPL-1.0": true, "EPL-2.0": true, "EUPL-1.1": true, "EUPL-1.2": true,
	"GPL-2.0": true, "GPL-2.0-only": true, "GPL-2.0-or-later": true, "GPL-3.0": true, "GPL-3.0-only": true, "GPL-3.0-or-later": true,
	"ISC": true, "LGPL-2.1": true, "LGPL-2.1-only": true, "LGPL-2.1-or-later": true, "LGPL-3.0": true, "LGPL-3.0-only": true,
	"LGPL-3.0-or-later": true, "LPPL-1.3c": true, "MIT": true, "MIT-0": true, "MPL-2.0": true, "MS-PL": true, "MS-RL": true,
	"MulanPSL-2.0": true, "NCSA": true, "OFL-1.1": true, "OSL-3.0": true, "PostgreSQL": true, "UPL-1.0": true,
	"Unlicense": true, "Zlib": true,
}

// OpenSource publishes a license only for a public repository that holds code (GitHub detects a
// language) under an OSI-approved license GitHub recognized. Releases-only repositories, missing
// or unrecognized licenses ("NOASSERTION") publish nothing rather than a guess.
func (r Repository) OpenSource() (OpenSource, bool) {
	if r.Private || r.License == nil || !osiLicenses[r.License.SPDXID] || r.Language == nil || *r.Language == "" {
		return OpenSource{}, false
	}
	if githubRepo(r.HTMLURL) == "" || !strings.EqualFold(githubRepo(r.HTMLURL), r.FullName) {
		return OpenSource{}, false
	}
	return OpenSource{Repo: r.FullName, URL: "https://github.com/" + r.FullName, License: r.License.SPDXID, Archived: r.Archived,
		Stars: cloneInt64(r.StargazersCount), PushedAt: cloneTime(r.PushedAt)}, true
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := value.UTC()
	return &out
}

// Getter is the changelog GitHub client: token, quota tracking and no redirects.
type Getter interface {
	Get(ctx context.Context, target string, maximum int64, headers http.Header) ([]byte, int, http.Header, error)
}

type GitHub struct {
	HTTP Getter
	// Base defaults to https://api.github.com.
	Base string
}

type RepoResult struct {
	Repository  Repository
	ETag        string
	NotModified bool
	NotFound    bool
	// Requests is the number of HTTP requests made, including a permitted rename redirect.
	Requests int
	// Remaining and Limit describe the primary quota after the request, or -1 when GitHub did not say.
	Remaining, Limit int64
}

var movedRepository = regexp.MustCompile(`^https://api\.github\.com/repositories/[0-9]+$`)

// Repo reads one repository, conditionally when an ETag is known (a 304 costs no quota).
// A renamed or transferred repository is followed once.
func (g *GitHub) Repo(ctx context.Context, repo, etag string) (RepoResult, error) {
	base := g.Base
	if base == "" {
		base = "https://api.github.com"
	}
	target := base + "/repos/" + repo
	requests := 0
	for redirects := 0; ; redirects++ {
		requests++
		headers := http.Header{}
		if etag != "" {
			headers.Set("If-None-Match", etag)
		}
		body, status, header, err := g.HTTP.Get(ctx, target, 1<<20, headers)
		if err != nil {
			return RepoResult{Requests: requests, Remaining: -1, Limit: -1}, err
		}
		result := RepoResult{Requests: requests, Remaining: quota(header, "X-Ratelimit-Remaining"), Limit: quota(header, "X-Ratelimit-Limit"), ETag: header.Get("ETag")}
		switch {
		case status == http.StatusOK:
			if err := json.Unmarshal(body, &result.Repository); err != nil {
				return result, fmt.Errorf("decode github repository: %w", err)
			}
			return result, nil
		case status == http.StatusNotModified:
			result.NotModified = true
			return result, nil
		case status == http.StatusNotFound || status == http.StatusGone || status == http.StatusUnavailableForLegalReasons:
			result.NotFound = true
			return result, nil
		case (status == http.StatusMovedPermanently || status == http.StatusTemporaryRedirect) && redirects == 0 && movedRepository.MatchString(header.Get("Location")):
			target, etag = header.Get("Location"), ""
			continue
		case status == http.StatusForbidden || status == http.StatusTooManyRequests:
			if result.Remaining == 0 || header.Get("Retry-After") != "" || status == http.StatusTooManyRequests {
				return result, ErrRateLimited
			}
			return result, fmt.Errorf("github repository status %d", status)
		default:
			return result, fmt.Errorf("github repository status %d", status)
		}
	}
}

func quota(header http.Header, name string) int64 {
	if header == nil {
		return -1
	}
	value, err := strconv.ParseInt(header.Get(name), 10, 64)
	if err != nil || value < 0 {
		return -1
	}
	return value
}
