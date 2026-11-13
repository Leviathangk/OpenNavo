package developer

import (
	"net/url"
	"strings"
)

// InferSourceKind names the metadata source behind a submission link. An explicit
// source wins; otherwise the host decides, and unknown hosts count as websites.
func InferSourceKind(explicit, rawURL string) string {
	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case "github", "appstore", "website":
		return strings.ToLower(strings.TrimSpace(explicit))
	}
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "website"
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "github.com", "www.github.com":
		return "github"
	case "apps.apple.com", "itunes.apple.com":
		return "appstore"
	default:
		return "website"
	}
}
