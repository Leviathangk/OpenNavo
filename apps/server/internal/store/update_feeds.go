package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
)

type UpdateFeed struct {
	domain.UpdateFeedInput
	Revision     int64                 `json:"revision"`
	Release      *domain.UpdateRelease `json:"release,omitempty"`
	LastModified *string               `json:"lastModified,omitempty"`
	Origin       string                `json:"origin"`
	Enabled      bool                  `json:"enabled"`
	ETag         *string               `json:"etag"`
	VerifiedAt   *time.Time            `json:"verifiedAt"`
	CheckedAt    *time.Time            `json:"checkedAt"`
	LastError    *string               `json:"lastError"`
	Token        string                `json:"token"`
	Kind         string                `json:"kind"`
}

const updateFeedView = `SELECT jsonb_build_object('bundleId',f.bundle_id,'source',f.source,'feedUrl',f.feed_url,'teamId',f.team_id,'channel',f.channel,'packageId',f.package_id,'origin',f.origin,'revision',f.revision,'githubRepo',f.github_repo,'assetPattern',f.asset_pattern,'resolver',f.resolver,'release',f.release,'lastModified',f.last_modified,'enabled',f.enabled,'etag',f.etag,'verifiedAt',f.verified_at,'checkedAt',f.checked_at,'lastError',f.last_error,'token',CASE WHEN p.removed_at IS NULL AND p.kind IN ('cask','link') THEN p.token ELSE '' END,'kind',CASE WHEN p.removed_at IS NULL AND p.kind IN ('cask','link') THEN p.kind ELSE 'cask' END) AS data FROM update_feeds f LEFT JOIN packages p ON p.id=f.package_id`

func (s *Store) UpdateFeeds(ctx context.Context) ([]UpdateFeed, error) {
	raw, err := s.JSONRows(ctx, updateFeedView+` ORDER BY f.bundle_id,f.source`)
	if err != nil {
		return nil, err
	}
	out := make([]UpdateFeed, 0, len(raw))
	for _, r := range raw {
		var v UpdateFeed
		if err := json.Unmarshal(r, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) UpdateFeed(ctx context.Context, key domain.UpdateFeedKey) (*UpdateFeed, error) {
	rows, err := s.JSONRows(ctx, updateFeedView+` WHERE f.bundle_id=? AND f.source=?`, key.BundleID, key.Source)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	var v UpdateFeed
	err = json.Unmarshal(rows[0], &v)
	return &v, err
}
func (s *Store) ValidateUpdateFeedPackage(ctx context.Context, input domain.UpdateFeedInput) error {
	if input.PackageID == nil {
		return nil
	}
	var count int64
	if err := s.DB.WithContext(ctx).Raw(`SELECT count(*) FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL`, *input.PackageID).Scan(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return domain.Rejected("package_not_found")
	}
	return nil
}
func (s *Store) WriteUpdateFeed(ctx context.Context, input domain.UpdateFeedInput, origin string) error {
	if err := input.Validate(); err != nil {
		return err
	}
	if err := s.ValidateUpdateFeedPackage(ctx, input); err != nil {
		return err
	}
	if origin != "manual" && origin != "homebrew" && origin != "link" && origin != "seed" {
		return domain.RejectedDetail("origin_invalid", origin)
	}
	resolver, err := json.Marshal(input.Resolver)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec(`INSERT INTO update_feeds(bundle_id,source,feed_url,team_id,channel,package_id,github_repo,asset_pattern,resolver,origin) VALUES(?,?,?,?,?,?,?,?,?::jsonb,?)
 ON CONFLICT(bundle_id,source) DO UPDATE SET feed_url=EXCLUDED.feed_url,team_id=EXCLUDED.team_id,channel=EXCLUDED.channel,package_id=COALESCE(EXCLUDED.package_id,update_feeds.package_id),github_repo=EXCLUDED.github_repo,asset_pattern=EXCLUDED.asset_pattern,resolver=EXCLUDED.resolver,origin=EXCLUDED.origin,enabled=true,updated_at=now(),revision=update_feeds.revision+1,
 etag=CASE WHEN (update_feeds.feed_url,update_feeds.team_id,update_feeds.channel,update_feeds.github_repo,update_feeds.asset_pattern,update_feeds.resolver) IS NOT DISTINCT FROM (EXCLUDED.feed_url,EXCLUDED.team_id,EXCLUDED.channel,EXCLUDED.github_repo,EXCLUDED.asset_pattern,EXCLUDED.resolver) THEN update_feeds.etag ELSE NULL END,
 last_modified=CASE WHEN (update_feeds.feed_url,update_feeds.team_id,update_feeds.channel,update_feeds.github_repo,update_feeds.asset_pattern,update_feeds.resolver) IS NOT DISTINCT FROM (EXCLUDED.feed_url,EXCLUDED.team_id,EXCLUDED.channel,EXCLUDED.github_repo,EXCLUDED.asset_pattern,EXCLUDED.resolver) THEN update_feeds.last_modified ELSE NULL END,
 verified_at=CASE WHEN (update_feeds.feed_url,update_feeds.team_id,update_feeds.channel,update_feeds.github_repo,update_feeds.asset_pattern,update_feeds.resolver) IS NOT DISTINCT FROM (EXCLUDED.feed_url,EXCLUDED.team_id,EXCLUDED.channel,EXCLUDED.github_repo,EXCLUDED.asset_pattern,EXCLUDED.resolver) THEN update_feeds.verified_at ELSE NULL END
 WHERE EXCLUDED.origin='manual' OR (update_feeds.origin=EXCLUDED.origin AND update_feeds.package_id IS NOT DISTINCT FROM EXCLUDED.package_id)`, input.BundleID, input.Source, input.FeedURL, input.TeamID, input.Channel, input.PackageID, input.GithubRepo, input.AssetPattern, string(resolver), origin).Error
}
func (s *Store) DisableUpdateFeed(ctx context.Context, key domain.UpdateFeedKey) error {
	r := s.DB.WithContext(ctx).Exec(`UPDATE update_feeds SET enabled=false,origin='manual',updated_at=now() WHERE bundle_id=? AND source=?`, key.BundleID, key.Source)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	return nil
}
func (s *Store) CheckedUpdateFeed(ctx context.Context, before UpdateFeed, etag string, err error) error {
	return s.CheckedResolvedUpdateFeed(ctx, before, etag, "", before.Release, err)
}
func (s *Store) CheckedResolvedUpdateFeed(ctx context.Context, before UpdateFeed, etag, modified string, release *domain.UpdateRelease, checkErr error) error {
	var message *string
	if checkErr != nil {
		v := checkErr.Error()
		if len(v) > 1024 {
			v = v[:1024]
		}
		message = &v
	}
	data, err := json.Marshal(release)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec(`UPDATE update_feeds SET checked_at=now(),last_error=?,etag=CASE WHEN ?::text IS NULL THEN ? ELSE etag END,last_modified=CASE WHEN ?::text IS NULL THEN ? ELSE last_modified END,release=CASE WHEN ?::text IS NULL THEN ?::jsonb ELSE release END,verified_at=CASE WHEN ?::text IS NULL THEN now() ELSE verified_at END WHERE bundle_id=? AND source=? AND revision=? AND enabled`, message, message, etag, message, modified, message, string(data), message, before.BundleID, before.Source, before.Revision).Error
}
