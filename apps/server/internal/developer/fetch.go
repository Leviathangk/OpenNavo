// Package developer fetches public metadata for a developer-submitted link. It never writes.
package developer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/opennavo/opennavo/server/internal/fetch"
)

const userAgent = "OpenNavoBot/1.0 (+https://opennavo.com/bot)"

var (
	githubPath  = regexp.MustCompile(`^/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)
	appStoreID  = regexp.MustCompile(`/id(\d+)`)
	emailFormat = regexp.MustCompile(`(?i)^[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// ValidEmail is the public submission contact check, kept next to the fetcher for handler reuse.
func ValidEmail(value string) bool {
	return len(value) <= 254 && emailFormat.MatchString(value)
}

type Metadata struct {
	Source      string   `json:"source"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Developer   string   `json:"developer"`
	Homepage    string   `json:"homepage"`
	RepoURL     string   `json:"repoUrl"`
	DownloadURL string   `json:"downloadUrl"`
	IconURL     string   `json:"iconUrl"`
	Version     string   `json:"version"`
	MinMacOS    string   `json:"minMacos"`
	Category    string   `json:"category"`
	License     string   `json:"license"`
	Warnings    []string `json:"warnings"`
}

type Client struct {
	HTTP *http.Client
	Now  func() time.Time
}

func NewClient() *Client {
	// The shared client resolves once, rejects any non-public address and dials the
	// validated IP, so DNS rebinding cannot swap in a private host between checks.
	return &Client{HTTP: fetch.NewClient(8 * time.Second)}
}

func (c *Client) client() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return NewClient().HTTP
}

// Fetch recognizes GitHub repository, App Store and official website links.
func (c *Client) Fetch(ctx context.Context, raw string) (Metadata, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Metadata{}, errors.New("only http(s) links are supported")
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "github.com", "www.github.com":
		return c.fetchGitHub(ctx, u)
	case "apps.apple.com", "itunes.apple.com":
		return c.fetchAppStore(ctx, u)
	default:
		return c.fetchWebsite(ctx, u)
	}
}

func (c *Client) fetchGitHub(ctx context.Context, u *url.URL) (Metadata, error) {
	match := githubPath.FindStringSubmatch(u.Path)
	if len(match) != 3 {
		return Metadata{}, errors.New("use a GitHub repository link, not a profile or file")
	}
	owner, repo := match[1], match[2]
	var data struct {
		Name        string `json:"name"`
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		Homepage    string `json:"homepage"`
		HTMLURL     string `json:"html_url"`
		Default     string `json:"default_branch"`
		License     *struct {
			SPDX string `json:"spdx_id"`
		} `json:"license"`
		Owner struct {
			Login     string `json:"login"`
			AvatarURL string `json:"avatar_url"`
		} `json:"owner"`
		Topics []string `json:"topics"`
	}
	if err := c.getJSON(ctx, "https://api.github.com/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), &data); err != nil {
		return Metadata{}, fmt.Errorf("read the GitHub repository: %w", err)
	}
	m := Metadata{
		Source:    "github",
		Name:      data.Name,
		Summary:   data.Description,
		Developer: data.Owner.Login,
		Homepage:  data.Homepage,
		RepoURL:   data.HTMLURL,
		IconURL:   data.Owner.AvatarURL,
		Category:  githubCategory(data.Topics),
	}
	if data.License != nil {
		m.License = data.License.SPDX
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.getJSON(ctx, "https://api.github.com/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/releases/latest", &release); err == nil {
		m.Version = strings.TrimPrefix(release.TagName, "v")
		if m.DownloadURL == "" {
			m.DownloadURL = release.HTMLURL
		}
	} else {
		m.Warnings = append(m.Warnings, "no recent release was found; enter the version manually")
	}
	if m.Homepage == "" && m.RepoURL != "" {
		m.Homepage = m.RepoURL
	}
	return m, nil
}

func (c *Client) fetchAppStore(ctx context.Context, u *url.URL) (Metadata, error) {
	match := appStoreID.FindStringSubmatch(u.Path)
	if len(match) != 2 {
		return Metadata{}, errors.New("the App Store link has no numeric app id")
	}
	var data struct {
		Results []struct {
			TrackName        string `json:"trackName"`
			Description      string `json:"description"`
			SellerName       string `json:"sellerName"`
			TrackViewURL     string `json:"trackViewUrl"`
			ArtworkURL512    string `json:"artworkUrl512"`
			ArtworkURL100    string `json:"artworkUrl100"`
			Version          string `json:"version"`
			MinimumOSVersion string `json:"minimumOsVersion"`
			PrimaryGenreName string `json:"primaryGenreName"`
		} `json:"results"`
	}
	if err := c.getJSON(ctx, "https://itunes.apple.com/lookup?id="+url.QueryEscape(match[1])+"&country=us", &data); err != nil {
		return Metadata{}, fmt.Errorf("read the App Store: %w", err)
	}
	if len(data.Results) == 0 {
		return Metadata{}, errors.New("the App Store returned no app for this link")
	}
	item := data.Results[0]
	icon := item.ArtworkURL512
	if icon == "" {
		icon = item.ArtworkURL100
	}
	return Metadata{
		Source:      "appstore",
		Name:        item.TrackName,
		Summary:     firstLine(item.Description),
		Description: item.Description,
		Developer:   item.SellerName,
		Homepage:    item.TrackViewURL,
		DownloadURL: item.TrackViewURL,
		IconURL:     icon,
		Version:     item.Version,
		MinMacOS:    item.MinimumOSVersion,
		Category:    strings.ToLower(strings.ReplaceAll(item.PrimaryGenreName, " ", "-")),
	}, nil
}

func (c *Client) fetchWebsite(ctx context.Context, u *url.URL) (Metadata, error) {
	if err := validatePublicURL(u.String()); err != nil {
		return Metadata{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Metadata{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := c.client().Do(req)
	if err != nil {
		return Metadata{}, fmt.Errorf("read the website: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Metadata{}, fmt.Errorf("the website returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Metadata{}, err
	}
	meta := parseWebsiteHTML(string(body), resp.Request.URL.String())
	meta.Source = "website"
	if meta.Name == "" {
		return meta, errors.New("the page has no title; enter the details manually")
	}
	return meta, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("too many requests; try again later")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(target)
}

func validatePublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("only public http(s) URLs are supported")
	}
	if err := fetch.ValidateURL(u); err != nil {
		return errors.New("private hosts are not allowed")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("private hosts are not allowed")
	}
	return nil
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateRunes(line, 240)
		}
	}
	return ""
}

// truncateRunes cuts on rune boundaries so CJK text never ends in half a character.
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func githubCategory(topics []string) string {
	for _, topic := range topics {
		switch strings.ToLower(topic) {
		case "macos", "mac", "swift", "terminal", "developer-tools":
			if strings.ToLower(topic) == "terminal" {
				return "terminal"
			}
			return "developer-tools"
		}
	}
	return ""
}

// parseWebsiteHTML extracts Open Graph, meta description and schema.org SoftwareApplication.
func parseWebsiteHTML(source, finalURL string) Metadata {
	meta := Metadata{Homepage: finalURL}
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return meta
	}
	var title string
	var jsonLD []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "title":
				title = strings.TrimSpace(textContent(node))
			case "meta":
				name := strings.ToLower(attr(node, "property"))
				if name == "" {
					name = strings.ToLower(attr(node, "name"))
				}
				content := strings.TrimSpace(attr(node, "content"))
				if content == "" {
					break
				}
				switch name {
				case "og:title":
					meta.Name = content
				case "og:description":
					meta.Summary = content
				case "og:site_name":
					meta.Developer = content
				case "og:image":
					meta.IconURL = content
				case "description":
					if meta.Summary == "" {
						meta.Summary = content
					}
				}
			case "script":
				if strings.Contains(strings.ToLower(attr(node, "type")), "ld+json") {
					jsonLD = append(jsonLD, textContent(node))
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if meta.Name == "" {
		meta.Name = title
	}
	for _, raw := range jsonLD {
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			continue
		}
		if object := findSoftwareApplication(value); object != nil {
			if meta.Name == "" {
				meta.Name = stringValue(object["name"])
			}
			if meta.Summary == "" {
				meta.Summary = stringValue(object["description"])
			}
			if meta.Developer == "" {
				meta.Developer = authorName(object["author"])
			}
			if meta.Version == "" {
				meta.Version = stringValue(object["softwareVersion"])
			}
			if meta.Category == "" {
				meta.Category = strings.ToLower(strings.ReplaceAll(stringValue(object["applicationCategory"]), " ", "-"))
			}
			if meta.Homepage == "" {
				meta.Homepage = stringValue(object["url"])
			}
			if meta.IconURL == "" {
				meta.IconURL = stringValue(object["image"])
			}
		}
	}
	meta.Summary = truncateRunes(meta.Summary, 500)
	return meta
}

func findSoftwareApplication(value any) map[string]any {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if found := findSoftwareApplication(item); found != nil {
				return found
			}
		}
	case map[string]any:
		typeValue := strings.ToLower(stringValue(typed["@type"]))
		if typeValue == "softwareapplication" || typeValue == "mobileapplication" || typeValue == "webapplication" {
			return typed
		}
		if graph, ok := typed["@graph"]; ok {
			return findSoftwareApplication(graph)
		}
	}
	return nil
}

func authorName(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		return stringValue(typed["name"])
	case []any:
		if len(typed) > 0 {
			return authorName(typed[0])
		}
	}
	return ""
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func textContent(node *html.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			builder.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func attr(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}
	return ""
}
