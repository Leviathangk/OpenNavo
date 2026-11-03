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

const candidateSQL = `SELECT p.id,p.kind,p.token,p.tap,COALESCE(p.homepage,'') homepage,COALESCE(p.download_url,'') download_url,COALESCE(m.repo_url,'') repo_url,COALESCE(p.raw->>'ruby_source_path','') ruby_source_path,COALESCE(p.raw->>'tap_git_head','') tap_git_head,COALESCE(gf.subject,'') github_repo FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN package_source_facts gf ON gf.package_id=p.id AND gf.source='github' AND gf.status IN ('found','none') AND gf.subject<>''`

// changelogSourceScope keeps Homebrew commit history limited to casks (unchanged) and reads GitHub
// releases for any package kind with a verified public repository. sparkle and webpage have no fetcher.
// An auto github_releases source is read only while the source facts job still verifies its repository as
// public (subject = config repo). When a repository turns private the facts job clears the subject and the
// source stops being due or fetchable, so a token with access never publishes private release notes.
// Manual sources are an editor's decision and are not gated.
const changelogSourceScope = `((s.type='homebrew_commits' AND p.kind='cask') OR (s.type='github_releases' AND (s.resolved_by='manual' OR EXISTS(SELECT 1 FROM package_source_facts vf WHERE vf.package_id=s.package_id AND vf.source='github' AND vf.status IN ('found','none') AND vf.subject<>'' AND vf.subject=s.config->>'repo'))))`

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
			if err := tx.Exec(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,?,?::jsonb,?,'auto') ON CONFLICT(package_id,type) DO UPDATE SET config=EXCLUDED.config,priority=EXCLUDED.priority,etag=NULL,last_modified=NULL,content_hash=CASE WHEN changelog_sources.config<>EXCLUDED.config THEN NULL ELSE changelog_sources.content_hash END,fail_count=0,last_error=NULL,next_fetch_at=now(),updated_at=now() WHERE changelog_sources.resolved_by='auto' AND (changelog_sources.config<>EXCLUDED.config OR changelog_sources.priority<>EXCLUDED.priority)`, id, source.Type, string(data), source.Priority).Error; err != nil {
				return err
			}
			// A repository change leaves the notes of the old repository behind: their tags rarely
			// collide, so the upsert never touches them. Drop the auto source's rows that do not
			// belong to the repository it now records; a manual source is left alone by the guard.
			if source.Type == "github_releases" {
				repo, _ := source.Config["repo"].(string)
				if repo != "" {
					if err := tx.Exec(`DELETE FROM releases r USING changelog_sources s
 WHERE r.package_id=? AND r.source='github_release' AND s.package_id=r.package_id AND s.type='github_releases' AND s.resolved_by='auto'
 AND NOT starts_with(COALESCE(r.source_url,''), ?)`, id, "https://github.com/"+repo+"/releases/").Error; err != nil {
						return err
					}
				}
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
	Archived                  bool
	ContentHash               *string `gorm:"column:content_hash"`
	Homepage                  *string
	CurrentVersion            string
	CurrentVersionCommittedAt *time.Time
}

func (s *Store) ChangelogState(ctx context.Context, id int64) (ChangelogState, error) {
	var row ChangelogState
	r := s.DB.WithContext(ctx).Raw("SELECT s.id,s.package_id,s.enabled,COALESCE(m.changelog_excluded,false) excluded,s.type,s.config,s.etag,s.last_modified,s.last_fetched_at,s.last_status,s.fail_count,s.archived,s.content_hash,p.rank_30d,p.homepage,p.version current_version,v.brew_committed_at current_version_committed_at FROM changelog_sources s JOIN packages p ON p.id=s.package_id LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN package_versions v ON v.package_id=p.id AND v.version=p.version WHERE s.id=? AND "+changelogSourceScope+" AND p.removed_at IS NULL", id).Scan(&row)
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
	err := s.DB.WithContext(ctx).Raw("SELECT s.id FROM changelog_sources s JOIN packages p ON p.id=s.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE NOT COALESCE(m.changelog_excluded,false) AND s.enabled AND "+changelogSourceScope+" AND s.next_fetch_at<=? AND p.removed_at IS NULL AND NOT p.disabled ORDER BY p.popularity DESC,s.id LIMIT 1000", now).Scan(&ids).Error
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
		if source.Type == "github_releases" {
			if changed, err = saveGitHubReleases(ctx, tx, source.PackageID, result.Releases, timeFetched); err != nil {
				return err
			}
		}
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
		if source.Type == "homebrew_commits" {
			// Lifecycle dates come from Homebrew version commits only.
			stats, err = refreshStalePackages(ctx, tx, timeFetched, []int64{source.PackageID})
			if err != nil {
				return err
			}
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
		if result.ContentHash != "" {
			fields["content_hash"] = result.ContentHash
		}
		// A fetch that did not read the repository (304, or an unchanged list) leaves the flag alone.
		if result.Archived != nil {
			fields["archived"] = *result.Archived
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

// UnresolvedHomebrewSources lists packages missing a source they qualify for: official-tap casks without
// homebrew_commits, and packages with a verified public GitHub repository without github_releases (or
// whose repository changed since it was resolved).
func (s *Store) UnresolvedHomebrewSources(ctx context.Context) ([]changelog.Candidate, error) {
	var rows []changelog.Candidate
	err := s.DB.WithContext(ctx).Raw(candidateSQL + ` WHERE NOT COALESCE(m.changelog_excluded,false) AND p.removed_at IS NULL AND NOT p.disabled AND (
 (p.kind='cask' AND p.ruby_source_path IS NOT NULL AND ` + OfficialTapSQL + ` AND NOT EXISTS(SELECT 1 FROM changelog_sources s WHERE s.package_id=p.id AND s.type='homebrew_commits'))
 OR (gf.subject ~ '^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$' AND NOT p.is_font AND NOT EXISTS(SELECT 1 FROM changelog_sources s WHERE s.package_id=p.id AND s.type='github_releases' AND (s.resolved_by='manual' OR s.config->>'repo'=gf.subject)))
) ORDER BY p.popularity DESC,p.id`).Scan(&rows).Error
	return rows, err
}

const releaseVersionsSQL = `SELECT DISTINCT version_base FROM package_versions WHERE package_id=?`

// saveGitHubReleases upserts published GitHub releases into the version history (releases, source
// github_release, keyed by tag). A tag is stored under the package's own version_base when the two
// normalize to the same version, so the timeline joins it to the Homebrew/link version. Rows edited or
// hidden by editors keep their hidden flag and title; unchanged notes (same body hash) are not rewritten.
func saveGitHubReleases(ctx context.Context, tx *gorm.DB, packageID int64, releases []changelog.Release, fetched time.Time) (bool, error) {
	if len(releases) == 0 {
		return false, nil
	}
	var bases []string
	if err := tx.WithContext(ctx).Raw(releaseVersionsSQL, packageID).Scan(&bases).Error; err != nil {
		return false, err
	}
	known := map[string]string{}
	for _, base := range bases {
		if key := changelog.VersionKey(base); key != "" {
			if _, ok := known[key]; !ok {
				known[key] = base
			}
		}
	}
	changed := false
	for _, release := range releases {
		version := release.Version
		if base, ok := known[changelog.VersionKey(version)]; ok {
			version = base
		}
		var body, hash, title any
		if release.Body != "" {
			body, hash = release.Body, release.BodyHash
		}
		if release.Title != "" {
			title = release.Title
		}
		r := tx.WithContext(ctx).Exec(`INSERT INTO releases(package_id,source,source_key,version,title,published_at,source_url,body_markdown,body_hash,is_prerelease,fetched_at,updated_at,source_locale)
 VALUES(?,'github_release',?,?,?,?,?,?,?,?,?,?,'en-US')
 ON CONFLICT(package_id,source,source_key) DO UPDATE SET version=EXCLUDED.version,title=CASE WHEN releases.title_edited THEN releases.title ELSE EXCLUDED.title END,published_at=EXCLUDED.published_at,source_url=EXCLUDED.source_url,body_markdown=EXCLUDED.body_markdown,body_hash=EXCLUDED.body_hash,is_prerelease=EXCLUDED.is_prerelease,fetched_at=EXCLUDED.fetched_at,updated_at=EXCLUDED.fetched_at
 WHERE releases.version IS DISTINCT FROM EXCLUDED.version OR (NOT releases.title_edited AND releases.title IS DISTINCT FROM EXCLUDED.title) OR releases.published_at IS DISTINCT FROM EXCLUDED.published_at OR releases.source_url IS DISTINCT FROM EXCLUDED.source_url OR releases.body_hash IS DISTINCT FROM EXCLUDED.body_hash OR releases.is_prerelease IS DISTINCT FROM EXCLUDED.is_prerelease`,
			packageID, release.Tag, version, title, release.PublishedAt, release.SourceURL, body, hash, release.Prerelease, fetched, fetched)
		if r.Error != nil {
			return false, r.Error
		}
		changed = changed || r.RowsAffected > 0
	}
	return changed, nil
}
