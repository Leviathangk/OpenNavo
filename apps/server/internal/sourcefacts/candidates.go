// Package sourcefacts reads app facts from public sources without anyone typing them in: the
// license of the GitHub repository an app comes from, and the Mac App Store listing that shares
// the app's bundle identifier. Every lookup starts from what the Homebrew definition already
// records, so a page can say where a fact came from and only ever describes what that source says.
package sourcefacts

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const maxBundleIDs = 4

var (
	githubOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	githubName  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	// GitHub paths that are site pages, not owners.
	githubReserved = []string{"about", "apps", "collections", "features", "login", "marketplace", "orgs", "settings", "site", "sponsors", "topics"}
	bundleID       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(?:\.[A-Za-z0-9_-]+)+$`)
	// Shared folders prefixed with a developer team ID ("2BUA8C4S2C.com.vendor") are not bundle identifiers.
	teamPrefixed = regexp.MustCompile(`^[A-Z0-9]{10}\.`)
	// Crash reporters and update helpers that many apps share.
	sharedFrameworks = []string{"com.plausiblelabs.", "com.crashlytics", "io.sentry", "com.bugsnag", "com.microsoft.autoupdate"}
)

// GitHubRepos lists "owner/name" repositories for an app in the order to try them: the homepage
// (usually the source repository), the download (sometimes a releases-only mirror), then the
// repository recorded on the package page.
func GitHubRepos(homepage, downloadURL, recorded string) []string {
	out := []string{}
	for _, raw := range []string{homepage, downloadURL, recorded} {
		if repo := githubRepo(raw); repo != "" && !slices.ContainsFunc(out, func(seen string) bool { return strings.EqualFold(seen, repo) }) {
			out = append(out, repo)
		}
	}
	return out
}

func githubRepo(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" && host != "raw.githubusercontent.com" && host != "codeload.github.com" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !githubOwner.MatchString(owner) || !githubName.MatchString(name) || name == "." || name == ".." || slices.Contains(githubReserved, strings.ToLower(owner)) {
		return ""
	}
	return owner + "/" + name
}

type entry struct {
	Type        string         `json:"type"`
	Declaration map[string]any `json:"declaration"`
}

// BundleIDs reads up to four bundle identifiers from a cask's uninstall and zap declarations,
// strongest evidence first: the process Homebrew quits, then sandbox containers, preferences and
// other per-app folders that macOS names after the identifier.
func BundleIDs(artifacts json.RawMessage) []string {
	var value struct {
		Entries []entry `json:"entries"`
	}
	if len(artifacts) == 0 || json.Unmarshal(artifacts, &value) != nil {
		return []string{}
	}
	score := map[string]int{}
	order := []string{}
	add := func(id string, weight int) {
		key := strings.ToLower(id)
		if !bundleID.MatchString(id) || teamPrefixed.MatchString(id) || strings.HasPrefix(key, "com.apple.") || strings.HasPrefix(key, "group.") ||
			slices.ContainsFunc(sharedFrameworks, func(prefix string) bool { return strings.HasPrefix(key, prefix) }) {
			return
		}
		if _, seen := score[key]; !seen {
			order = append(order, id)
		}
		score[key] += weight
	}
	for _, e := range value.Entries {
		if e.Type != "uninstall" && e.Type != "zap" {
			continue
		}
		for _, stanza := range objects(e.Declaration[e.Type]) {
			for _, id := range texts(stanza["quit"]) {
				add(id, 5)
			}
			for _, key := range []string{"trash", "delete", "rmdir"} {
				for _, path := range texts(stanza[key]) {
					if id, weight := pathBundleID(path); id != "" {
						add(id, weight)
					}
				}
			}
		}
	}
	slices.SortStableFunc(order, func(a, b string) int { return score[strings.ToLower(b)] - score[strings.ToLower(a)] })
	if len(order) > maxBundleIDs {
		order = order[:maxBundleIDs]
	}
	return order
}

// Folders macOS names after the bundle identifier, with how strongly each points at the app itself.
// A folder listed with a file suffix comes before the same folder without one.
var bundleFolders = []struct {
	prefix, suffix string
	weight         int
}{
	{"~/Library/Containers/", "", 3},
	{"~/Library/Preferences/", ".plist", 2},
	{"~/Library/Application Scripts/", "", 1},
	{"~/Library/Caches/", "", 1},
	{"~/Library/HTTPStorages/", ".binarycookies", 1},
	{"~/Library/HTTPStorages/", "", 1},
	{"~/Library/WebKit/", "", 1},
	{"~/Library/Saved Application State/", ".savedState", 1},
	{"~/Library/Cookies/", ".binarycookies", 1},
}

func pathBundleID(path string) (string, int) {
	if strings.ContainsAny(path, "*?[{") {
		return "", 0
	}
	for _, folder := range bundleFolders {
		rest, ok := strings.CutPrefix(path, folder.prefix)
		if !ok {
			continue
		}
		rest, _, _ = strings.Cut(rest, "/")
		if folder.suffix != "" {
			if rest, ok = strings.CutSuffix(rest, folder.suffix); !ok {
				continue
			}
		}
		if rest != "" {
			return rest, folder.weight
		}
	}
	return "", 0
}

func objects(value any) []map[string]any {
	out := []map[string]any{}
	switch typed := value.(type) {
	case map[string]any:
		out = append(out, typed)
	case []any:
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				out = append(out, object)
			}
		}
	}
	return out
}

func texts(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		out := []string{}
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

// NameMatches guards a bundle-identifier match: ignoring case, spaces and punctuation, the App
// Store name must contain one of the app's names or be contained in it ("Slack" and "Slack for
// Desktop"). A shared vendor word alone ("Microsoft") is not enough.
func NameMatches(names []string, listing string) bool {
	target := compact(listing)
	if len(target) < 3 {
		return false
	}
	for _, name := range names {
		own := compact(name)
		if len(own) >= 3 && (strings.Contains(target, own) || strings.Contains(own, target)) {
			return true
		}
	}
	return false
}

func compact(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}
