package facts

import (
	"regexp"
	"slices"
	"strings"
)

type SecurityObservation struct {
	AppVersion    string   `json:"appVersion,omitempty"`
	Developer     string   `json:"developer,omitempty"`
	TeamID        string   `json:"teamId,omitempty"`
	Channels      []string `json:"channels"`
	OpenSource    string   `json:"openSource"`
	RepositoryURL string   `json:"repositoryUrl,omitempty"`
	ProductCPE    string   `json:"productCpe,omitempty"`
}

var teamIDPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// ExactCPEVersion accepts a conservative subset of CPE 2.3. Escaped or ambiguous components,
// wildcard products and wildcard versions never establish a match.
func ExactCPEVersion(cpe string) (string, bool) {
	parts := strings.Split(cpe, ":")
	if len(parts) != 13 || parts[0] != "cpe" || parts[1] != "2.3" || parts[2] != "a" {
		return "", false
	}
	for _, part := range parts[3:6] {
		if part == "" || part == "-" || strings.ContainsAny(part, "*?\\ \t\n\r") {
			return "", false
		}
	}
	for _, part := range parts[6:] {
		if part == "" || strings.ContainsAny(part, "\\ \t\n\r") {
			return "", false
		}
	}
	return parts[5], true
}

func (v SecurityObservation) Validate() error {
	if len(v.Developer) > 160 || len(v.AppVersion) > 100 || v.TeamID != "" && !teamIDPattern.MatchString(v.TeamID) {
		return ErrInvalid
	}
	if !slices.Contains([]string{"yes", "no", "unknown"}, v.OpenSource) || len(v.Channels) > 3 {
		return ErrInvalid
	}
	for i, c := range v.Channels {
		if !slices.Contains([]string{"homebrew", "vendor", "app_store"}, c) || slices.Contains(v.Channels[:i], c) {
			return ErrInvalid
		}
	}
	if v.RepositoryURL != "" && !ValidSourceURL(v.RepositoryURL) || v.OpenSource == "yes" && v.RepositoryURL == "" {
		return ErrInvalid
	}
	if v.ProductCPE != "" {
		version, ok := ExactCPEVersion(v.ProductCPE)
		if !ok || v.AppVersion == "" || version != v.AppVersion {
			return ErrInvalid
		}
	}
	return nil
}

type Vulnerability struct {
	ID         string `json:"id"`
	ProductCPE string `json:"productCpe"`
	AppVersion string `json:"appVersion"`
	SourceURL  string `json:"sourceUrl"`
}
