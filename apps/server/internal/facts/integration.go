package facts

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Integration struct {
	Kind          string                 `json:"kind"`
	Name          string                 `json:"name"`
	AppVersion    string                 `json:"appVersion,omitempty"`
	RepositoryURL string                 `json:"repositoryUrl"`
	Version       string                 `json:"version"`
	Installation  IntegrationInstall     `json:"installation"`
	Clients       []string               `json:"clients"`
	Permissions   IntegrationPermissions `json:"permissions"`
	AppleSilicon  string                 `json:"appleSilicon"`
	Versions      []IntegrationVersion   `json:"versions"`
}
type IntegrationInstall struct {
	Method  string `json:"method"`
	Package string `json:"package"`
}
type IntegrationPermissions struct {
	Files   []IntegrationFile `json:"files"`
	Network []string          `json:"network"`
}
type IntegrationFile struct {
	Path   string `json:"path"`
	Access string `json:"access"`
}
type IntegrationVersion struct {
	Version    string    `json:"version"`
	ReleasedAt time.Time `json:"releasedAt"`
	SourceURL  string    `json:"sourceUrl"`
}

var integrationPackages = map[string]*regexp.Regexp{
	"homebrew": regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]*(/[a-z0-9][a-z0-9+._-]*/[a-z0-9][a-z0-9+._-]*)?$`),
	"uv":       regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`),
	"npm":      regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`),
}

func (v Integration) Validate(now time.Time) error {
	if !slices.Contains([]string{"mcp", "skill"}, v.Kind) || strings.TrimSpace(v.Name) == "" || utf8.RuneCountInString(v.Name) > 160 || utf8.RuneCountInString(v.AppVersion) > 100 || !ValidSourceURL(v.RepositoryURL) || !integrationVersion(v.Version) || !slices.Contains([]string{"yes", "no", "unknown"}, v.AppleSilicon) {
		return ErrInvalid
	}
	pattern, ok := integrationPackages[v.Installation.Method]
	if !ok || len(v.Installation.Package) > 200 || !pattern.MatchString(v.Installation.Package) {
		return ErrInvalid
	}
	if len(v.Clients) == 0 || len(v.Clients) > 3 || len(v.Permissions.Files) > 30 || len(v.Permissions.Network) > 30 || len(v.Versions) == 0 || len(v.Versions) > 30 {
		return ErrInvalid
	}
	for i, c := range v.Clients {
		if !slices.Contains([]string{"claude_desktop", "cursor", "codex"}, c) || slices.Contains(v.Clients[:i], c) {
			return ErrInvalid
		}
	}
	for _, f := range v.Permissions.Files {
		if f.Path == "" || utf8.RuneCountInString(f.Path) > 1024 || strings.ContainsFunc(f.Path, unicode.IsControl) || !slices.Contains([]string{"read", "write", "read_write"}, f.Access) {
			return ErrInvalid
		}
	}
	for _, raw := range v.Permissions.Network {
		u, e := url.Parse(raw)
		if e != nil || !ValidSourceURL(raw) || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return ErrInvalid
		}
	}
	found := false
	seen := map[string]bool{}
	for _, h := range v.Versions {
		if !integrationVersion(h.Version) || h.ReleasedAt.Before(earliest) || h.ReleasedAt.After(now.Add(maxSkew)) || !OfficialDocument(h.SourceURL, []string{v.RepositoryURL}) || seen[h.Version] {
			return ErrInvalid
		}
		seen[h.Version] = true
		found = found || h.Version == v.Version
	}
	if !found {
		return ErrInvalid
	}
	return nil
}
func integrationVersion(v string) bool {
	return v != "" && utf8.RuneCountInString(v) <= 100 && !strings.ContainsAny(v, "*?\r\n\x00")
}
