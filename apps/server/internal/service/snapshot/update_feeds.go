package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/opennavo/opennavo/server/internal/domain"
)

// Update feeds use their own representation hash, independent of catalog revisions.
type UpdateFeedRule struct {
	BundleID     string                `json:"bundleId"`
	Source       string                `json:"source"`
	FeedURL      string                `json:"feedUrl"`
	TeamID       *string               `json:"teamId"`
	Channel      string                `json:"channel"`
	Token        string                `json:"token"`
	Kind         string                `json:"kind"`
	GithubRepo   *string               `json:"githubRepo,omitempty"`
	AssetPattern *string               `json:"assetPattern,omitempty"`
	Release      *domain.UpdateRelease `json:"release,omitempty"`
}
type CatalogUpdateFeeds struct {
	ETag  string           `json:"etag"`
	Items []UpdateFeedRule `json:"items"`
}

func (s *Service) UpdateFeeds(ctx context.Context) (CatalogUpdateFeeds, error) {
	result := CatalogUpdateFeeds{Items: []UpdateFeedRule{}}
	rows, err := s.Store.UpdateFeeds(ctx)
	if err != nil {
		return result, err
	}
	for _, r := range rows {
		if r.Enabled && r.VerifiedAt != nil && r.LastError == nil {
			result.Items = append(result.Items, UpdateFeedRule{BundleID: r.BundleID, Source: r.Source, FeedURL: r.FeedURL, TeamID: r.TeamID, Channel: r.Channel, Token: r.Token, Kind: r.Kind, GithubRepo: r.GithubRepo, AssetPattern: r.AssetPattern, Release: r.Release})
		}
	}
	raw, err := json.Marshal(result.Items)
	if err != nil {
		return result, err
	}
	result.ETag = fmt.Sprintf(`"%x"`, sha256.Sum256(raw))
	return result, nil
}
