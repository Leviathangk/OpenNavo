package domain

import (
	"regexp"
	"strings"
)

var feedBundleID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(\.[A-Za-z0-9_-]+)+$`)

type UpdateFeedKey struct {
	BundleID string `json:"bundleId"`
	Source   string `json:"source"`
}

func (k UpdateFeedKey) Validate() error {
	if len(k.BundleID) > 255 || !feedBundleID.MatchString(k.BundleID) {
		return Validation()
	}
	switch k.Source {
	case "sparkle", "electron", "github", "vendor":
		return nil
	default:
		return Validation()
	}
}

// VendorResolver is declarative. No executable code, custom headers, credentials,
// shell expressions, Ruby or arbitrary transformations can enter a rule.
type VendorResolver struct {
	ScopePath       string            `json:"scopePath,omitempty"`
	Match           map[string]string `json:"match,omitempty"`
	HTTPSUpgrade    bool              `json:"httpsUpgrade,omitempty"`
	Mode            string            `json:"mode"`
	FileName        string            `json:"fileName,omitempty"`
	ChecksumForURL  bool              `json:"checksumForUrl,omitempty"`
	VersionKey      string            `json:"versionKey,omitempty"`
	VersionPath     string            `json:"versionPath,omitempty"`
	VersionPattern  string            `json:"versionPattern,omitempty"`
	DownloadPath    string            `json:"downloadPath,omitempty"`
	DownloadPattern string            `json:"downloadPattern,omitempty"`
	DownloadURL     string            `json:"downloadUrl,omitempty"`
	SHA256Path      string            `json:"sha256Path,omitempty"`
	SHA256Pattern   string            `json:"sha256Pattern,omitempty"`
	Architecture    string            `json:"architecture,omitempty"`
	// Each variant resolves its version and download together; no cross-architecture
	// version/hash mixing, even if a vendor rolls out architectures at different times.
	Variants []VendorVariant `json:"variants,omitempty"`
}
type VendorVariant struct {
	URL          string            `json:"url"`
	Architecture string            `json:"architecture"`
	Match        map[string]string `json:"match,omitempty"`
}

func (v *VendorResolver) Validate() error {
	if len(v.FileName) > 255 || strings.ContainsAny(v.FileName, "/\\\r\n") {
		return Validation()
	}
	if v.VersionKey == "" {
		v.VersionKey = "CFBundleShortVersionString"
	}
	switch v.VersionKey {
	case "CFBundleVersion", "CFBundleShortVersionString", "Wave Build Version Number":
	default:
		return Validation()
	}
	switch v.Mode {
	case "json", "text", "redirect", "sparkle", "electron":
	default:
		return Validation()
	}
	if v.Mode != "sparkle" && v.Mode != "electron" && v.VersionPath == "" && v.VersionPattern == "" {
		return Validation()
	}
	if v.Mode != "sparkle" && v.Mode != "electron" && v.Mode != "redirect" && v.DownloadPath == "" && v.DownloadPattern == "" && v.DownloadURL == "" {
		return Validation()
	}
	for _, pattern := range []string{v.VersionPattern, v.DownloadPattern, v.SHA256Pattern} {
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if len(pattern) > 2048 || err != nil || re.NumSubexp() < 1 {
			return Validation()
		}
	}
	for _, path := range []string{v.ScopePath, v.VersionPath, v.DownloadPath, v.SHA256Path} {
		if len(path) > 256 || strings.ContainsAny(path, "\r\n") {
			return Validation()
		}
	}
	if v.DownloadURL != "" && !LinkHTTPS(strings.ReplaceAll(v.DownloadURL, "{version}", "1.0")) {
		return Validation()
	}
	if !updateArchitecture(v.Architecture) || len(v.Variants) > 4 {
		return Validation()
	}
	if !validMatch(v.Match) {
		return Validation()
	}
	seen := map[string]bool{}
	for _, variant := range v.Variants {
		if !validMatch(variant.Match) || !LinkHTTPS(variant.URL) || !updateArchitecture(variant.Architecture) || variant.Architecture == "" || seen[variant.Architecture] {
			return Validation()
		}
		seen[variant.Architecture] = true
	}
	return nil
}
func updateArchitecture(a string) bool {
	return a == "" || a == "arm64" || a == "x86_64" || a == "universal"
}

type UpdateFeedInput struct {
	UpdateFeedKey
	FeedURL      string          `json:"feedUrl"`
	TeamID       *string         `json:"teamId"`
	Channel      string          `json:"channel"`
	PackageID    *int64          `json:"packageId"`
	GithubRepo   *string         `json:"githubRepo,omitempty"`
	AssetPattern *string         `json:"assetPattern,omitempty"`
	Resolver     *VendorResolver `json:"resolver,omitempty"`
}

func (v *UpdateFeedInput) Validate() error {
	if err := v.UpdateFeedKey.Validate(); err != nil {
		return err
	}
	if v.Channel == "" {
		v.Channel = "stable"
	}
	if (v.Channel != "stable" && v.Channel != "beta") || (v.TeamID != nil && !linkTeam.MatchString(*v.TeamID)) || (v.PackageID != nil && *v.PackageID <= 0) {
		return Validation()
	}
	switch v.Source {
	case "github":
		if v.TeamID == nil || v.GithubRepo == nil || !linkRepo.MatchString(*v.GithubRepo) || v.AssetPattern == nil || len(*v.AssetPattern) > 2048 || v.Resolver != nil {
			return Validation()
		}
		if _, err := regexp.Compile(*v.AssetPattern); err != nil || *v.AssetPattern == "" {
			return Validation()
		}
		v.FeedURL = "https://api.github.com/repos/" + *v.GithubRepo + "/releases/latest"
		if v.Channel == "beta" {
			v.FeedURL = "https://api.github.com/repos/" + *v.GithubRepo + "/releases?per_page=20"
		}
	case "vendor":
		if v.TeamID == nil || v.Resolver == nil || v.GithubRepo != nil || v.AssetPattern != nil {
			return Validation()
		}
		if err := v.Resolver.Validate(); err != nil {
			return err
		}
	default:
		if v.GithubRepo != nil || v.AssetPattern != nil || v.Resolver != nil {
			return Validation()
		}
	}
	if !LinkHTTPS(v.FeedURL) {
		return Validation()
	}
	return nil
}

type UpdateFile struct {
	URL          string  `json:"url"`
	Name         string  `json:"name"`
	Size         *uint64 `json:"size"`
	Architecture *string `json:"architecture"`
	SHA256       *string `json:"sha256"`
	SHA512       *string `json:"sha512"`
}
type UpdateNotes struct {
	Format string `json:"format,omitempty"`
	Body   string `json:"body,omitempty"`
	URL    string `json:"url,omitempty"`
}
type UpdateRelease struct {
	Version    string       `json:"version"`
	VersionKey string       `json:"versionKey"`
	Notes      *UpdateNotes `json:"notes"`
	MinimumOS  *string      `json:"minimumOs"`
	Files      []UpdateFile `json:"files"`
}

func validMatch(match map[string]string) bool {
	if len(match) > 8 {
		return false
	}
	for key, value := range match {
		if key == "" || len(key) > 100 || len(value) > 256 {
			return false
		}
	}
	return true
}
