package sourcefacts

import (
	"context"
	"encoding/base64"
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

// OpenSource is the published license fact for an app. Repository signals are
// stored separately in GitHubFact so the OSI decision can keep its 30-day
// schedule.
type OpenSource struct {
	Repo     string `json:"repo"`
	URL      string `json:"url"`
	License  string `json:"license"`
	Archived bool   `json:"archived"`
}

// GitHubFact is the repository record kept for every public GitHub repository
// identified by a package. License is populated only after the 30-day OSI
// check accepts the repository.
type GitHubFact struct {
	Repo     string     `json:"repo"`
	URL      string     `json:"url"`
	License  string     `json:"license,omitempty"`
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

// needsLicenseFile reports whether GitHub could not name the license but the repository can still
// publish one: it must be public, hold code, and have a canonical name to request the file for.
func (r Repository) needsLicenseFile() bool {
	return r.License != nil && r.License.SPDXID == "NOASSERTION" && r.Public() && r.Language != nil && *r.Language != ""
}

// licenseFile fetches /license and classifies it locally when GitHub's own SPDX ID is still
// "NOASSERTION". The repository ETag already avoids this request on a 304.
func (g *GitHub) licenseFile(ctx context.Context, base, repo string) (string, http.Header, error) {
	body, status, header, err := g.HTTP.Get(ctx, base+"/repos/"+repo+"/license", 1<<20, http.Header{})
	if err != nil {
		return "", nil, err
	}
	switch status {
	case http.StatusOK:
		var value struct {
			License *struct {
				SPDXID string `json:"spdx_id"`
			} `json:"license"`
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := json.Unmarshal(body, &value); err != nil {
			return "", header, fmt.Errorf("decode github license: %w", err)
		}
		if value.License != nil && value.License.SPDXID != "" && value.License.SPDXID != "NOASSERTION" && osiLicenses[value.License.SPDXID] {
			// The lookup may know the license even when the repository response did not.
			return value.License.SPDXID, header, nil
		}
		text := value.Content
		if value.Encoding == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(value.Content)
			if err != nil {
				return "", header, nil
			}
			text = string(decoded)
		}
		return classifyLicense(text), header, nil
	case http.StatusNotFound, http.StatusGone, http.StatusUnavailableForLegalReasons:
		return "", header, nil
	case http.StatusForbidden, http.StatusTooManyRequests:
		if quota(header, "X-Ratelimit-Remaining") == 0 || header.Get("Retry-After") != "" || status == http.StatusTooManyRequests {
			return "", header, ErrRateLimited
		}
		return "", header, fmt.Errorf("github license status %d", status)
	default:
		return "", header, fmt.Errorf("github license status %d", status)
	}
}

// OpenSource publishes a license only for a public repository that holds code (GitHub detects a
// language) under an OSI-approved license. GitHub reports "NOASSERTION" when its licensee cannot
// match the file exactly; Repo then classifies the license file itself and only fills the SPDX ID on
// a confident match. Releases-only repositories and unrecognized licenses publish nothing.
func (r Repository) OpenSource() (OpenSource, bool) {
	if !r.Public() || r.License == nil || !osiLicenses[r.License.SPDXID] || r.Language == nil || *r.Language == "" {
		return OpenSource{}, false
	}
	return OpenSource{Repo: r.FullName, URL: githubURL(r.FullName), License: r.License.SPDXID, Archived: r.Archived}, true
}

// Public reports whether GitHub returned a public repository whose canonical
// URL and full name agree. It intentionally does not require language or a
// license: releases-only and non-OSI repositories still publish signals.
func (r Repository) Public() bool {
	return !r.Private && r.FullName != "" && githubRepo(r.HTMLURL) != "" && strings.EqualFold(githubRepo(r.HTMLURL), r.FullName)
}

// GitHubFact returns the repository identity and current signals for any
// public repository, including one without an OSI-approved license.
func (r Repository) GitHubFact() (GitHubFact, bool) {
	if !r.Public() {
		return GitHubFact{}, false
	}
	return GitHubFact{Repo: r.FullName, URL: githubURL(r.FullName), Archived: r.Archived,
		Stars: cloneInt64(r.StargazersCount), PushedAt: cloneTime(r.PushedAt)}, true
}

func githubURL(repo string) string {
	return "https://github.com/" + repo
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
	// LicenseUnavailable reports a transient /license read failure: Repository may still be
	// published, but the OSI decision must retry sooner than its 30-day schedule.
	LicenseUnavailable bool
	Repository         Repository
	ETag               string
	NotModified        bool
	NotFound           bool
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
			if result.Repository.needsLicenseFile() {
				spdx, licenseHeader, licenseErr := g.licenseFile(ctx, base, result.Repository.FullName)
				requests++
				result.Requests = requests
				if licenseHeader != nil {
					if remaining := quota(licenseHeader, "X-Ratelimit-Remaining"); remaining >= 0 {
						result.Remaining = remaining
					}
					if limit := quota(licenseHeader, "X-Ratelimit-Limit"); limit >= 0 {
						result.Limit = limit
					}
				}
				if errors.Is(licenseErr, ErrRateLimited) {
					return result, ErrRateLimited
				}
				// A license file that cannot be read leaves the fact unset rather than failing the
				// repository signals; the service schedules a short retry instead of the 30-day one.
				if licenseErr != nil {
					result.LicenseUnavailable = true
				}
				if licenseErr == nil && spdx != "" && result.Repository.License != nil {
					result.Repository.License.SPDXID = spdx
				}
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

// licenseSignature is a license text fingerprint. All phrases must occur after normalization;
// any absent phrase means a different license. Extra copyright lines never affect a match.
type licenseSignature struct {
	spdx    string
	phrases []string
	absent  []string
}

var licenseSignatures = []licenseSignature{
	{spdx: "MIT", phrases: []string{
		"permission is hereby granted, free of charge, to any person obtaining a copy",
		`the software is provided "as is", without warranty of any kind`,
	}},
	{spdx: "ISC", phrases: []string{
		"permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted",
	}},
	{spdx: "Apache-2.0", phrases: []string{
		"apache license", "version 2.0, january 2004", "www.apache.org/licenses/license-2.0",
	}},
	{spdx: "MPL-2.0", phrases: []string{"mozilla public license version 2.0"}},
	{spdx: "GPL-3.0", phrases: []string{"gnu general public license", "version 3, 29 june 2007"}},
	{spdx: "GPL-2.0", phrases: []string{"gnu general public license", "version 2, june 1991"}},
	{spdx: "LGPL-3.0", phrases: []string{"gnu lesser general public license", "version 3, 29 june 2007"}},
	{spdx: "LGPL-2.1", phrases: []string{"gnu lesser general public license", "version 2.1, february 1999"}},
	{spdx: "BSD-3-Clause", phrases: []string{
		"redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met",
		"neither the name of",
		// BSD-4-Clause repeats these lines and adds the advertising clause; it is not BSD-3.
	}, absent: []string{"all advertising materials"}},
	{spdx: "BSD-2-Clause", phrases: []string{
		"redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met",
	}, absent: []string{"neither the name of"}},
}

// classifyLicense identifies one OSI license from its text. It returns "" when nothing matches or
// more than one does, so an ambiguous or dual-license file never becomes a published fact.
func classifyLicense(text string) string {
	normalized := strings.ToLower(strings.NewReplacer("“", `"`, "”", `"`, "‘", "'", "’", "'").Replace(text))
	normalized = strings.Join(strings.Fields(normalized), " ")
	matched := ""
	for _, signature := range licenseSignatures {
		ok := true
		for _, phrase := range signature.phrases {
			if !strings.Contains(normalized, phrase) {
				ok = false
				break
			}
		}
		for _, phrase := range signature.absent {
			if strings.Contains(normalized, phrase) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if matched != "" && matched != signature.spdx {
			return ""
		}
		matched = signature.spdx
	}
	return matched
}
