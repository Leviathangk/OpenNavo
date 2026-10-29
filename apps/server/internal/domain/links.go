package domain

import (
	"context"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/i18n"
)

// Links are opt-in on negotiated public lists. Legacy snapshot callers explicitly
// remove this capability, even if a client sends the header on those routes.
type linksKey struct{}

func WithLinks(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, linksKey{}, enabled)
}
func LinksAllowed(ctx context.Context) bool {
	enabled, _ := ctx.Value(linksKey{}).(bool)
	return enabled
}
func LinksCacheKey(ctx context.Context) string {
	if LinksAllowed(ctx) {
		return ":links"
	}
	return ""
}

type ReleaseAsset struct {
	Name   string  `json:"name"`
	URL    string  `json:"url"`
	Size   int64   `json:"size"`
	Arch   *string `json:"arch"`
	Format string  `json:"format"`
	SHA256 *string `json:"sha256"`
}
type GithubRelease struct {
	Tag         string         `json:"tag"`
	Version     string         `json:"version"`
	PublishedAt time.Time      `json:"publishedAt"`
	URL         string         `json:"url"`
	Assets      []ReleaseAsset `json:"assets"`
}
type LinkGithub struct {
	Repo   string         `json:"repo"`
	Latest *GithubRelease `json:"latest"`
}
type LinkInfo struct {
	Homepage      *string            `json:"homepage"`
	DownloadURL   *string            `json:"downloadUrl"`
	Github        *LinkGithub        `json:"github"`
	TeamID        *string            `json:"teamId"`
	Pricing       string             `json:"pricing"`
	Notes         map[string]*string `json:"notes"`
	AdoptedByCask *string            `json:"adoptedByCask"`
}
type LinkInput struct {
	Token              string             `json:"token"`
	Name               string             `json:"name"`
	SourceLocale       string             `json:"sourceLocale"`
	Homepage           *string            `json:"homepage"`
	DownloadURL        *string            `json:"downloadUrl"`
	GithubRepo         *string            `json:"githubRepo"`
	GithubTagPrefix    *string            `json:"githubTagPrefix"`
	GithubTagRegex     *string            `json:"githubTagRegex"`
	GithubAssetPattern *string            `json:"githubAssetPattern"`
	IncludePrereleases bool               `json:"includePrereleases"`
	BundleIDs          []string           `json:"bundleIds"`
	TeamID             *string            `json:"teamId"`
	Pricing            string             `json:"pricing"`
	SparkleFeed        *string            `json:"sparkleFeed"`
	AssetRules         map[string]string  `json:"assetRules"`
	Notes              map[string]*string `json:"notes"`
}

var linkToken = regexp.MustCompile(`^[a-z0-9][a-z0-9+@._-]{0,127}$`)

// ValidLinkToken reports whether token can name a link app.
func ValidLinkToken(token string) bool { return linkToken.MatchString(token) }

var linkRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
var linkTeam = regexp.MustCompile(`^[A-Z0-9]{10}$`)
var linkBundle = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+$`)

func LinkHTTPS(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 4096 && u.Scheme == "https" && u.Fragment == "" && fetch.ValidateURL(u) == nil
}
func (v *LinkInput) Validate() error {
	v.Name = strings.TrimSpace(v.Name)
	if v.SourceLocale == "" {
		v.SourceLocale = "en-US"
	}
	if !linkToken.MatchString(v.Token) {
		return Rejected("token_invalid:token")
	}
	if v.Name == "" || len(v.Name) > 256 {
		return Rejected("name_invalid:name")
	}
	if !i18n.Valid(v.SourceLocale) {
		return Rejected("locale_invalid:sourceLocale")
	}
	for _, p := range []**string{&v.Homepage, &v.DownloadURL, &v.SparkleFeed, &v.GithubRepo, &v.TeamID} {
		if *p != nil {
			s := strings.TrimSpace(**p)
			if s == "" {
				*p = nil
			} else {
				*p = &s
			}
		}
	}
	if v.Homepage == nil && v.DownloadURL == nil && v.GithubRepo == nil {
		return Rejected("link_destination_required")
	}
	for _, field := range []struct {
		key   string
		value *string
	}{{"homepage", v.Homepage}, {"downloadUrl", v.DownloadURL}, {"sparkleFeed", v.SparkleFeed}} {
		if field.value != nil && !LinkHTTPS(*field.value) {
			return Rejected("url_invalid:" + field.key)
		}
	}
	if v.GithubRepo != nil && (!linkRepo.MatchString(*v.GithubRepo) || strings.HasSuffix(*v.GithubRepo, ".git") || strings.Contains(*v.GithubRepo, "..")) {
		return Rejected("github_repo_invalid:githubRepo")
	}
	if err := v.ValidateGithubFilters(); err != nil {
		return err
	}
	if v.TeamID != nil && !linkTeam.MatchString(*v.TeamID) {
		return Rejected("team_id_invalid:teamId")
	}
	if v.Pricing == "" {
		v.Pricing = "unknown"
	}
	if !slices.Contains([]string{"free", "paid", "freemium", "subscription", "open_source", "unknown"}, v.Pricing) || len(v.BundleIDs) > 32 {
		return Rejected("pricing_or_bundle_count_invalid")
	}
	if v.BundleIDs == nil {
		v.BundleIDs = []string{}
	}
	seen := map[string]bool{}
	for _, b := range v.BundleIDs {
		if len(b) > 255 || !linkBundle.MatchString(b) || seen[b] {
			return Rejected("bundle_id_invalid_or_duplicate:bundleIds")
		}
		seen[b] = true
	}
	for k, s := range v.Notes {
		if !i18n.Valid(k) || (s != nil && len(*s) > 16384) {
			return Rejected("notes_invalid:notes")
		}
	}
	for k, s := range v.AssetRules {
		if !slices.Contains([]string{"arm64", "x86_64", "universal"}, k) || len(s) > 1024 {
			return Rejected("asset_rule_invalid:assetRules")
		}
		if _, err := regexp.Compile(s); err != nil {
			return Rejected("asset_rule_pattern_invalid:assetRules")
		}
	}
	return nil
}

// HasGithubFilters also selects the releases list when prereleases are requested:
// GitHub's /releases/latest endpoint never includes them.
func (v LinkInput) HasGithubFilters() bool {
	return v.GithubTagPrefix != nil || v.GithubTagRegex != nil || v.GithubAssetPattern != nil || v.IncludePrereleases
}

func (v LinkInput) ValidateGithubFilters() error {
	for _, field := range []struct {
		key   string
		value *string
		limit int
	}{{"githubTagPrefix", v.GithubTagPrefix, 256}, {"githubTagRegex", v.GithubTagRegex, 1024}, {"githubAssetPattern", v.GithubAssetPattern, 512}} {
		if field.value != nil && (strings.TrimSpace(*field.value) == "" || len(*field.value) > field.limit) {
			return Rejected("github_filter_length_invalid:" + field.key)
		}
	}
	if v.HasGithubFilters() && v.GithubRepo == nil {
		return Rejected("github_repo_required:githubRepo")
	}
	if v.GithubTagPrefix != nil && v.GithubTagRegex != nil {
		return Rejected("github_tag_filters_exclusive:githubTagRegex")
	}
	if v.GithubTagRegex != nil {
		// Go's regexp package uses RE2 syntax and linear-time matching; never
		// compile these expressions in Python's backtracking regex engine.
		if _, err := regexp.Compile(*v.GithubTagRegex); err != nil {
			return Rejected("github_tag_regex_invalid:githubTagRegex")
		}
	}
	if v.GithubAssetPattern != nil {
		if _, err := path.Match(*v.GithubAssetPattern, ""); err != nil {
			return Rejected("github_asset_pattern_invalid:githubAssetPattern")
		}
	}
	return nil
}
