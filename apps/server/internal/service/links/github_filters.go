package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

// ErrNoMatchingRelease must never trigger a fallback to /releases/latest or to
// another upstream. Sync records it as lastError and keeps the last known version.
var ErrNoMatchingRelease = errors.New("github_release_no_match")

const githubReleasePageLimit = 10

func (s *Service) filteredGithub(ctx context.Context, row store.LinkApp) (*domain.GithubRelease, string, error) {
	if err := row.ValidateGithubFilters(); err != nil {
		return nil, "", err
	}
	if s.GitHub == nil {
		return nil, "", errors.New("GitHub reader is not configured")
	}
	var tagRegex *regexp.Regexp
	if row.GithubTagRegex != nil {
		var err error
		tagRegex, err = regexp.Compile(*row.GithubTagRegex)
		if err != nil {
			return nil, "", err
		}
	}
	var selected *githubRelease
	for page := 1; page <= githubReleasePageLimit; page++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		// Construct every page ourselves; never follow upstream Link URLs. All
		// pages are fetched afresh: caching just page 1 could hide edits on page 2.
		target := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100&page=%d", *row.GithubRepo, page)
		data, status, headers, err := s.GitHub.Get(ctx, target, 4<<20, http.Header{})
		if err != nil {
			return nil, "", err
		}
		if status == http.StatusNotFound {
			return nil, "", ErrNoMatchingRelease
		}
		if status != http.StatusOK {
			return nil, "", fmt.Errorf("release metadata returned HTTP %d", status)
		}
		var releases []githubRelease
		if err := json.Unmarshal(data, &releases); err != nil {
			return nil, "", err
		}
		if len(releases) > 100 {
			return nil, "", errors.New("GitHub release page too large")
		}
		for _, release := range releases {
			if release.Draft || (release.Prerelease && !row.IncludePrereleases) {
				continue
			}
			if row.GithubTagPrefix != nil && !strings.HasPrefix(release.Tag, *row.GithubTagPrefix) {
				continue
			}
			if tagRegex != nil && !tagRegex.MatchString(release.Tag) {
				continue
			}
			if row.GithubAssetPattern != nil {
				assets := release.Assets[:0]
				for _, a := range release.Assets {
					// Validated once above; matching uses Go's bounded glob matcher.
					if ok, _ := path.Match(*row.GithubAssetPattern, a.Name); ok {
						assets = append(assets, a)
					}
				}
				release.Assets = assets
				if len(assets) == 0 {
					continue
				}
			}
			if selected == nil || release.Published.After(selected.Published) || (release.Published.Equal(selected.Published) && release.Tag > selected.Tag) {
				selected = &release
			}
		}
		if !strings.Contains(headers.Get("Link"), `rel="next"`) {
			if selected == nil {
				return nil, "", ErrNoMatchingRelease
			}
			return parseGithubRelease(*selected, row.AssetRules, row.IncludePrereleases, row.GithubTagPrefix)
		}
	}
	// A truncated scan cannot establish which matching release is newest.
	return nil, "", errors.New("github_release_search_limit: more than 1000 releases")
}
