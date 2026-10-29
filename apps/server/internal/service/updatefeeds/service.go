// Package updatefeeds maintains an independent, verified catalog of update rules.
package updatefeeds

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"time"

	"golang.org/x/sync/errgroup"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/store"
)

type Service struct {
	Store      *store.Store
	GitHub     taps.GitHub
	SeedRules  bool
	HTTP       *http.Client
	UserAgent  string
	ArchiveURL string
}

func (s *Service) get(ctx context.Context, target, etag string, limit int64) ([]byte, int, string, error) {
	if !domain.LinkHTTPS(target) {
		return nil, 0, "", errors.New("update feed must be public HTTPS")
	}
	client := s.HTTP
	if client == nil {
		client = fetch.NewClient(60 * time.Second)
	}
	copy := *client
	previous := client.CheckRedirect
	copy.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !domain.LinkHTTPS(r.URL.String()) {
			return errors.New("unsafe update feed redirect")
		}
		if previous != nil {
			return previous(r, via)
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, "", err
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	request.Header.Set("User-Agent", s.UserAgent)
	response, err := copy.Do(request)
	if err != nil {
		return nil, 0, "", err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if int64(len(data)) > limit {
		return nil, 0, "", errors.New("update feed size limit")
	}
	return data, response.StatusCode, response.Header.Get("ETag"), err
}

// parseArchive never extracts files to disk; paths identify official casks only.
func parseArchive(data []byte) (map[string]homebrew.UpdateLivecheck, error) {
	zip, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zip.Close() }()
	bounded := &io.LimitedReader{R: zip, N: 128 << 20}
	archive := tar.NewReader(bounded)
	out := map[string]homebrew.UpdateLivecheck{}
	entries := 0
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if bounded.N == 0 {
				return nil, errors.New("archive size limit")
			}
			break
		}
		if err != nil {
			return nil, err
		}
		entries++
		if entries > 40000 {
			return nil, errors.New("archive entry limit")
		}
		parts := strings.Split(header.Name, "/")
		if len(parts) < 3 || parts[1] != "Casks" || !strings.HasSuffix(header.Name, ".rb") || header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size > 256<<10 {
			return nil, errors.New("cask source size limit")
		}
		source, err := io.ReadAll(archive)
		if err != nil {
			return nil, err
		}
		token := strings.TrimSuffix(path.Base(header.Name), ".rb")
		if _, ok := out[token]; ok {
			return nil, errors.New("duplicate cask token")
		}
		rule, err := homebrew.ParseUpdateLivecheck(string(source))
		if err == nil && rule.Source != "" {
			out[token] = rule
		}
	}
	return out, nil
}

// ValidateSparkle checks namespaced build identifiers and HTTPS enclosures. It
// does not choose an update for any machine or download installation assets.
func ValidateSparkle(body []byte) error {
	if len(body) > 2<<20 {
		return errors.New("appcast size limit")
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth, nodes, items := 0, 0, 0
	inItem := false
	hasVersion, hasURL, informational, hasLink := false, false, false, false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		nodes++
		if nodes > 50000 {
			return errors.New("appcast node limit")
		}
		switch t := token.(type) {
		case xml.Directive:
			return errors.New("XML directives are not allowed")
		case xml.StartElement:
			depth++
			if depth == 1 && (t.Name.Local != "rss" || t.Name.Space != "") {
				return errors.New("appcast RSS root required")
			}
			if depth > 24 {
				return errors.New("appcast depth limit")
			}
			if t.Name.Local == "item" && t.Name.Space == "" {
				inItem = true
				hasVersion = false
				hasURL = false
				informational = false
				hasLink = false
			}
			if !inItem {
				continue
			}
			if t.Name.Local == "version" && t.Name.Space == "http://www.andymatuschak.org/xml-namespaces/sparkle" {
				var version string
				if err := decoder.DecodeElement(&version, &t); err != nil {
					return err
				}
				depth--
				hasVersion = strings.TrimSpace(version) != ""
			}
			if t.Name.Local == "enclosure" && t.Name.Space == "" {
				for _, a := range t.Attr {
					if a.Name.Local == "url" && a.Name.Space == "" {
						if !domain.LinkHTTPS(a.Value) {
							return errors.New("insecure appcast URL")
						}
						hasURL = true
					}
					if a.Name.Local == "version" && a.Name.Space == "http://www.andymatuschak.org/xml-namespaces/sparkle" && a.Value != "" {
						hasVersion = true
					}
				}
			}
			if t.Name.Local == "informationalUpdate" && t.Name.Space == "http://www.andymatuschak.org/xml-namespaces/sparkle" {
				informational = true
			}
			if t.Name.Local == "link" && t.Name.Space == "" {
				var link string
				if err := decoder.DecodeElement(&link, &t); err != nil {
					return err
				}
				depth--
				hasLink = domain.LinkHTTPS(strings.TrimSpace(link))
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "item" && t.Name.Space == "" {
				if hasVersion && (hasURL || (informational && hasLink)) {
					items++
				}
				inItem = false
			}
		}
	}
	if items == 0 {
		return errors.New("no Sparkle update items")
	}
	return nil
}
func (s *Service) Run(ctx context.Context) (map[string]any, error) {
	archiveURL := s.ArchiveURL
	if archiveURL == "" {
		// Follow the repository default branch; the previous master URL now returns 404.
		archiveURL = "https://codeload.github.com/Homebrew/homebrew-cask/tar.gz/HEAD"
	}
	data, status, _, archiveErr := s.get(ctx, archiveURL, "", 64<<20)
	feeds := map[string]homebrew.UpdateLivecheck{}
	if archiveErr == nil && status != 200 {
		archiveErr = fmt.Errorf("cask archive HTTP %d", status)
	}
	if archiveErr == nil {
		feeds, archiveErr = parseArchive(data)
	}
	// An upstream repository outage must not prevent validation of existing or
	// manually maintained rules. Report the import failure after checking them.
	packages, err := s.Store.SourceFactPackages(ctx)
	if err != nil {
		return nil, err
	}
	links, err := s.Store.LinkApps(ctx, true)
	if err != nil {
		return nil, err
	}
	// Only official Homebrew casks can inherit rules from this repository.
	official := map[int64]bool{}
	var ids []int64
	if err := s.Store.DB.WithContext(ctx).Raw("SELECT p.id FROM packages p WHERE p.kind='cask' AND " + store.OfficialTapSQL + " AND removed_at IS NULL").Scan(&ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		official[id] = true
	}
	imported := 0
	for _, p := range packages {
		if !official[p.ID] || feeds[p.Token].Source == "" {
			continue
		}
		bundles := sourcefacts.BundleIDs(p.Artifacts)
		if len(bundles) == 0 {
			continue
		}
		// Ambiguous helper identifiers are never all mapped to the main app's feed.
		rule := feeds[p.Token]
		input := domain.UpdateFeedInput{UpdateFeedKey: domain.UpdateFeedKey{BundleID: bundles[0], Source: rule.Source}, FeedURL: rule.URL, Channel: "stable", PackageID: &p.ID}
		if rule.Source == "github" {
			team := seedTeam(bundles[0])
			if team == nil {
				continue
			}
			input.TeamID = team
			input.GithubRepo = &rule.GithubRepo
			input.AssetPattern = ptr(defaultMacPattern)
		}
		if err := s.Store.WriteUpdateFeed(ctx, input, "homebrew"); err != nil {
			return nil, err
		}
		imported++
	}
	for _, p := range links {
		for _, bundle := range p.BundleIDs {
			team := p.TeamID
			if team == nil {
				team = seedTeam(bundle)
			}
			inputs := []domain.UpdateFeedInput{}
			if p.SparkleFeed != nil {
				inputs = append(inputs, domain.UpdateFeedInput{UpdateFeedKey: domain.UpdateFeedKey{BundleID: bundle, Source: "sparkle"}, FeedURL: *p.SparkleFeed, TeamID: team, Channel: "stable", PackageID: &p.ID})
			}
			if p.GithubRepo != nil {
				pattern := defaultMacPattern
				if custom := p.AssetRules["pattern"]; custom != "" {
					pattern = custom
				}
				inputs = append(inputs, domain.UpdateFeedInput{UpdateFeedKey: domain.UpdateFeedKey{BundleID: bundle, Source: "github"}, TeamID: team, GithubRepo: p.GithubRepo, AssetPattern: &pattern, Channel: "stable", PackageID: &p.ID})
			}
			for _, input := range inputs {
				if err := s.Store.WriteUpdateFeed(ctx, input, "link"); err != nil {
					return nil, err
				}
				imported++
			}
		}
	}
	if s.SeedRules {
		if err := s.importSeeds(ctx); err != nil {
			return nil, err
		}
	}
	rows, err := s.Store.UpdateFeeds(ctx)
	if err != nil {
		return nil, err
	}
	var checked, failed atomic.Int64
	workers, checkCtx := errgroup.WithContext(ctx)
	workers.SetLimit(8)
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		workers.Go(func() error {
			if err := checkCtx.Err(); err != nil {
				return err
			}
			var release *domain.UpdateRelease
			etag, modified := "", ""
			var err error
			switch row.Source {
			case "github":
				release, modified, err = s.github(checkCtx, row)
			case "vendor":
				release, err = s.vendor(checkCtx, row)
			case "sparkle", "electron":
				tag := ""
				if row.ETag != nil && row.VerifiedAt != nil {
					tag = *row.ETag
				}
				var body []byte
				var status int
				body, status, etag, err = s.get(checkCtx, row.FeedURL, tag, metadataLimit)
				if err == nil {
					switch status {
					case 304:
						if row.VerifiedAt == nil || row.Source == "electron" && row.Release == nil {
							err = errors.New("304 without verified feed")
						}
						etag = tag
						release = row.Release
					case 200:
						if row.Source == "sparkle" {
							err = ValidateSparkle(body)
						} else {
							release, err = parseElectron(body, row.FeedURL)
						}
					default:
						err = fmt.Errorf("update feed HTTP %d", status)
					}
				}
			default:
				err = errors.New("unsupported update source")
			}
			checked.Add(1)
			if err != nil {
				failed.Add(1)
			}
			return s.Store.CheckedResolvedUpdateFeed(checkCtx, row, etag, modified, release, err)
		})
	}
	if err := workers.Wait(); err != nil {
		return nil, err
	}
	return map[string]any{"imported": imported, "checked": int(checked.Load()), "failed": int(failed.Load())}, archiveErr
}
