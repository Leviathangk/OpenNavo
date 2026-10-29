package updatefeeds

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"go.yaml.in/yaml/v3"
)

const metadataLimit = 2 << 20

var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func ptr[T any](v T) *T { return &v }
func architecture(name string) *string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "universal"):
		return ptr("universal")
	case strings.Contains(name, "arm64") || strings.Contains(name, "aarch64"):
		return ptr("arm64")
	case strings.Contains(name, "x64") || strings.Contains(name, "x86_64") || strings.Contains(name, "amd64") || strings.Contains(name, "intel"):
		return ptr("x86_64")
	default:
		return nil
	}
}
func fileName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return path.Base(u.Path)
}
func supportedFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".zip", ".dmg", ".pkg":
		return true
	default:
		return false
	}
}
func validateRelease(r *domain.UpdateRelease, source string) error {
	if r == nil || r.Version == "" || len(r.Version) > 128 || strings.ContainsAny(r.Version, "\r\n\x00") || len(r.Files) == 0 || len(r.Files) > 100 {
		return errors.New("no valid update release")
	}
	for _, f := range r.Files {
		if !domain.LinkHTTPS(f.URL) || !supportedFile(f.Name) {
			return errors.New("invalid download URL or archive type")
		}
		if f.SHA256 != nil && !sha256Pattern.MatchString(*f.SHA256) {
			return errors.New("invalid sha256")
		}
		if f.SHA512 != nil {
			b, err := base64.StdEncoding.DecodeString(*f.SHA512)
			if err != nil || len(b) != 64 {
				return errors.New("invalid sha512")
			}
		}
		if source == "github" && f.SHA256 == nil || source == "electron" && f.SHA512 == nil {
			return errors.New("missing required digest")
		}
	}
	return nil
}
func (s *Service) github(ctx context.Context, row store.UpdateFeed) (*domain.UpdateRelease, string, error) {
	client := s.GitHub
	if client == nil {
		client = pipeline.NewResolver(s.UserAgent)
	} // Anonymous, never a desktop token.
	headers := http.Header{}
	if row.LastModified != nil && row.VerifiedAt != nil && row.Release != nil {
		headers.Set("If-Modified-Since", *row.LastModified)
	}
	body, status, response, err := client.Get(ctx, row.FeedURL, 4<<20, headers)
	if err != nil {
		return nil, "", err
	}
	modified := response.Get("Last-Modified")
	if status == 304 {
		if row.Release == nil || row.VerifiedAt == nil {
			return nil, "", errors.New("GitHub 304 without verified release")
		}
		return row.Release, headers.Get("If-Modified-Since"), nil
	}
	if status != 200 {
		return nil, "", fmt.Errorf("GitHub release HTTP %d", status)
	}
	release, err := parseGitHub(body, row)
	return release, modified, err
}

type githubRelease struct {
	Tag        string    `json:"tag_name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name   string  `json:"name"`
		URL    string  `json:"browser_download_url"`
		Digest *string `json:"digest"`
		Size   uint64  `json:"size"`
	} `json:"assets"`
}

func parseGitHub(body []byte, row store.UpdateFeed) (*domain.UpdateRelease, error) {
	if row.GithubRepo == nil || row.AssetPattern == nil {
		return nil, errors.New("GitHub rule requires repository and filename regex")
	}
	var choices []githubRelease
	if row.Channel == "beta" {
		if err := json.Unmarshal(body, &choices); err != nil {
			return nil, err
		}
	} else {
		var v githubRelease
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		choices = []githubRelease{v}
	}
	var chosen *githubRelease
	for i := range choices {
		r := &choices[i]
		if r.Draft || r.Prerelease && row.Channel != "beta" {
			continue
		}
		if chosen == nil || r.Published.After(chosen.Published) {
			chosen = r
		}
	}
	if chosen == nil {
		return nil, errors.New("no GitHub release in channel")
	}
	pattern, err := regexp.Compile(*row.AssetPattern)
	if err != nil {
		return nil, err
	}
	out := &domain.UpdateRelease{Version: strings.TrimPrefix(strings.TrimSpace(chosen.Tag), "v"), VersionKey: "CFBundleShortVersionString", Files: []domain.UpdateFile{}}
	if chosen.Body != "" {
		out.Notes = &domain.UpdateNotes{Format: "markdown", Body: chosen.Body}
	}
	for _, a := range chosen.Assets {
		if !pattern.MatchString(a.Name) || !supportedFile(a.Name) || a.Digest == nil || !strings.HasPrefix(*a.Digest, "sha256:") {
			continue
		}
		digest := strings.TrimPrefix(*a.Digest, "sha256:")
		u, err := url.Parse(a.URL)
		if err != nil || !domain.LinkHTTPS(a.URL) || u.Host != "github.com" || !strings.HasPrefix(u.Path, "/"+*row.GithubRepo+"/releases/download/") || !sha256Pattern.MatchString(digest) {
			continue
		}
		out.Files = append(out.Files, domain.UpdateFile{URL: a.URL, Name: a.Name, Size: &a.Size, Architecture: architecture(a.Name), SHA256: ptr(strings.ToLower(digest))})
	}
	return out, validateRelease(out, "github")
}

// No aliases, application tags, additional documents or unbounded recursion.
func parseElectron(body []byte, base string) (*domain.UpdateRelease, error) {
	if len(body) > metadataLimit {
		return nil, errors.New("electron YAML size limit")
	}
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple YAML documents")
	}
	nodes := 0
	var visit func(*yaml.Node, int) error
	visit = func(n *yaml.Node, depth int) error {
		nodes++
		if nodes > 20000 || depth > 16 || n.Kind == yaml.AliasNode || n.Anchor != "" || strings.HasPrefix(n.Tag, "!") && !strings.HasPrefix(n.Tag, "!!") {
			return errors.New("unsafe Electron YAML")
		}
		for _, child := range n.Content {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(&root, 0); err != nil {
		return nil, err
	}
	var doc struct {
		Version      string `yaml:"version"`
		ReleaseNotes string `yaml:"releaseNotes"`
		MinimumOS    string `yaml:"minimumSystemVersion"`
		Files        []struct {
			URL    string `yaml:"url"`
			SHA512 string `yaml:"sha512"`
			Size   uint64 `yaml:"size"`
		} `yaml:"files"`
		Path   string `yaml:"path"`
		SHA512 string `yaml:"sha512"`
	}
	if err := root.Decode(&doc); err != nil {
		return nil, err
	}
	out := &domain.UpdateRelease{Version: doc.Version, VersionKey: "CFBundleShortVersionString", Files: []domain.UpdateFile{}}
	if doc.ReleaseNotes != "" {
		out.Notes = &domain.UpdateNotes{Format: "markdown", Body: doc.ReleaseNotes}
	}
	if doc.MinimumOS != "" {
		out.MinimumOS = &doc.MinimumOS
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	add := func(raw, hash string, size uint64) error {
		v, err := u.Parse(raw)
		if err != nil {
			return err
		}
		name := fileName(v.String())
		if !supportedFile(name) {
			return nil
		}
		file := domain.UpdateFile{URL: v.String(), Name: name, Architecture: architecture(name), SHA512: &hash}
		if size > 0 {
			file.Size = &size
		}
		out.Files = append(out.Files, file)
		return nil
	}
	for _, f := range doc.Files {
		if err := add(f.URL, f.SHA512, f.Size); err != nil {
			return nil, err
		}
	}
	if len(doc.Files) == 0 && doc.Path != "" {
		if err := add(doc.Path, doc.SHA512, 0); err != nil {
			return nil, err
		}
	}
	return out, validateRelease(out, "electron")
}

// Metadata only. For redirect recipes HEAD follows public HTTPS redirects and
// never downloads the installation artifact while discovering its version.
func (s *Service) vendorFetch(ctx context.Context, target, mode string) ([]byte, string, error) {
	if !domain.LinkHTTPS(target) {
		return nil, "", errors.New("vendor HTTPS required")
	}
	client := s.HTTP
	if client == nil {
		client = fetch.NewClient(30 * time.Second)
	}
	copy := *client
	previous := client.CheckRedirect
	copy.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !domain.LinkHTTPS(r.URL.String()) {
			return errors.New("unsafe vendor redirect")
		}
		if previous != nil {
			return previous(r, via)
		}
		return nil
	}
	method := http.MethodGet
	if mode == "redirect" {
		method = http.MethodHead
	}
	request, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", s.UserAgent)
	response, err := copy.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return nil, "", fmt.Errorf("vendor HTTP %d", response.StatusCode)
	}
	final := target
	if response.Request != nil {
		final = response.Request.URL.String()
	}
	if mode == "redirect" {
		return []byte(final), final, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, metadataLimit+1))
	if len(body) > metadataLimit {
		return nil, "", errors.New("vendor metadata size limit")
	}
	return body, final, err
}
func jsonNode(root any, selector string) any {
	value := root
	for _, part := range strings.Split(selector, ".") {
		switch v := value.(type) {
		case map[string]any:
			value = v[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			value = v[i]
		default:
			return nil
		}
	}
	return value
}
func jsonValue(root any, selector string) string {
	switch v := jsonNode(root, selector).(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	default:
		return ""
	}
}

func extract(body []byte, root any, selector, pattern string) string {
	if selector != "" {
		return jsonValue(root, selector)
	}
	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return ""
		}
		match := re.FindSubmatch(body)
		if len(match) > 1 {
			return html.UnescapeString(string(match[1]))
		}
	}
	return ""
}
func parseVendor(body []byte, final string, rule domain.VendorResolver, channel string) (*domain.UpdateRelease, error) {
	release, err := parseVendorMetadata(body, final, rule, channel)
	if err == nil && rule.Architecture != "" {
		// Apply the configured architecture after every parser has validated its
		// release, including parsers that produce multiple download files.
		for i := range release.Files {
			release.Files[i].Architecture = ptr(rule.Architecture)
		}
	}
	return release, err
}

func parseVendorMetadata(body []byte, final string, rule domain.VendorResolver, channel string) (*domain.UpdateRelease, error) {
	if rule.Mode == "sparkle" {
		release, err := vendorSparkle(body, channel)
		if err == nil && rule.VersionPattern != "" {
			release.Version = extract([]byte(release.Version), nil, "", rule.VersionPattern)
			err = validateRelease(release, "vendor")
		}
		return release, err
	}
	if rule.Mode == "electron" {
		return parseElectron(body, final)
	}
	var root any
	if rule.Mode == "json" {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		if err := d.Decode(&root); err != nil {
			return nil, err
		}
	}
	if rule.ScopePath != "" {
		scope := jsonNode(root, rule.ScopePath)
		if entries, ok := scope.([]any); ok {
			var selected any
			count := 0
			for _, entry := range entries {
				obj, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				matches := true
				for key, value := range rule.Match {
					if obj[key] != value {
						matches = false
						break
					}
				}
				if matches {
					selected = entry
					count++
				}
			}
			if count != 1 {
				return nil, errors.New("vendor scope must select exactly one record")
			}
			scope = selected
		}
		root = scope
	}
	v := strings.TrimSpace(extract(body, root, rule.VersionPath, rule.VersionPattern))
	if rule.VersionPath != "" && rule.VersionPattern != "" {
		v = extract([]byte(v), nil, "", rule.VersionPattern)
	}
	v = strings.TrimLeft(v, "vV")
	target := extract(body, root, rule.DownloadPath, rule.DownloadPattern)
	if rule.DownloadURL != "" {
		target = strings.ReplaceAll(rule.DownloadURL, "{version}", url.PathEscape(v))
	}
	if rule.Mode == "redirect" {
		target = final
	}
	if rule.HTTPSUpgrade && strings.HasPrefix(target, "http://") {
		target = "https://" + strings.TrimPrefix(target, "http://")
	}
	file := domain.UpdateFile{URL: target, Name: fileName(target), Architecture: architecture(fileName(target))}
	if rule.FileName != "" {
		file.Name = rule.FileName
	}
	if hash := extract(body, root, rule.SHA256Path, rule.SHA256Pattern); hash != "" {
		file.SHA256 = &hash
	} else if rule.SHA256Path != "" || rule.SHA256Pattern != "" {
		return nil, errors.New("declared vendor sha256 missing")
	}
	if rule.ChecksumForURL {
		if root == nil {
			if err := json.Unmarshal(body, &root); err != nil {
				return nil, err
			}
		}
		digest, err := checksumForURL(root, target)
		if err != nil {
			return nil, err
		}
		file.SHA256 = &digest
	}
	out := &domain.UpdateRelease{Version: v, VersionKey: rule.VersionKey, Files: []domain.UpdateFile{file}}
	return out, validateRelease(out, "vendor")
}
func vendorSparkle(body []byte, channel string) (*domain.UpdateRelease, error) {
	if err := ValidateSparkle(body); err != nil {
		return nil, err
	}
	var doc struct {
		Items []struct {
			MinimumOS string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle minimumSystemVersion"`
			Version   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString"`
			Channel   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle channel"`
			Build     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version"`
			Enclosure struct {
				MinimumOS string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle minimumSystemVersion,attr"`
				URL       string `xml:"url,attr"`
				Version   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString,attr"`
				Build     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version,attr"`
			} `xml:"enclosure"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var best *domain.UpdateRelease
	for _, item := range doc.Items {
		// An unlabeled item is a stable release and remains eligible for beta users.
		itemChannel := strings.TrimSpace(item.Channel)
		if itemChannel != "" && itemChannel != "stable" && itemChannel != channel {
			continue
		}
		v := item.Version
		if v == "" {
			v = item.Enclosure.Version
		}
		if v == "" || !domain.LinkHTTPS(item.Enclosure.URL) || !supportedFile(fileName(item.Enclosure.URL)) {
			continue
		}
		r := &domain.UpdateRelease{Version: v, VersionKey: "CFBundleShortVersionString", Files: []domain.UpdateFile{{URL: item.Enclosure.URL, Name: fileName(item.Enclosure.URL), Architecture: architecture(item.Enclosure.URL)}}}
		minimum := strings.TrimSpace(item.MinimumOS)
		if minimum == "" {
			minimum = strings.TrimSpace(item.Enclosure.MinimumOS)
		}
		if minimum != "" {
			r.MinimumOS = &minimum
		}
		if best == nil {
			best = r
		} else if c, ok := homebrew.CompareVersion(v, best.Version); ok && c > 0 {
			best = r
		}
	}
	return best, validateRelease(best, "vendor")
}
func (s *Service) vendor(ctx context.Context, row store.UpdateFeed) (*domain.UpdateRelease, error) {
	if row.Resolver == nil {
		return nil, errors.New("vendor resolver required")
	}
	variants := row.Resolver.Variants
	if len(variants) == 0 {
		variants = []domain.VendorVariant{{URL: row.FeedURL, Architecture: row.Resolver.Architecture}}
	}
	var result *domain.UpdateRelease
	for _, variant := range variants {
		body, final, err := s.vendorFetch(ctx, variant.URL, row.Resolver.Mode)
		if err != nil {
			return nil, err
		}
		rule := *row.Resolver
		rule.Architecture = variant.Architecture
		if variant.Match != nil {
			rule.Match = variant.Match
		}
		release, err := parseVendor(body, final, rule, row.Channel)
		if err != nil {
			return nil, err
		}
		if row.TeamID == nil {
			for _, file := range release.Files {
				if file.SHA256 == nil && file.SHA512 == nil {
					return nil, errors.New("unsigned vendor release requires a publisher digest")
				}
			}
		}
		switch {
		case result == nil:
			result = release
		case result.Version == release.Version:
			// MinimumOS is release-wide; merging different constraints would make
			// eligibility depend on variant order. Missing is not equivalent to known.
			if (result.MinimumOS == nil) != (release.MinimumOS == nil) ||
				result.MinimumOS != nil && *result.MinimumOS != *release.MinimumOS {
				return nil, errors.New("vendor architecture minimum system versions differ; cannot merge release")
			}
			result.Files = append(result.Files, release.Files...)
		default:
			return nil, errors.New("vendor architecture versions differ; await coherent release")
		}
	}
	return result, nil
}

func checksumForURL(root any, target string) (string, error) {
	found := ""
	count := 0
	var walk func(any, int)
	walk = func(v any, depth int) {
		if depth > 16 {
			return
		}
		switch value := v.(type) {
		case map[string]any:
			if value["url"] == target {
				if hash, ok := value["sha256"].(string); ok {
					found = hash
					count++
				}
			}
			for _, child := range value {
				walk(child, depth+1)
			}
		case []any:
			for _, child := range value {
				walk(child, depth+1)
			}
		}
	}
	walk(root, 0)
	if count != 1 || !sha256Pattern.MatchString(found) {
		return "", errors.New("missing or ambiguous artifact sha256")
	}
	return found, nil
}

// ResolveVendor exposes the pure metadata boundary to the read-only probe and
// the scheduled sample workflow. It never fetches an installation artifact.
func (s *Service) ResolveVendor(ctx context.Context, input domain.UpdateFeedInput) (*domain.UpdateRelease, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	return s.vendor(ctx, store.UpdateFeed{UpdateFeedInput: input})
}
