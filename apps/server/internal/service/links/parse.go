package links

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
)

var digestPattern = regexp.MustCompile(`^sha256:([a-fA-F0-9]{64})$`)
var armPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(arm64|aarch64|apple[._ -]?silicon)([^a-z0-9]|$)`)
var intelPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(x86_64|x64|amd64|intel)([^a-z0-9]|$)`)

func asset(name, target string, size int64, rules map[string]string) (domain.ReleaseAsset, error) {
	a := domain.ReleaseAsset{Name: name, URL: target, Size: size, Format: "other"}
	if name == "" || len(name) > 512 || !domain.LinkHTTPS(target) || size < 0 || size > 9007199254740991 {
		return a, errors.New("invalid release asset")
	}
	// Rules select an architecture only when exactly one matches; ambiguity stays null.
	arches := []string{}
	for _, arch := range []string{"arm64", "x86_64", "universal"} {
		if rule, ok := rules[arch]; ok {
			r, err := regexp.Compile(rule)
			if err != nil {
				return a, err
			}
			if r.MatchString(name) {
				arches = append(arches, arch)
			}
		}
	}
	if len(arches) == 1 {
		a.Arch = &arches[0]
	} else if len(arches) == 0 {
		arm, intel := armPattern.MatchString(name), intelPattern.MatchString(name)
		switch {
		case strings.Contains(strings.ToLower(name), "universal") || arm && intel:
			v := "universal"
			a.Arch = &v
		case arm:
			v := "arm64"
			a.Arch = &v
		case intel:
			v := "x86_64"
			a.Arch = &v
		}
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".dmg":
		a.Format = "dmg"
	case ".zip":
		a.Format = "zip"
	case ".pkg":
		a.Format = "pkg"
	}
	return a, nil
}

type githubRelease struct {
	Tag        string    `json:"tag_name"`
	Published  time.Time `json:"published_at"`
	URL        string    `json:"html_url"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func parseGitHub(data []byte, rules map[string]string) (*domain.GithubRelease, string, error) {
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, "", err
	}
	return parseGithubRelease(release, rules, false, nil)
}

func parseGithubRelease(release githubRelease, rules map[string]string, includePrereleases bool, prefix *string) (*domain.GithubRelease, string, error) {
	tag := release.Tag
	if prefix != nil {
		tag = strings.TrimPrefix(tag, *prefix)
	}
	version := releaseVersion(tag)
	if release.Draft || (release.Prerelease && !includePrereleases) || version == "" || len(version) > 256 || release.Published.IsZero() || !domain.LinkHTTPS(release.URL) || len(release.Assets) > 500 {
		return nil, "", errors.New("invalid GitHub release")
	}
	result := &domain.GithubRelease{Tag: release.Tag, Version: version, PublishedAt: release.Published, URL: release.URL, Assets: []domain.ReleaseAsset{}}
	for _, input := range release.Assets {
		a, err := asset(input.Name, input.URL, input.Size, rules)
		if err != nil {
			return nil, "", err
		}
		if match := digestPattern.FindStringSubmatch(input.Digest); len(match) == 2 {
			value := strings.ToLower(match[1])
			a.SHA256 = &value
		}
		result.Assets = append(result.Assets, a)
	}
	if len(release.Body) > 200<<10 {
		return nil, "", errors.New("release notes too large")
	}
	return result, release.Body, nil
}
func parseAppcast(data []byte, target string, rules map[string]string) (*domain.GithubRelease, string, error) {
	var rss struct {
		Items []struct {
			Date        string `xml:"pubDate"`
			Description string `xml:"description"`
			Version     string `xml:"shortVersionString"`
			Enclosures  []struct {
				URL     string `xml:"url,attr"`
				Size    int64  `xml:"length,attr"`
				Version string `xml:"version,attr"`
				Short   string `xml:"shortVersionString,attr"`
				OS      string `xml:"os,attr"`
				Delta   string `xml:"deltaFrom,attr"`
			} `xml:"enclosure"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal(data, &rss); err != nil {
		return nil, "", err
	}
	var result *domain.GithubRelease
	body := ""
	for _, item := range rss.Items {
		published, err := time.Parse(time.RFC1123Z, item.Date)
		if err != nil {
			published, err = time.Parse(time.RFC1123, item.Date)
		}
		if err != nil {
			continue
		}
		for _, enclosure := range item.Enclosures {
			if enclosure.Delta != "" || (enclosure.OS != "" && enclosure.OS != "macos") {
				continue
			}
			version := enclosure.Short
			if version == "" {
				version = item.Version
			}
			if version == "" {
				version = enclosure.Version
			}
			if version == "" || len(version) > 256 || !domain.LinkHTTPS(enclosure.URL) {
				continue
			}
			u, err := url.Parse(enclosure.URL)
			if err != nil {
				continue
			}
			a, err := asset(path.Base(u.Path), enclosure.URL, enclosure.Size, rules)
			if err != nil {
				return nil, "", err
			}
			if result == nil || published.After(result.PublishedAt) {
				result = &domain.GithubRelease{Tag: enclosure.Version, Version: version, PublishedAt: published, URL: target, Assets: []domain.ReleaseAsset{}}
				if result.Tag == "" {
					result.Tag = version
				}
				body = item.Description
			}
			if result.Version == version && result.PublishedAt.Equal(published) {
				result.Assets = append(result.Assets, a)
			}
		}
	}
	if result == nil {
		return nil, "", errors.New("appcast has no dated full macOS release")
	}
	if len(body) > 200<<10 {
		return nil, "", errors.New("appcast notes too large")
	}
	return result, body, nil
}
