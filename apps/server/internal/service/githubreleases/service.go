// Package githubreleases snapshots GitHub's cumulative asset download counters.
package githubreleases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
)

type SnapshotStore interface {
	GitHubToken(context.Context, config.Config) (string, error)
	RecordGitHubReleaseSnapshot(context.Context, []store.GitHubReleaseAsset, time.Time) error
}

type Service struct {
	Store        SnapshotStore
	Config       config.Config
	HTTP         *http.Client
	ObserveQuota func(context.Context, int64)
	Now          func() time.Time
}

const releasesURL = "https://api.github.com/repos/opennavo/OpenNavo/releases"
const pageSize = 100
const pageLimit = 1000

func (s *Service) Run(ctx context.Context) (map[string]any, error) {
	// Read credentials for every run, so clearing/rotating the saved token takes
	// effect without restarting the worker. Never fall back to anonymous reads.
	token, err := s.Store.GitHubToken(ctx, s.Config)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, errors.New("GitHub release snapshot credential unavailable")
	}
	at := time.Now()
	if s.Now != nil {
		at = s.Now()
	}
	reader := changelog.NewResolver(s.Config.FetchUserAgent)
	if s.HTTP != nil {
		client := *s.HTTP
		client.CheckRedirect = reader.Client.CheckRedirect
		reader.Client = &client
	}
	reader.TokenProvider = func(context.Context) (string, error) { return token, nil }
	reader.ObserveQuota = s.ObserveQuota
	releases, err := pages[release](ctx, reader, releasesURL)
	if err != nil {
		return nil, err
	}
	assets := []store.GitHubReleaseAsset{}
	count := 0
	for _, release := range releases {
		if !strings.HasPrefix(release.Tag, "desktop-v") {
			continue
		}
		if release.ID <= 0 {
			return nil, errors.New("invalid GitHub release identity")
		}
		listed, err := pages[asset](ctx, reader, fmt.Sprintf("%s/%d/assets", releasesURL, release.ID))
		if err != nil {
			return nil, err
		}
		for _, asset := range listed {
			if asset.ID <= 0 || asset.Name == "" || asset.DownloadCount == nil || *asset.DownloadCount < 0 {
				return nil, errors.New("invalid GitHub release asset")
			}
			assets = append(assets, store.GitHubReleaseAsset{ReleaseTag: release.Tag, AssetID: asset.ID, AssetName: asset.Name, DownloadCount: *asset.DownloadCount})
		}
		count++
	}
	if len(assets) == 0 {
		return nil, errors.New("GitHub release snapshot has no assets")
	}
	// All pages must succeed before the store is allowed to write anything.
	if err := s.Store.RecordGitHubReleaseSnapshot(ctx, assets, at); err != nil {
		return nil, err
	}
	return map[string]any{"releases": count, "assets": len(assets)}, nil
}

type release struct {
	ID  int64  `json:"id"`
	Tag string `json:"tag_name"`
}

type asset struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DownloadCount *int64 `json:"download_count"`
}

func pages[T any](ctx context.Context, reader *changelog.Resolver, target string) ([]T, error) {
	result := []T{}
	for page := 1; page <= pageLimit; page++ {
		// Construct pagination locally; never follow response links with credentials.
		body, status, _, err := reader.Get(ctx, fmt.Sprintf("%s?per_page=%d&page=%d", target, pageSize, page), 16<<20, nil)
		if err != nil {
			return nil, errors.New("GitHub release snapshot request failed")
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("GitHub release snapshot HTTP %d", status)
		}
		var batch []T
		if json.Unmarshal(body, &batch) != nil || batch == nil {
			return nil, errors.New("invalid GitHub release snapshot response")
		}
		result = append(result, batch...)
		if len(batch) < pageSize {
			return result, nil
		}
	}
	return nil, errors.New("GitHub release snapshot pagination limit")
}
