package changelog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// GitHub release notes are external Markdown. They are stored through NormalizeMarkdown (HTML stripped,
// capped at 200 KB, matching releases.body_markdown) and rendered by @opennavo/ui's markdown-it (html:false)
// + DOMPurify pipeline, exactly like the other upstream release sources.
const (
	releasesPerPage       = 100
	releasesBackfillPages = 5       // A first fetch reads up to 500 releases so history is not page one only.
	releasesMaxBody       = 8 << 20 // One page of 100 releases with long notes and asset lists.
	releaseTitleMaxRune   = 300
	// GitHubQuotaReserve pauses github_releases fetches once the remaining primary quota falls to this
	// fraction of the limit, leaving the rest for Homebrew commit history and source facts (#64 keeps
	// the same last-tenth reserve).
	githubQuotaReserveDivisor = 10
	// Without a limit header the pause starts below this many remaining requests.
	githubQuotaFloor = 100
)

// Release is one published GitHub release mapped to a package version.
type Release struct {
	Tag, Version, Title, SourceURL string
	Body, BodyHash                 string
	Prerelease                     bool
	PublishedAt                    *time.Time
}

type githubRelease struct {
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	Body        string     `json:"body"`
	HTMLURL     string     `json:"html_url"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt *time.Time `json:"published_at"`
}

// quotaGuard remembers the last GitHub primary quota this fetcher saw so a low quota pauses the
// next github_releases fetch before it spends a request.
type quotaGuard struct {
	mu               sync.Mutex
	remaining, limit int64
	reset            time.Time
	known            bool
}

func (q *quotaGuard) observe(headers http.Header) {
	remaining, err := strconv.ParseInt(headers.Get("X-Ratelimit-Remaining"), 10, 64)
	if err != nil || remaining < 0 {
		return
	}
	limit, err := strconv.ParseInt(headers.Get("X-Ratelimit-Limit"), 10, 64)
	if err != nil || limit < 0 {
		limit = -1
	}
	reset := time.Time{}
	if epoch, err := strconv.ParseInt(headers.Get("X-Ratelimit-Reset"), 10, 64); err == nil && epoch > 0 {
		reset = time.Unix(epoch, 0)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.remaining, q.limit, q.reset, q.known = remaining, limit, reset, true
}

// paused returns the reset time while the remaining quota is inside the reserve.
func (q *quotaGuard) paused(now time.Time) (time.Time, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.known || q.reset.IsZero() || !q.reset.After(now) {
		return time.Time{}, false
	}
	floor := int64(githubQuotaFloor)
	if q.limit > 0 {
		floor = q.limit / githubQuotaReserveDivisor
	}
	if q.remaining > floor {
		return time.Time{}, false
	}
	return q.reset, true
}

var githubRepoName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

// GitHubReleasesSource builds the source for a package whose public GitHub repository was verified by
// the source facts job (#64: homepage, cask URL or recorded repository, checked public via the API).
func GitHubReleasesSource(p Candidate) (Source, bool) {
	repo := strings.TrimSpace(p.GitHubRepo)
	if !githubRepoName.MatchString(repo) || strings.Contains(repo, "..") {
		return Source{}, false
	}
	return Source{Type: "github_releases", Priority: 20, Config: map[string]any{"repo": repo}}, true
}

func (f *Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Fetcher) releases(ctx context.Context, s FetchSource) (FetchResult, error) {
	out := FetchResult{Versions: []BrewVersion{}, Releases: []Release{}}
	repo := configText(s.Config, "repo")
	if !githubRepoName.MatchString(repo) || strings.Contains(repo, "..") {
		return out, errors.New("invalid GitHub repository")
	}
	if reset, paused := f.quota.paused(f.now()); paused {
		return out, &RateLimitError{Reset: reset}
	}
	// The first fetch walks older pages so history is not limited to the newest 100 releases. Later
	// fetches read page one only: the list is newest-first, so everything older is already stored and
	// re-reading it every few hours would multiply the quota cost for unchanged notes.
	pages := 1
	if s.Backfill {
		pages = releasesBackfillPages
	}
	batch := []githubRelease{}
	digest := sha256.New()
	for page := 1; page <= pages; page++ {
		headers := http.Header{}
		if page == 1 && s.ETag != "" {
			// A 304 costs no primary quota. The list embeds asset download counters, so busy repositories
			// still change ETag often; unchanged notes are then deduplicated by body hash in the store.
			headers.Set("If-None-Match", s.ETag)
		}
		target := "https://api.github.com/repos/" + repo + "/releases?" + url.Values{
			"per_page": {strconv.Itoa(releasesPerPage)},
			"page":     {strconv.Itoa(page)},
		}.Encode()
		body, status, responseHeaders, err := f.HTTP.readRequest(ctx, target, releasesMaxBody, headers)
		if responseHeaders != nil {
			f.quota.observe(responseHeaders)
		}
		if err != nil {
			return out, err
		}
		if err := checkStatus(status, responseHeaders); err != nil {
			return out, err
		}
		if page == 1 {
			out.ETag = responseHeaders.Get("ETag")
			if status == http.StatusNotModified {
				out.NotModified = true
				if out.ETag == "" {
					out.ETag = s.ETag
				}
				return out, nil
			}
		}
		var items []githubRelease
		if err := json.Unmarshal(body, &items); err != nil || items == nil {
			return out, &Error{Kind: "invalid_json"}
		}
		if page == 1 {
			// The steady-state fetch reads page one only, so the hash is over page one: hashing the
			// backfill pages too would make every first fetch's hash differ from the next fetch's.
			digest.Write(body)
		}
		batch = append(batch, items...)
		if len(items) < releasesPerPage {
			break
		}
	}
	out.ContentHash = hex.EncodeToString(digest.Sum(nil))
	if s.ContentHash != "" && s.ContentHash == out.ContentHash {
		// The same list as last time, so nothing would be written and the visibility request that
		// gates writes has nothing to decide. This is the common case for a busy repository whose
		// ETag changes with asset download counters.
		out.NotModified = true
		out.Releases = nil
		return out, nil
	}
	if reset, paused := f.quota.paused(f.now()); paused {
		// The list itself can report a quota that has just dropped into the reserve. Storing it would
		// require the visibility request below, and that request would spend the reserve the guard is
		// protecting; fail closed and let the run after the reset fetch the list again. An unchanged
		// list never reaches this point, because the hash short-circuit above returns first.
		return FetchResult{Versions: []BrewVersion{}, Releases: []Release{}}, &RateLimitError{Reset: reset}
	}
	// The token can read private repositories, and the source facts job only notices a repository that turned
	// private on its next run. Changed notes (a 200, not a 304) are written only after the repository itself
	// still reports public; a private one answers like a missing one (404) and nothing is stored.
	meta, err := f.repoMeta(ctx, repo)
	if err != nil {
		return FetchResult{Versions: []BrewVersion{}, Releases: []Release{}}, err
	}
	if meta.private {
		return FetchResult{Versions: []BrewVersion{}, Releases: []Release{}}, &StatusError{Status: http.StatusNotFound}
	}
	out.Archived = &meta.archived
	seen, versions := map[string]bool{}, map[string]bool{}
	for _, item := range batch {
		// Drafts are unpublished; prereleases are kept and flagged (releases.is_prerelease), which the
		// version timeline already shows with a "Prerelease" chip.
		if item.Draft || item.TagName == "" || seen[item.TagName] {
			continue
		}
		version := ReleaseVersion(item.TagName, repo)
		if version == "" {
			continue
		}
		seen[item.TagName] = true
		if item.Prerelease && !IsPrereleaseVersion(version) {
			// GitHub marks it a prerelease but the tag looks final (v1.0.0): never attach it to the stable
			// version's history.
			version += "-prerelease"
		}
		// Tags that normalize to one version (v2.0 and 2.0.0) keep only the newest release, so the
		// timeline shows one entry per version.
		if key := VersionKey(version); versions[key] {
			continue
		} else {
			versions[key] = true
		}
		release := Release{Tag: truncateRunes(item.TagName, 255), Version: version, Prerelease: item.Prerelease,
			Title: truncateRunes(strings.TrimSpace(item.Name), releaseTitleMaxRune), SourceURL: releaseURL(repo, item)}
		if item.PublishedAt != nil && !item.PublishedAt.IsZero() {
			published := item.PublishedAt.UTC()
			release.PublishedAt = &published
		}
		if strings.TrimSpace(item.Body) != "" {
			release.Body, release.BodyHash = NormalizeMarkdown(item.Body)
		}
		out.Releases = append(out.Releases, release)
	}
	return out, nil
}

// Version parsing is a whitelist. A tag is an optional word prefix (release-, desktop-v, the repository name)
// and a numeric core (1.2.3), followed by at most one suffix:
//   - a known pre-release suffix (-rc1, -beta.2, -pre.1, -prerelease, -dev3, .alpha, -preview, and the
//     Python-style 2.0b3 / 1.0a1 directly after the core): kept, and the version counts as a pre-release;
//   - "+build" metadata: dropped;
//   - any other word after a separator (-hotfix, -patch1, _final) or a single letter directly after the core
//     (OpenSSL-style 1.1.1w, 2.0.0a): kept verbatim, so it never merges into the base version.
//
// Anything else (hash-like 3a4f9c1, a prefix that is itself a pre-release word such as Beta-2) is not a
// version: the release is skipped rather than attached to a guessed version.
const prereleaseWord = `(?:alpha|beta|rc|preview|prerelease|pre|dev)`

var (
	// A prefix is a run of words separated from the core by "-", "_", "/" or (monorepo tags) "@";
	// a single word may also sit directly against the digits (go1.22.0, r1.2).
	releaseTagPattern     = regexp.MustCompile(`^((?:[a-z][a-z0-9]*[-_/])*)([a-z][a-z0-9]*)?v?(\d+(?:\.\d+)*)(.*)$`)
	prereleaseSuffix      = regexp.MustCompile(`^(?:[-._]?` + prereleaseWord + `(?:[.-]?\d+)*|[ab]\d+(?:\.\d+)*)$`)
	otherSuffix           = regexp.MustCompile(`^(?:[-._][a-z][a-z0-9]*(?:[.-][a-z0-9]+)*|[-._]\d+|[a-z])$`)
	prereleasePrefixWords = regexp.MustCompile(`(?:^|[-_/])` + prereleaseWord + `\d*[-_/]`)
	// curl-8_5_0 and OpenSSL_1_1_1w spell the numeric core with underscores. Only a digit on
	// both sides is rewritten, so a separator in front of a word (feature_branch) stays intact.
	digitUnderscore = regexp.MustCompile(`(\d)_(\d)`)
	// A prerelease word with no version attached (beta2) is a label, not a version.
	barePrerelease = regexp.MustCompile(`^` + prereleaseWord + `\d*$`)
)

// parseVersion splits a lowercased tag into prefix, numeric core and suffix ("+build" dropped).
func parseVersion(tag string) (prefix, core, suffix string, ok bool) {
	// Monorepo tags name the package before the version: pkg@1.2.3, @scope/pkg@1.2.3. Everything
	// before the last "@" is a prefix the version does not need.
	if i := strings.LastIndexByte(tag, '@'); i >= 0 {
		tag = tag[i+1:]
	}
	for digitUnderscore.MatchString(tag) {
		tag = digitUnderscore.ReplaceAllString(tag, "$1.$2")
	}
	m := releaseTagPattern.FindStringSubmatch(tag)
	if m == nil {
		return "", "", "", false
	}
	prefix, core, suffix = m[1]+m[2], m[3], m[4]
	if barePrerelease.MatchString(m[2]) {
		return "", "", "", false
	}
	if i := strings.IndexByte(suffix, '+'); i >= 0 {
		suffix = suffix[:i]
	}
	if suffix != "" && !prereleaseSuffix.MatchString(suffix) && !otherSuffix.MatchString(suffix) {
		return "", "", "", false
	}
	return prefix, core, suffix, true
}

// ReleaseVersion normalizes a release tag to a package version: lowercased, "v", "release-" and repository
// name prefixes stripped, suffixes kept (v3.0-RC1 -> 3.0-rc1, v2.0b3 -> 2.0b3, v1.2.3-hotfix -> 1.2.3-hotfix,
// 1.1.1w -> 1.1.1w). Tags that are not a version return "".
func ReleaseVersion(tag, repo string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "tip" || tag == "nightly" || !strings.ContainsAny(tag, "0123456789") {
		return ""
	}
	name := strings.ToLower(repo)
	if parts := strings.Split(name, "/"); len(parts) > 1 {
		name = parts[len(parts)-1]
	}
	for _, prefix := range []string{"release-", "release/", name + "-", name + "_"} {
		if prefix != "-" && prefix != "_" {
			tag = strings.TrimPrefix(tag, prefix)
		}
	}
	prefix, core, suffix, ok := parseVersion(tag)
	if !ok || prereleasePrefixWords.MatchString(prefix) {
		return ""
	}
	return core + suffix
}

// IsPrereleaseVersion reports whether a normalized version carries a known pre-release suffix.
func IsPrereleaseVersion(version string) bool {
	_, _, suffix, ok := parseVersion(strings.ToLower(strings.TrimSpace(strings.SplitN(version, ",", 2)[0])))
	return ok && suffix != "" && prereleaseSuffix.MatchString(suffix)
}

// VersionKey compares a release version with a package version: case-insensitive, build suffix after a
// comma dropped (Homebrew "1.2,345"), trailing ".0" components ignored (tag 2.0 matches 2.0.0), every
// suffix significant (3.0-rc1 never matches 3.0, -pre never matches -prerelease). Values without a
// numeric version (Homebrew "latest") have no key and return "".
func VersionKey(version string) string {
	version = strings.ToLower(strings.TrimSpace(strings.SplitN(version, ",", 2)[0]))
	if i := strings.IndexByte(version, '+'); i >= 0 {
		version = version[:i]
	}
	version = strings.TrimPrefix(version, "v")
	if version == "" || version[0] < '0' || version[0] > '9' {
		return ""
	}
	core, suffix := version, ""
	if i := strings.IndexFunc(version, func(r rune) bool { return (r < '0' || r > '9') && r != '.' }); i >= 0 {
		core, suffix = version[:i], strings.TrimLeft(version[i:], "-._")
	}
	core = strings.TrimRight(core, ".")
	for strings.HasSuffix(core, ".0") {
		core = strings.TrimSuffix(core, ".0")
	}
	if suffix != "" {
		return core + "-" + suffix
	}
	return core
}

// repoMeta reads the repository's visibility and archived flag with the same request path and
// quota guard as the release list. Visibility decides whether notes may be stored at all; archived
// only decides how often the source is fetched.
func (f *Fetcher) repoMeta(ctx context.Context, repo string) (repoMetadata, error) {
	body, status, headers, err := f.HTTP.readRequest(ctx, "https://api.github.com/repos/"+repo, 1<<20, http.Header{})
	if headers != nil {
		f.quota.observe(headers)
	}
	if err != nil {
		return repoMetadata{}, err
	}
	if err := checkStatus(status, headers); err != nil {
		return repoMetadata{}, err
	}
	var meta struct {
		Private    *bool  `json:"private"`
		Visibility string `json:"visibility"`
		Archived   bool   `json:"archived"`
	}
	if err := json.Unmarshal(body, &meta); err != nil || meta.Private == nil {
		return repoMetadata{}, &Error{Kind: "invalid_json"}
	}
	return repoMetadata{
		private:  *meta.Private || (meta.Visibility != "" && meta.Visibility != "public"),
		archived: meta.Archived,
	}, nil
}

type repoMetadata struct{ private, archived bool }

func releaseURL(repo string, item githubRelease) string {
	prefix := "https://github.com/" + repo + "/releases/"
	if strings.HasPrefix(strings.ToLower(item.HTMLURL), strings.ToLower(prefix)) {
		if u, err := url.Parse(item.HTMLURL); err == nil && u.Scheme == "https" && strings.EqualFold(u.Host, "github.com") && u.User == nil {
			return item.HTMLURL
		}
	}
	return prefix + "tag/" + url.PathEscape(item.TagName)
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}
