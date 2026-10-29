package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"gorm.io/gorm"
)

// SourceFactPackage is what the source-facts job needs to find an app's repository and bundle
// identifiers in its Homebrew definition.
type SourceFactPackage struct {
	BundleIDs   []string
	ID          int64
	Token       string
	Names       []string
	Homepage    *string
	DownloadURL *string
	RepoURL     *string
	Artifacts   json.RawMessage
}

// SourceFact is one row of package_source_facts.
type SourceFact struct {
	PackageID          int64
	Source             string
	Input              string // Candidates from the latest attempt, including failed attempts.
	Subject            string
	Status             string
	Data               json.RawMessage
	ETag               *string `gorm:"column:etag"`
	CheckedAt          time.Time
	NextCheckAt        time.Time
	FailCount          int
	SignalsCheckedAt   *time.Time `gorm:"column:signals_checked_at"`
	SignalsNextCheckAt *time.Time `gorm:"column:signals_next_check_at"`
	SignalsFailCount   int
}

// SourceFactKey identifies a row.
type SourceFactKey struct {
	PackageID int64
	Source    string
}

// SourceFactPackages lists apps with a page (not fonts, not removed or hidden), most popular first.
func (s *Store) SourceFactPackages(ctx context.Context) ([]SourceFactPackage, error) {
	var rows []struct {
		ID                             int64
		Token                          string
		Names, Artifacts, BundleIDs    []byte
		Homepage, DownloadURL, RepoURL *string
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id,p.token,to_jsonb(p.names) AS names,p.homepage,p.download_url,COALESCE(m.repo_url,'https://github.com/'||l.github_repo) AS repo_url,p.artifacts,to_jsonb(l.bundle_ids) AS bundle_ids
 FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN link_apps l ON l.package_id=p.id
 WHERE p.kind IN ('cask','link') AND p.removed_at IS NULL AND NOT p.is_font AND NOT COALESCE(m.hidden,false)
 ORDER BY p.popularity DESC,p.id`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("query source fact packages: %w", err)
	}
	out := make([]SourceFactPackage, 0, len(rows))
	for _, row := range rows {
		p := SourceFactPackage{ID: row.ID, Token: row.Token, Homepage: row.Homepage, DownloadURL: row.DownloadURL, RepoURL: row.RepoURL, Artifacts: row.Artifacts}
		if len(row.BundleIDs) > 0 {
			if err := json.Unmarshal(row.BundleIDs, &p.BundleIDs); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal(row.Names, &p.Names); err != nil {
			return nil, fmt.Errorf("decode package names: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// SourceFacts loads every row, for scheduling.
func (s *Store) SourceFacts(ctx context.Context) (map[SourceFactKey]SourceFact, error) {
	var rows []SourceFact
	if err := s.DB.WithContext(ctx).Raw(`SELECT package_id,source,input,subject,status,data,etag,checked_at,next_check_at,fail_count,signals_checked_at,signals_next_check_at,signals_fail_count FROM package_source_facts`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query source facts: %w", err)
	}
	out := make(map[SourceFactKey]SourceFact, len(rows))
	for _, row := range rows {
		out[SourceFactKey{row.PackageID, row.Source}] = row
	}
	return out, nil
}

// PublicSourceFacts returns the facts a page may show for one app.
func (s *Store) PublicSourceFacts(ctx context.Context, packageID int64) ([]SourceFact, error) {
	var rows []SourceFact
	if err := s.DB.WithContext(ctx).Raw(`SELECT package_id,source,input,subject,status,data,etag,checked_at,next_check_at,fail_count,signals_checked_at,signals_next_check_at,signals_fail_count FROM package_source_facts WHERE package_id=? AND (status='found' OR (source='github' AND subject<>'')) ORDER BY source`, packageID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query public source facts: %w", err)
	}
	return rows, nil
}

// SaveSourceFact writes a checked row. When what pages show changed, the app's detail cache is
// invalidated after commit (only that app, never c:*).
func (s *Store) SaveSourceFact(ctx context.Context, fact SourceFact, token string, publicChanged bool) error {
	data := fact.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		err := tx.WithContext(ctx).Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data,etag,checked_at,next_check_at,fail_count,signals_checked_at,signals_next_check_at,signals_fail_count)
 VALUES(?,?,?,?,?,?::jsonb,?,?,?,?,?,?,?)
 ON CONFLICT(package_id,source) DO UPDATE SET input=EXCLUDED.input,subject=EXCLUDED.subject,status=EXCLUDED.status,data=EXCLUDED.data,
	 etag=EXCLUDED.etag,checked_at=EXCLUDED.checked_at,next_check_at=EXCLUDED.next_check_at,fail_count=EXCLUDED.fail_count,
 signals_checked_at=EXCLUDED.signals_checked_at,signals_next_check_at=EXCLUDED.signals_next_check_at,signals_fail_count=EXCLUDED.signals_fail_count`,
			fact.PackageID, fact.Source, fact.Input, fact.Subject, fact.Status, string(data), fact.ETag, fact.CheckedAt, fact.NextCheckAt, fact.FailCount,
			fact.SignalsCheckedAt, fact.SignalsNextCheckAt, fact.SignalsFailCount).Error
		if err != nil || !publicChanged {
			return err
		}
		return sourceFactInvalidation(ctx, tx, fact.PackageID, token)
	})
}

// RecordSourceFactSignalFailure backs off a daily GitHub signal refresh while preserving the
// successful license fact and its 30-day schedule.
func (s *Store) RecordSourceFactSignalFailure(ctx context.Context, key SourceFactKey, next time.Time) error {
	return s.DB.WithContext(ctx).Exec(`UPDATE package_source_facts
 SET signals_next_check_at=?,signals_fail_count=signals_fail_count+1
 WHERE package_id=? AND source=? AND subject<>''`, next, key.PackageID, key.Source).Error
}

// RecordSourceFactFailure keeps the successful facts and their check time, but records the
// attempted input with its retry schedule so a source change does not bypass backoff.
// A new row is an error row.
func (s *Store) RecordSourceFactFailure(ctx context.Context, key SourceFactKey, input string, checked, next time.Time) error {
	return s.DB.WithContext(ctx).Exec(`INSERT INTO package_source_facts(package_id,source,input,status,checked_at,next_check_at,fail_count) VALUES(?,?,?,'error',?,?,1)
 ON CONFLICT(package_id,source) DO UPDATE SET input=EXCLUDED.input,next_check_at=EXCLUDED.next_check_at,fail_count=package_source_facts.fail_count+1`,
		key.PackageID, key.Source, input, checked, next).Error
}

// DeleteSourceFact drops a row once the definition no longer points anywhere.
func (s *Store) DeleteSourceFact(ctx context.Context, key SourceFactKey, token string, publicChanged bool) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Exec(`DELETE FROM package_source_facts WHERE package_id=? AND source=?`, key.PackageID, key.Source).Error; err != nil || !publicChanged {
			return err
		}
		return sourceFactInvalidation(ctx, tx, key.PackageID, token)
	})
}

func sourceFactInvalidation(ctx context.Context, tx *gorm.DB, packageID int64, token string) error {
	var kind string
	if err := tx.WithContext(ctx).Raw("SELECT kind FROM packages WHERE id=?", packageID).Scan(&kind).Error; err != nil {
		return err
	}
	// Agent writes enqueue this same canonical entity after the fact transaction.
	// Merge so a later source refresh cannot replace its wider pending scope.
	return mergeCacheInvalidation(ctx, tx, fmt.Sprintf("seo-content:pkg:%d", packageID), cache.PackageDetailPrefix+cache.PatternLiteral(kind)+":"+cache.PatternLiteral(token)+":*")
}
