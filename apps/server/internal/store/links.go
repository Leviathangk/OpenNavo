package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

type LinkApp struct {
	domain.LinkInput
	ID              int64                 `json:"id"`
	Revision        int64                 `json:"revision"`
	Kind            string                `json:"kind"`
	Latest          *domain.GithubRelease `json:"latest"`
	ETag            *string               `json:"etag"`
	SyncedAt        *time.Time            `json:"syncedAt"`
	LastError       *string               `json:"lastError"`
	AdoptedByCaskAt *time.Time            `json:"adoptedByCaskAt"`
	Removed         bool                  `json:"removed"`
}

const linkView = `SELECT jsonb_build_object('id',p.id,'revision',l.revision,'kind',p.kind,'token',p.token,'name',p.name,'sourceLocale',p.source_locale,'homepage',l.homepage,'downloadUrl',l.download_url,'githubRepo',l.github_repo,'githubTagPrefix',l.github_tag_prefix,'githubTagRegex',l.github_tag_regex,'githubAssetPattern',l.github_asset_pattern,'includePrereleases',l.include_prereleases,'bundleIds',l.bundle_ids,'teamId',l.team_id,'pricing',l.pricing,'sparkleFeed',l.sparkle_feed,'assetRules',l.asset_rules,'notes',l.notes,'latest',l.latest,'etag',l.etag,'syncedAt',l.synced_at,'lastError',l.last_error,'adoptedByCaskAt',l.adopted_by_cask_at,'removed',p.removed_at IS NOT NULL) AS data FROM link_apps l JOIN packages p ON p.id=l.package_id`

func (s *Store) LinkApps(ctx context.Context, activeOnly bool) ([]LinkApp, error) {
	condition := ""
	if activeOnly {
		condition = " WHERE p.kind='link' AND p.removed_at IS NULL AND l.adopted_by_cask_at IS NULL"
	}
	return s.linkRows(ctx, condition+" ORDER BY p.token")
}
func (s *Store) linkRows(ctx context.Context, condition string, args ...any) ([]LinkApp, error) {
	rows, err := s.JSONRows(ctx, linkView+condition, args...)
	if err != nil {
		return nil, err
	}
	result := make([]LinkApp, 0, len(rows))
	for _, row := range rows {
		var v LinkApp
		if err := json.Unmarshal(row, &v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}
func (s *Store) LinkApp(ctx context.Context, id int64) (*LinkApp, error) {
	rows, err := s.linkRows(ctx, " WHERE p.id=?", id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}
func (s *Store) LinkTokenAvailable(ctx context.Context, token string, id int64) error {
	var n int64
	if err := s.DB.WithContext(ctx).Raw("SELECT count(*) FROM packages WHERE kind IN ('cask','link') AND token=? AND id<>?", token, id).Scan(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: 409}
	}
	return nil
}

// WriteLinkApp is called inside the management transaction. A single catalog lock
// serializes manual additions with official Homebrew adoption and cursor writes.
func (s *Store) WriteLinkApp(ctx context.Context, id int64, input domain.LinkInput) (int64, error) {
	if err := input.Validate(); err != nil {
		return 0, err
	}
	if err := LockCatalogWrites(ctx, s.DB); err != nil {
		return 0, err
	}
	if err := s.LinkTokenAvailable(ctx, input.Token, id); err != nil {
		return 0, err
	}
	created := id == 0
	if created {
		if err := s.DB.WithContext(ctx).Raw(`INSERT INTO packages(kind,token,full_token,tap,name,names,homepage,download_url,version,version_base,raw,raw_hash,source_locale) VALUES('link',?,?,'',?,ARRAY[?]::text[],?,?,'','','{}'::jsonb,'',?) RETURNING id`, input.Token, input.Token, input.Name, input.Name, input.Homepage, input.DownloadURL, input.SourceLocale).Scan(&id).Error; err != nil {
			return 0, err
		}
	} else {
		r := s.DB.WithContext(ctx).Exec(`UPDATE packages SET name=?,names=ARRAY[?]::text[],homepage=?,download_url=?,source_locale=?,updated_at=now() WHERE id=? AND kind='link' AND token=? AND removed_at IS NULL`, input.Name, input.Name, input.Homepage, input.DownloadURL, input.SourceLocale, id, input.Token)
		if r.Error != nil {
			return 0, r.Error
		}
		if r.RowsAffected != 1 {
			return 0, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
		}
	}
	bundles, _ := json.Marshal(input.BundleIDs)
	rules, _ := json.Marshal(input.AssetRules)
	notes, _ := json.Marshal(input.Notes)
	err := s.DB.WithContext(ctx).Exec(`INSERT INTO link_apps(package_id,homepage,download_url,github_repo,bundle_ids,team_id,pricing,sparkle_feed,asset_rules,notes,github_tag_prefix,github_tag_regex,github_asset_pattern,include_prereleases)
 VALUES(?,?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?,?,?,?::jsonb,?::jsonb,?,?,?,?)
 ON CONFLICT(package_id) DO UPDATE SET homepage=EXCLUDED.homepage,download_url=EXCLUDED.download_url,github_repo=EXCLUDED.github_repo,bundle_ids=EXCLUDED.bundle_ids,team_id=EXCLUDED.team_id,pricing=EXCLUDED.pricing,sparkle_feed=EXCLUDED.sparkle_feed,asset_rules=EXCLUDED.asset_rules,notes=EXCLUDED.notes,github_tag_prefix=EXCLUDED.github_tag_prefix,github_tag_regex=EXCLUDED.github_tag_regex,github_asset_pattern=EXCLUDED.github_asset_pattern,include_prereleases=EXCLUDED.include_prereleases,
 latest=CASE WHEN link_apps.github_repo IS DISTINCT FROM EXCLUDED.github_repo OR link_apps.sparkle_feed IS DISTINCT FROM EXCLUDED.sparkle_feed OR link_apps.asset_rules IS DISTINCT FROM EXCLUDED.asset_rules OR link_apps.github_tag_prefix IS DISTINCT FROM EXCLUDED.github_tag_prefix OR link_apps.github_tag_regex IS DISTINCT FROM EXCLUDED.github_tag_regex OR link_apps.github_asset_pattern IS DISTINCT FROM EXCLUDED.github_asset_pattern OR link_apps.include_prereleases IS DISTINCT FROM EXCLUDED.include_prereleases THEN NULL ELSE link_apps.latest END,
 etag=CASE WHEN link_apps.github_repo IS DISTINCT FROM EXCLUDED.github_repo OR link_apps.sparkle_feed IS DISTINCT FROM EXCLUDED.sparkle_feed OR link_apps.asset_rules IS DISTINCT FROM EXCLUDED.asset_rules OR link_apps.github_tag_prefix IS DISTINCT FROM EXCLUDED.github_tag_prefix OR link_apps.github_tag_regex IS DISTINCT FROM EXCLUDED.github_tag_regex OR link_apps.github_asset_pattern IS DISTINCT FROM EXCLUDED.github_asset_pattern OR link_apps.include_prereleases IS DISTINCT FROM EXCLUDED.include_prereleases THEN NULL ELSE link_apps.etag END`, id, input.Homepage, input.DownloadURL, input.GithubRepo, string(bundles), input.TeamID, input.Pricing, input.SparkleFeed, string(rules), string(notes), input.GithubTagPrefix, input.GithubTagRegex, input.GithubAssetPattern, input.IncludePrereleases).Error
	if err != nil {
		return 0, err
	}
	if err := UpdateSearchIndex(ctx, s.DB, []int64{id}); err != nil {
		return 0, err
	}
	// Adding a link is a catalog event like a new cask; editing its definition is not (Homebrew's rule).
	event := ""
	if created {
		event = "created"
	}
	if err := s.linkChanged(ctx, id, "upsert", event, nil); err != nil {
		return 0, err
	}
	if err := addOutbox(ctx, s.DB, "links:sync", "packageId", id); err != nil {
		return 0, err
	}
	return id, nil
}
func (s *Store) RemoveLinkApp(ctx context.Context, id int64) error {
	if err := LockCatalogWrites(ctx, s.DB); err != nil {
		return err
	}
	r := s.DB.WithContext(ctx).Exec("UPDATE packages SET removed_at=now(),updated_at=now() WHERE id=? AND kind='link' AND removed_at IS NULL", id)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	return s.linkChanged(ctx, id, "delete", "removed", nil)
}

// linkChanged records a link app change for snapshots, the URL change feed and catalog activity. event is
// created, updated (a new version), removed or empty for a definition edit; the row carries the current
// version, and previous names the version an update replaced.
func (s *Store) linkChanged(ctx context.Context, id int64, op, event string, previous *string) error {
	if err := s.DB.WithContext(ctx).Exec("INSERT INTO catalog_changes(package_id,kind,token,op,reason,event_type,version,previous_version) SELECT id,kind,token,?,'link',NULLIF(?,''),NULLIF(version,''),NULLIF(?,'') FROM packages WHERE id=?", op, event, previous, id).Error; err != nil {
		return err
	}
	return addCatalogInvalidation(ctx, s.DB)
}

// LinkSyncResult rechecks source identity under the lock: a response fetched before
// an edit/removal/adoption may never overwrite the newly selected source.
func (s *Store) LinkSyncResult(ctx context.Context, before LinkApp, latest *domain.GithubRelease, source, body, etag string, checked time.Time, fetchError error) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &Store{DB: tx, Redis: s.Redis}
		current, err := st.LinkApp(ctx, before.ID)
		if err != nil {
			return err
		}
		if current == nil || current.Kind != "link" || current.Removed || !sameLinkSource(*current, before) {
			return nil
		}
		if fetchError != nil {
			message := fetchError.Error()
			if len(message) > 1024 {
				message = message[:1024]
			}
			return tx.WithContext(ctx).Exec("UPDATE link_apps SET synced_at=?,last_error=? WHERE package_id=?", checked, message, before.ID).Error
		}
		if latest == nil {
			return tx.WithContext(ctx).Exec("UPDATE link_apps SET synced_at=?,last_error=NULL,etag=? WHERE package_id=?", checked, nullable(etag), before.ID).Error
		}
		raw, err := json.Marshal(latest)
		if err != nil {
			return err
		}
		old, _ := json.Marshal(current.Latest)
		if err := tx.WithContext(ctx).Exec("UPDATE link_apps SET latest=?::jsonb,etag=?,synced_at=?,last_error=NULL WHERE package_id=?", string(raw), nullable(etag), checked, before.ID).Error; err != nil {
			return err
		}
		if source != "github_release" && source != "sparkle" {
			return errors.New("invalid link release source")
		}
		recorded := tx.WithContext(ctx).Exec("INSERT INTO package_versions(package_id,version,version_base,first_seen_at) VALUES(?,?,?,?) ON CONFLICT(package_id,version) DO NOTHING", before.ID, latest.Version, latest.Version, checked)
		if recorded.Error != nil {
			return recorded.Error
		}
		release := tx.WithContext(ctx).Exec(`INSERT INTO releases(package_id,source,source_key,version,title,published_at,source_url,body_markdown,source_locale) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(package_id,source,source_key) DO UPDATE SET version=EXCLUDED.version,published_at=EXCLUDED.published_at,source_url=EXCLUDED.source_url,body_markdown=EXCLUDED.body_markdown,source_locale=EXCLUDED.source_locale,updated_at=now() WHERE (releases.version,releases.published_at,releases.source_url,releases.body_markdown,releases.source_locale) IS DISTINCT FROM (EXCLUDED.version,EXCLUDED.published_at,EXCLUDED.source_url,EXCLUDED.body_markdown,EXCLUDED.source_locale)`, before.ID, source, latest.Tag, latest.Version, latest.Tag, latest.PublishedAt, latest.URL, nullable(body), before.SourceLocale)
		if release.Error != nil {
			return release.Error
		}
		if release.RowsAffected > 0 || string(raw) != string(old) {
			if err := addCacheInvalidation(ctx, tx, "link-releases:"+current.Token, "c:rel:*:link:"+current.Token+":*"); err != nil {
				return err
			}
		}
		if string(raw) == string(old) {
			if release.RowsAffected > 0 {
				return addCatalogInvalidation(ctx, tx)
			}
			return nil
		}
		var previous string
		if err := tx.WithContext(ctx).Raw("SELECT version FROM packages WHERE id=?", before.ID).Scan(&previous).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Exec("UPDATE packages SET version_changed_at=CASE WHEN version<>? THEN ? ELSE version_changed_at END,version=?,version_base=?,updated_at=? WHERE id=?", latest.Version, checked, latest.Version, latest.Version, checked, before.ID).Error; err != nil {
			return err
		}
		// Like a cask, a link reports updated only for a version it has never shipped before.
		event := ""
		if recorded.RowsAffected > 0 && previous != latest.Version {
			event = "updated"
		}
		return st.linkChanged(ctx, before.ID, "upsert", event, &previous)
	})
}
func sameLinkSource(a, b LinkApp) bool {
	equal := func(x, y *string) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	x, _ := json.Marshal(a.AssetRules)
	y, _ := json.Marshal(b.AssetRules)
	return equal(a.GithubRepo, b.GithubRepo) && equal(a.SparkleFeed, b.SparkleFeed) && string(x) == string(y) && equal(a.GithubTagPrefix, b.GithubTagPrefix) && equal(a.GithubTagRegex, b.GithubTagRegex) && equal(a.GithubAssetPattern, b.GithubAssetPattern) && a.IncludePrereleases == b.IncludePrereleases
}

func adoptLinks(ctx context.Context, tx *gorm.DB, tokens []string, now time.Time) error {
	if len(tokens) == 0 {
		return nil
	}
	// Preserve the editor's removal intent before the cask upsert clears removed_at.
	if err := tx.WithContext(ctx).Exec(`INSERT INTO package_meta(package_id,hidden)
 SELECT id,true FROM packages WHERE kind='link' AND token IN ? AND removed_at IS NOT NULL
 ON CONFLICT(package_id) DO UPDATE SET hidden=true`, tokens).Error; err != nil {
		return err
	}
	// Change ownership first, preserving the row's ID and all foreign-key content.
	if err := tx.WithContext(ctx).Exec(`UPDATE link_apps l SET adopted_by_cask_at=? FROM packages p WHERE p.id=l.package_id AND p.kind='link' AND p.token IN ?`, now, tokens).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("UPDATE packages SET kind='cask' WHERE kind='link' AND token IN ?", tokens).Error
}
