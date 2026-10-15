package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"gorm.io/gorm"
)

// TapCask is a third-party tap registration joined with the catalog fields the tap sync compares against.
type TapCask struct {
	PackageID                    int64
	Token, FullToken             string
	Tap, PackageTap              string // registered tap, and the tap the catalog row currently holds
	Name, Version                string
	RawHash                      string
	NormalizationVersion         string
	Disabled, Deprecated         bool
	DisableDate, DeprecationDate string
	SourcePath                   string
	ETag, HeadSHA                string
	HistorySHA                   string // newest commit whose history was read into package_versions
	LastError                    string
	CheckedAt                    *time.Time
	RemovedAt                    *time.Time
	CreatedAt, UpdatedAt         time.Time
}

const tapCaskSQL = `SELECT t.package_id,p.token,p.full_token,t.tap,p.tap AS package_tap,p.name,p.version,p.raw_hash,
 p.disabled,p.deprecated,COALESCE(p.disable_date::text,'') AS disable_date,COALESCE(p.deprecation_date::text,'') AS deprecation_date,
 COALESCE(p.dependencies->>'normalizationVersion','') AS normalization_version,t.source_path,COALESCE(t.etag,'') AS e_tag,
 COALESCE(t.head_sha,'') AS head_sha,COALESCE(t.history_sha,'') AS history_sha,COALESCE(t.last_error,'') AS last_error,t.checked_at,p.removed_at,t.created_at,t.updated_at
 FROM tap_casks t JOIN packages p ON p.id=t.package_id`

func (s *Store) TapCasks(ctx context.Context) ([]TapCask, error) {
	rows := []TapCask{}
	if err := s.DB.WithContext(ctx).Raw(tapCaskSQL + " ORDER BY p.token").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load tap casks: %w", err)
	}
	return rows, nil
}

func (s *Store) TapCask(ctx context.Context, packageID int64) (*TapCask, error) {
	var rows []TapCask
	if err := s.DB.WithContext(ctx).Raw(tapCaskSQL+" WHERE t.package_id=?", packageID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load tap cask: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// CaskOwner is the catalog row currently holding a cask token, if any.
type CaskOwner struct {
	ID        int64
	Tap       string
	RemovedAt *time.Time
	Tracked   bool
}

func (s *Store) CaskOwner(ctx context.Context, token string) (*CaskOwner, error) {
	var rows []CaskOwner
	err := s.DB.WithContext(ctx).Raw("SELECT p.id,p.tap,p.removed_at,EXISTS(SELECT 1 FROM tap_casks t WHERE t.package_id=p.id) AS tracked FROM packages p WHERE p.kind='cask' AND p.token=?", token).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load cask owner: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// RegisterTapCask records where a third-party cask file lives; the caller already wrote the catalog row.
// historySHA is the newest commit whose history the caller recorded, or "" for the tap sync to read it.
func (s *Store) RegisterTapCask(ctx context.Context, packageID int64, tap, sourcePath, etag, headSHA, historySHA string, checkedAt time.Time) error {
	return s.DB.WithContext(ctx).Exec(`INSERT INTO tap_casks (package_id,tap,source_path,etag,head_sha,history_sha,checked_at) VALUES (?,?,?,?,?,?,?)
 ON CONFLICT (package_id) DO UPDATE SET tap=EXCLUDED.tap,source_path=EXCLUDED.source_path,etag=EXCLUDED.etag,head_sha=EXCLUDED.head_sha,
 history_sha=EXCLUDED.history_sha,checked_at=EXCLUDED.checked_at,last_error=NULL,updated_at=now()`, packageID, tap, sourcePath, nullable(etag), nullable(headSHA), nullable(historySHA), checkedAt).Error
}

// RecordTapCheck stores the outcome of one tap sync read; an empty lastError marks the cask healthy.
func (s *Store) RecordTapCheck(ctx context.Context, packageID int64, sourcePath, etag, headSHA, lastError string, checkedAt time.Time) error {
	return s.DB.WithContext(ctx).Exec("UPDATE tap_casks SET source_path=?,etag=?,head_sha=?,last_error=?,checked_at=?,updated_at=now() WHERE package_id=?",
		sourcePath, nullable(etag), nullable(headSHA), nullable(lastError), checkedAt, packageID).Error
}

// RecordTapVersionCommit dates a version from the tap's git history, which the stale-package
// lifecycle and release timeline read the same way as Homebrew commit dates.
func (s *Store) RecordTapVersionCommit(ctx context.Context, packageID int64, version, sha string, committedAt time.Time) error {
	return s.DB.WithContext(ctx).Exec("UPDATE package_versions SET brew_committed_at=?,brew_commit_sha=? WHERE package_id=? AND version=? AND brew_committed_at IS NULL",
		committedAt.UTC(), nullable(sha), packageID, version).Error
}

// TapVersion is a version a tap cask file declared and the oldest commit known to declare it.
type TapVersion struct {
	Version, SHA string
	CommittedAt  time.Time
}

// RecordTapVersions stores versions read from a tap cask file's history and returns how many rows changed.
// A version keeps the oldest commit that declared it, so reading a newer window never moves its date. The
// caller holds LockCatalogWrites.
func (s *Store) RecordTapVersions(ctx context.Context, packageID int64, versions []TapVersion, now time.Time) (int, error) {
	if len(versions) == 0 {
		return 0, nil
	}
	changed := 0
	for _, version := range versions {
		if version.Version == "" || version.Version == "latest" || version.CommittedAt.IsZero() || version.CommittedAt.After(now) {
			continue
		}
		base, _, _ := strings.Cut(version.Version, ",")
		r := s.DB.WithContext(ctx).Exec(`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at,brew_commit_sha) VALUES(?,?,?,?,?)
 ON CONFLICT(package_id,version) DO UPDATE SET brew_committed_at=EXCLUDED.brew_committed_at,brew_commit_sha=EXCLUDED.brew_commit_sha
 WHERE package_versions.brew_committed_at IS NULL
 OR package_versions.brew_committed_at<='0001-01-01T00:00:00Z'::timestamptz
 OR package_versions.brew_committed_at>EXCLUDED.brew_committed_at`, packageID, version.Version, base, version.CommittedAt.UTC(), nullable(version.SHA))
		if r.Error != nil {
			return changed, fmt.Errorf("record tap version: %w", r.Error)
		}
		changed += int(r.RowsAffected)
	}
	// The current version may have just been dated, which the stale-package policy reads.
	if _, err := refreshStalePackages(ctx, s.DB, now, []int64{packageID}); err != nil {
		return changed, err
	}
	if changed == 0 {
		return 0, nil
	}
	if err := s.AppendContentChange(ctx, packageID); err != nil {
		return changed, err
	}
	var p struct{ Kind, Token string }
	if err := s.DB.WithContext(ctx).Raw("SELECT kind,token FROM packages WHERE id=?", packageID).Scan(&p).Error; err != nil {
		return changed, err
	}
	identity := cache.PatternLiteral(p.Kind) + ":" + cache.PatternLiteral(p.Token) + ":*"
	return changed, addCacheInvalidation(ctx, s.DB, fmt.Sprintf("changelog:%d", packageID), "c:rel:*:"+identity, "c:pkg:c3:"+identity)
}

// RecordTapHistory marks the history of a tap cask file as read up to sha.
func (s *Store) RecordTapHistory(ctx context.Context, packageID int64, sha string) error {
	return s.DB.WithContext(ctx).Exec("UPDATE tap_casks SET history_sha=?,updated_at=now() WHERE package_id=?", nullable(sha), packageID).Error
}

// UnregisterTapCask stops tracking a tap cask and removes it from the catalog, as Homebrew removals do.
func (s *Store) UnregisterTapCask(ctx context.Context, packageID int64) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		var row struct {
			Kind, Token, Version string
			Tap, PackageTap      string
			RemovedAt            *time.Time
		}
		if err := tx.WithContext(ctx).Raw("SELECT p.kind,p.token,p.version,p.removed_at,t.tap,p.tap AS package_tap FROM tap_casks t JOIN packages p ON p.id=t.package_id WHERE t.package_id=? FOR UPDATE OF t", packageID).Scan(&row).Error; err != nil {
			return fmt.Errorf("load tap cask: %w", err)
		}
		if row.Token == "" {
			return errors.New("tap cask not found")
		}
		if err := tx.WithContext(ctx).Exec("DELETE FROM tap_casks WHERE package_id=?", packageID).Error; err != nil {
			return fmt.Errorf("delete tap cask: %w", err)
		}
		if row.RemovedAt != nil || row.Tap != row.PackageTap {
			return nil
		}
		if err := tx.WithContext(ctx).Exec("UPDATE packages SET removed_at=now(),updated_at=now() WHERE id=?", packageID).Error; err != nil {
			return fmt.Errorf("remove tap package: %w", err)
		}
		if err := tx.WithContext(ctx).Exec("INSERT INTO catalog_changes (package_id,kind,token,op,reason,event_type,version) VALUES (?,?,?,'delete','homebrew','removed',?)", packageID, row.Kind, row.Token, row.Version).Error; err != nil {
			return fmt.Errorf("record removed tap package: %w", err)
		}
		return addCatalogInvalidation(ctx, tx)
	})
}
