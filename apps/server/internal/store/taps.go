package store

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	LastError                    string
	CheckedAt                    *time.Time
	RemovedAt                    *time.Time
	CreatedAt, UpdatedAt         time.Time
}

const tapCaskSQL = `SELECT t.package_id,p.token,p.full_token,t.tap,p.tap AS package_tap,p.name,p.version,p.raw_hash,
 p.disabled,p.deprecated,COALESCE(p.disable_date::text,'') AS disable_date,COALESCE(p.deprecation_date::text,'') AS deprecation_date,
 COALESCE(p.dependencies->>'normalizationVersion','') AS normalization_version,t.source_path,COALESCE(t.etag,'') AS e_tag,
 COALESCE(t.head_sha,'') AS head_sha,COALESCE(t.last_error,'') AS last_error,t.checked_at,p.removed_at,t.created_at,t.updated_at
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
func (s *Store) RegisterTapCask(ctx context.Context, packageID int64, tap, sourcePath, etag, headSHA string, checkedAt time.Time) error {
	return s.DB.WithContext(ctx).Exec(`INSERT INTO tap_casks (package_id,tap,source_path,etag,head_sha,checked_at) VALUES (?,?,?,?,?,?)
 ON CONFLICT (package_id) DO UPDATE SET tap=EXCLUDED.tap,source_path=EXCLUDED.source_path,etag=EXCLUDED.etag,head_sha=EXCLUDED.head_sha,
 checked_at=EXCLUDED.checked_at,last_error=NULL,updated_at=now()`, packageID, tap, sourcePath, nullable(etag), nullable(headSHA), checkedAt).Error
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
