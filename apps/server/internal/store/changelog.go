package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/changelog"
	"gorm.io/gorm"
)

const candidateSQL = `SELECT p.id,p.kind,p.token,p.tap,COALESCE(p.homepage,'') homepage,COALESCE(p.download_url,'') download_url,COALESCE(m.repo_url,'') repo_url,COALESCE(p.raw->>'ruby_source_path','') ruby_source_path,COALESCE(p.raw->>'tap_git_head','') tap_git_head FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id`

func (s *Store) ChangelogCandidate(ctx context.Context, id int64) (changelog.Candidate, error) {
	var row changelog.Candidate
	r := s.DB.WithContext(ctx).Raw(candidateSQL+" WHERE p.id=? AND p.removed_at IS NULL", id).Scan(&row)
	if r.Error != nil {
		return row, r.Error
	}
	if r.RowsAffected == 0 {
		return row, gorm.ErrRecordNotFound
	}
	return row, nil
}
func (s *Store) ChangelogTop(ctx context.Context, limit int) ([]changelog.Candidate, error) {
	rows := []changelog.Candidate{}
	err := s.DB.WithContext(ctx).Raw(candidateSQL+" WHERE p.removed_at IS NULL AND NOT p.disabled AND NOT p.is_font AND NOT COALESCE(m.hidden,false) ORDER BY p.popularity DESC,p.id LIMIT ?", limit).Scan(&rows).Error
	return rows, err
}
func (s *Store) SaveChangelogSources(ctx context.Context, id int64, sources []changelog.Source) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		for _, source := range sources {
			data, err := json.Marshal(source.Config)
			if err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,?,?::jsonb,?,'auto') ON CONFLICT(package_id,type) DO UPDATE SET config=EXCLUDED.config,priority=EXCLUDED.priority,etag=NULL,last_modified=NULL,fail_count=0,last_error=NULL,next_fetch_at=now(),updated_at=now() WHERE changelog_sources.resolved_by='auto' AND (changelog_sources.config<>EXCLUDED.config OR changelog_sources.priority<>EXCLUDED.priority)`, id, source.Type, string(data), source.Priority).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type ChangelogState struct {
	ID, PackageID             int64
	Enabled                   bool
	Excluded                  bool
	Type                      string
	Config                    json.RawMessage
	ETag                      *string `gorm:"column:etag"`
	LastModified              *string
	LastFetchedAt             *time.Time
	LastStatus                string
	FailCount                 int
	Rank30d                   *int `gorm:"column:rank_30d"`
	Homepage                  *string
	CurrentVersion            string
	CurrentVersionCommittedAt *time.Time
}

func (s *Store) ChangelogState(ctx context.Context, id int64) (ChangelogState, error) {
	var row ChangelogState
	r := s.DB.WithContext(ctx).Raw("SELECT s.id,s.package_id,s.enabled,COALESCE(m.changelog_excluded,false) excluded,s.type,s.config,s.etag,s.last_modified,s.last_fetched_at,s.last_status,s.fail_count,p.rank_30d,p.homepage,p.version current_version,v.brew_committed_at current_version_committed_at FROM changelog_sources s JOIN packages p ON p.id=s.package_id LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN package_versions v ON v.package_id=p.id AND v.version=p.version WHERE s.id=? AND s.type='homebrew_commits' AND p.kind='cask' AND p.removed_at IS NULL", id).Scan(&row)
	if r.Error != nil {
		return row, r.Error
	}
	if r.RowsAffected == 0 {
		return row, gorm.ErrRecordNotFound
	}
	return row, nil
}
func (s *Store) ChangelogDue(ctx context.Context, now time.Time) ([]int64, error) {
	ids := []int64{}
	err := s.DB.WithContext(ctx).Raw("SELECT s.id FROM changelog_sources s JOIN packages p ON p.id=s.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE NOT COALESCE(m.changelog_excluded,false) AND s.enabled AND s.type='homebrew_commits' AND p.kind='cask' AND s.next_fetch_at<=? AND p.removed_at IS NULL AND NOT p.disabled ORDER BY p.popularity DESC,s.id LIMIT 1000", now).Scan(&ids).Error
	return ids, err
}

// SaveFetchedChangelog reports whether the fetch added or completed any version.
func (s *Store) SaveFetchedChangelog(ctx context.Context, source ChangelogState, result changelog.FetchResult, next, timeFetched time.Time) (bool, error) {
	changed, _, err := s.SaveFetchedChangelogWithLifecycle(ctx, source, result, next, timeFetched)
	return changed, err
}

func (s *Store) SaveFetchedChangelogWithLifecycle(ctx context.Context, source ChangelogState, result changelog.FetchResult, next, timeFetched time.Time) (bool, StalePackageStats, error) {
	changed := false
	var stats StalePackageStats
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		settings, err := (&Store{DB: tx}).PackageChangelogSettings(ctx, source.PackageID)
		if err != nil {
			return err
		}
		if settings.Excluded {
			return nil
		}
		changed = false
		for _, version := range result.Versions {
			if version.CommittedAt.IsZero() || version.CommittedAt.After(timeFetched) || version.Version == "" || version.Version == "latest" {
				continue
			}
			base := strings.SplitN(version.Version, ",", 2)[0]
			r := tx.Exec(`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at,brew_commit_sha) VALUES(?,?,?,?,?)
 ON CONFLICT(package_id,version) DO UPDATE SET brew_committed_at=EXCLUDED.brew_committed_at,brew_commit_sha=EXCLUDED.brew_commit_sha
 WHERE package_versions.brew_committed_at IS NULL
 OR package_versions.brew_committed_at<='0001-01-01T00:00:00Z'::timestamptz
 OR package_versions.brew_committed_at>?`, source.PackageID, version.Version, base, version.CommittedAt, version.SHA, timeFetched)
			if r.Error != nil {
				return r.Error
			}
			changed = changed || r.RowsAffected > 0
		}
		stats, err = refreshStalePackages(ctx, tx, timeFetched, []int64{source.PackageID})
		if err != nil {
			return err
		}
		if changed {
			if err := (&Store{DB: tx}).AppendContentChange(ctx, source.PackageID); err != nil {
				return err
			}
		}
		status := "ok"
		if result.NotModified {
			status = "not_modified"
		}
		fields := map[string]any{"etag": nil, "last_modified": nil, "last_status": status, "last_error": nil, "last_fetched_at": timeFetched, "fail_count": 0, "next_fetch_at": next, "updated_at": timeFetched}
		if result.NotModified {
			delete(fields, "etag")
			delete(fields, "last_modified")
		}
		if result.ETag != "" {
			fields["etag"] = result.ETag
		}
		if result.LastModified != "" {
			fields["last_modified"] = result.LastModified
		}
		if err := tx.WithContext(ctx).Table("changelog_sources").Where("id=?", source.ID).Updates(fields).Error; err != nil {
			return err
		}
		if changed {
			p, err := (&Store{DB: tx}).ChangelogCandidate(ctx, source.PackageID)
			if err != nil {
				return err
			}
			identity := cache.PatternLiteral(p.Kind) + ":" + cache.PatternLiteral(p.Token) + ":*"
			return addCacheInvalidation(ctx, tx, fmt.Sprintf("changelog:%d", source.PackageID), "c:rel:*:"+identity, cache.PackageDetailPrefix+identity)
		}
		return nil
	})
	if err != nil {
		return false, StalePackageStats{}, err
	}
	return changed, stats, nil
}
func (s *Store) ChangelogFailure(ctx context.Context, id int64, status, message string, next time.Time) error {
	return s.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET last_status=?,last_error=?,fail_count=fail_count+1,next_fetch_at=?,last_fetched_at=now(),updated_at=now() WHERE id=?", status, message, next, id).Error
}

func (s *Store) UnresolvedHomebrewSources(ctx context.Context) ([]changelog.Candidate, error) {
	var rows []changelog.Candidate
	err := s.DB.WithContext(ctx).Raw(candidateSQL + ` WHERE NOT COALESCE(m.changelog_excluded,false) AND p.kind='cask' AND p.removed_at IS NULL AND NOT p.disabled AND p.ruby_source_path IS NOT NULL AND ` + OfficialTapSQL + ` AND NOT EXISTS(SELECT 1 FROM changelog_sources s WHERE s.package_id=p.id AND s.type='homebrew_commits') ORDER BY p.popularity DESC,p.id`).Scan(&rows).Error
	return rows, err
}
