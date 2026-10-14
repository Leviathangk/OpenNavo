package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"gorm.io/gorm"
)

type CatalogRow struct {
	ID                                                int64
	Kind, Token, RawHash, Version, DescEn, I18nStatus string
	RemovedAt                                         *time.Time
	NormalizationVersion                              string
}
type CatalogMutation struct {
	Package     homebrew.Package
	Previous    *CatalogRow
	RenamedFrom string
}

func (s *Store) WithAdvisoryLock(ctx context.Context, key string, fn func(context.Context) error) (result error) {
	db, err := s.DB.DB()
	if err != nil {
		return fmt.Errorf("get database: %w", err)
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve database connection: %w", err)
	}
	defer func() { result = errors.Join(result, connection.Close()) }()
	var locked bool
	if err := connection.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&locked); err != nil {
		return fmt.Errorf("acquire job lock: %w", err)
	}
	if !locked {
		return &domain.AppError{Code: domain.CodeInvalidState, HTTPStatus: 409}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, err := connection.ExecContext(cleanup, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key)
		result = errors.Join(result, err)
	}()
	return fn(ctx)
}

func (s *Store) CatalogRows(ctx context.Context, kind string) (map[string]*CatalogRow, error) {
	var rows []CatalogRow
	// The kind/token unique index locates the type; read only hashes and change-detection fields instead of loading all raw JSON into memory.
	// Casks from third-party taps belong to the tap sync, so the official feed never removes or renames them.
	err := s.DB.WithContext(ctx).Raw("SELECT p.id,p.kind,p.token,p.raw_hash,p.version,coalesce(p.desc_en,'') AS desc_en,p.removed_at,coalesce(i.status,'') AS i18n_status,coalesce(p.dependencies->>'normalizationVersion','') AS normalization_version FROM packages p LEFT JOIN package_i18n i ON i.package_id=p.id AND i.locale='zh-CN' WHERE p.kind=? AND "+OfficialTapSQL, kind).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load catalog rows: %w", err)
	}
	result := make(map[string]*CatalogRow, len(rows))
	for _, row := range rows {
		result[row.Token] = &row
	}
	return result, nil
}

// CatalogRow loads the change-detection fields of one package whatever its tap; nil when the token is unused.
func (s *Store) CatalogRow(ctx context.Context, kind, token string) (*CatalogRow, error) {
	var rows []CatalogRow
	err := s.DB.WithContext(ctx).Raw("SELECT p.id,p.kind,p.token,p.raw_hash,p.version,coalesce(p.desc_en,'') AS desc_en,p.removed_at,'' AS i18n_status,coalesce(p.dependencies->>'normalizationVersion','') AS normalization_version FROM packages p WHERE p.kind=? AND p.token=?", kind, token).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load catalog row: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Read only active Casks with raw Homebrew definitions; leave historical placeholders for the next full upstream sync.
func (s *Store) VisitOutdatedCasks(ctx context.Context, version string, consume func(json.RawMessage) error) error {
	rows, err := s.DB.WithContext(ctx).Raw("SELECT raw FROM packages p WHERE kind='cask' AND removed_at IS NULL AND dependencies->>'normalizationVersion' IS DISTINCT FROM ? AND jsonb_exists(raw,'token') AND "+OfficialTapSQL+" ORDER BY id", version).Rows()
	if err != nil {
		return fmt.Errorf("read outdated cask definitions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return fmt.Errorf("scan outdated cask: %w", err)
		}
		if err := consume(raw); err != nil {
			return err
		}
	}
	return rows.Err()
}

// OfficialTapSQL matches packages owned by the official Homebrew sync (alias p); third-party taps are tracked in tap_casks.
const OfficialTapSQL = "p.tap LIKE 'homebrew/%'"

var catalogColumns = []string{"kind", "token", "full_token", "tap", "name", "names", "desc_en", "homepage", "version", "version_base", "license", "auto_updates", "deprecated", "deprecation_date", "deprecation_reason", "deprecation_replacement", "disabled", "disable_date", "disable_reason", "disable_replacement", "download_url", "artifacts", "dependencies", "conflicts_with", "caveats", "min_macos", "supports_arm64", "supports_x86_64", "keg_only", "ruby_source_path", "tap_git_head", "generated_date", "is_font", "raw", "raw_hash"}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func catalogValues(item homebrew.Package) []any {
	names, _ := json.Marshal(item.Names)
	return []any{item.Kind, item.Token, item.FullToken, item.Tap, item.Name, string(names), nullable(item.Desc), nullable(item.Homepage), item.Version, item.VersionBase, nullable(item.License), item.AutoUpdates, item.Deprecated, nullable(item.DeprecationDate), nullable(item.DeprecationReason), nullable(item.DeprecationReplacement), item.Disabled, nullable(item.DisableDate), nullable(item.DisableReason), nullable(item.DisableReplacement), nullable(item.DownloadURL), string(item.Artifacts), string(item.Dependencies), string(item.ConflictsWith), nullable(item.Caveats), nullable(item.MinMacos), item.SupportsArm64, item.SupportsX8664, item.KegOnly, nullable(item.RubySourcePath), nullable(item.TapGitHead), nullable(item.GeneratedDate), item.IsFont, string(item.Raw), item.RawHash}
}

func addOutbox(ctx context.Context, tx *gorm.DB, jobType string, ref string, id int64) error {
	payload, err := json.Marshal(map[string]int64{ref: id})
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("INSERT INTO job_outbox (unique_key,job_type,payload) VALUES (?,?,?::jsonb) ON CONFLICT (unique_key) DO NOTHING", fmt.Sprintf("%s:%d", jobType, id), jobType, string(payload)).Error
}

func (s *Store) ApplyCatalogBatch(ctx context.Context, mutations []CatalogMutation) error {
	_, err := s.ApplyCatalogBatchWithLifecycle(ctx, mutations, time.Now().UTC())
	return err
}

func (s *Store) ApplyCatalogBatchWithLifecycle(ctx context.Context, mutations []CatalogMutation, now time.Time) (StalePackageStats, error) {
	var stats StalePackageStats
	if len(mutations) == 0 {
		return stats, nil
	}
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		for _, mutation := range mutations {
			if mutation.RenamedFrom != "" {
				if err := tx.WithContext(ctx).Exec("UPDATE packages SET token=? WHERE id=?", mutation.Package.Token, mutation.Previous.ID).Error; err != nil {
					return fmt.Errorf("rename package: %w", err)
				}
			}
		}
		placeholders := make([]string, len(catalogColumns))
		updates := []string{}
		for i, column := range catalogColumns {
			placeholders[i] = "?"
			switch column {
			case "names":
				placeholders[i] = "ARRAY(SELECT jsonb_array_elements_text(?::jsonb))"
			case "raw", "artifacts", "dependencies", "conflicts_with":
				placeholders[i] = "?::jsonb"
			}
			if column != "kind" && column != "token" {
				updates = append(updates, column+"=EXCLUDED."+column)
			}
		}
		updates = append(updates, "version_changed_at=CASE WHEN packages.version IS DISTINCT FROM EXCLUDED.version THEN now() ELSE packages.version_changed_at END", "download_size=CASE WHEN packages.version IS DISTINCT FROM EXCLUDED.version THEN NULL ELSE packages.download_size END", "removed_at=NULL", "updated_at=now()")
		groups := make([]string, 0, len(mutations))
		arguments := make([]any, 0, len(mutations)*len(catalogColumns))
		for _, mutation := range mutations {
			groups = append(groups, "("+strings.Join(placeholders, ",")+")")
			arguments = append(arguments, catalogValues(mutation.Package)...)
		}
		query := "INSERT INTO packages (" + strings.Join(catalogColumns, ",") + ") VALUES " + strings.Join(groups, ",") + " ON CONFLICT (kind,token) DO UPDATE SET " + strings.Join(updates, ",") + " RETURNING id,token"
		var inserted []struct {
			ID    int64
			Token string
		}
		if err := tx.WithContext(ctx).Raw(query, arguments...).Scan(&inserted).Error; err != nil {
			return fmt.Errorf("upsert catalog batch: %w", err)
		}
		ids := map[string]int64{}
		for _, row := range inserted {
			ids[row.Token] = row.ID
		}
		for _, mutation := range mutations {
			item := mutation.Package
			id := ids[item.Token]
			if id == 0 {
				return errors.New("upsert did not return package ID")
			}
			if mutation.RenamedFrom != "" {
				if err := tx.WithContext(ctx).Exec("INSERT INTO catalog_changes (package_id,kind,token,op,reason) VALUES (?,?,?,'delete','homebrew')", id, item.Kind, mutation.RenamedFrom).Error; err != nil {
					return fmt.Errorf("record old token: %w", err)
				}
			}
			isNew := mutation.Previous == nil
			versionChanged := !isNew && mutation.Previous.Version != item.Version
			var eventType, previousVersion any
			if isNew || mutation.Previous.RemovedAt != nil {
				eventType = "created"
			} else {
				previousVersion = mutation.Previous.Version
				if mutation.RenamedFrom != "" {
					eventType = "renamed"
				} else if versionChanged {
					var seen int64
					if err := tx.WithContext(ctx).Table("package_versions").Where("package_id=? AND version=?", id, item.Version).Count(&seen).Error; err != nil {
						return err
					}
					if seen == 0 {
						eventType = "updated"
					}
				}
			}
			if err := tx.WithContext(ctx).Exec("INSERT INTO catalog_changes (package_id,kind,token,op,reason,event_type,version,previous_version,previous_token) VALUES (?,?,?,'upsert','homebrew',?,?,?,?)", id, item.Kind, item.Token, eventType, item.Version, previousVersion, nullable(mutation.RenamedFrom)).Error; err != nil {
				return fmt.Errorf("record package change: %w", err)
			}
			// Former tokens keep resolving (WP2): the observed rename and the tokens Homebrew declares.
			aliases := map[string]string{}
			if item.Kind == "cask" {
				for _, token := range item.OldTokens {
					aliases[token] = "homebrew"
				}
			}
			if mutation.RenamedFrom != "" {
				aliases[mutation.RenamedFrom] = "renamed"
			}
			for token, source := range aliases {
				if token == item.Token {
					continue
				}
				if err := tx.WithContext(ctx).Exec("INSERT INTO package_aliases (kind,token,package_id,source) VALUES (?,?,?,?) ON CONFLICT (kind,token) DO UPDATE SET package_id=EXCLUDED.package_id,source=EXCLUDED.source", item.Kind, token, id, source).Error; err != nil {
					return fmt.Errorf("record package alias: %w", err)
				}
			}
			if isNew || versionChanged {
				if err := tx.WithContext(ctx).Exec("INSERT INTO package_versions (package_id,version,version_base,first_seen_at) VALUES (?,?,?,now()) ON CONFLICT (package_id,version) DO NOTHING", id, item.Version, item.VersionBase).Error; err != nil {
					return fmt.Errorf("record package version: %w", err)
				}
			}
			if item.ChineseName != "" {
				if err := tx.WithContext(ctx).Exec("INSERT INTO package_i18n (package_id,locale,display_name,status,source) VALUES (?,'zh-CN',?,'machine','homebrew') ON CONFLICT (package_id,locale) DO UPDATE SET display_name=EXCLUDED.display_name,updated_at=now() WHERE package_i18n.source<>'human' AND package_i18n.status<>'manual'", id, item.ChineseName).Error; err != nil {
					return fmt.Errorf("save Homebrew Chinese name: %w", err)
				}
			}
			if versionChanged {
				var sources []int64
				if err := tx.WithContext(ctx).Raw("SELECT id FROM changelog_sources WHERE package_id=? AND enabled AND type='homebrew_commits'", id).Scan(&sources).Error; err != nil {
					return err
				}
				if len(sources) == 0 {
					if err := addOutbox(ctx, tx, "changelog:resolve", "packageId", id); err != nil {
						return err
					}
				} else {
					for _, sourceID := range sources {
						if err := addOutbox(ctx, tx, "changelog:fetch", "sourceId", sourceID); err != nil {
							return err
						}
					}
				}
				if item.Kind == "cask" {
					if err := addOutbox(ctx, tx, "assets:download-size", "packageId", id); err != nil {
						return err
					}
				}
			}
		}
		searchIDs := make([]int64, 0, len(ids))
		for _, id := range ids {
			searchIDs = append(searchIDs, id)
		}
		if err := UpdateSearchIndex(ctx, tx, searchIDs); err != nil {
			return err
		}
		var err error
		stats, err = refreshStalePackages(ctx, tx, now, searchIDs)
		if err != nil {
			return err
		}
		return addCatalogInvalidation(ctx, tx)
	})
	if err != nil {
		return StalePackageStats{}, err
	}
	return stats, nil
}

func (s *Store) RemoveCatalogRows(ctx context.Context, rows []*CatalogRow) error {
	if len(rows) == 0 {
		return nil
	}
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		ids := make([]int64, 0, len(rows))
		groups := make([]string, 0, len(rows))
		arguments := make([]any, 0, len(rows)*3)
		for _, row := range rows {
			ids = append(ids, row.ID)
			groups = append(groups, "(?,?,?,'delete','homebrew','removed',?)")
			arguments = append(arguments, row.ID, row.Kind, row.Token, row.Version)
		}
		if err := tx.WithContext(ctx).Exec("UPDATE packages SET removed_at=now(),updated_at=now() WHERE id IN ? AND removed_at IS NULL", ids).Error; err != nil {
			return fmt.Errorf("mark removed packages: %w", err)
		}
		if err := tx.WithContext(ctx).Exec("INSERT INTO catalog_changes (package_id,kind,token,op,reason,event_type,version) VALUES "+strings.Join(groups, ","), arguments...).Error; err != nil {
			return fmt.Errorf("record removed packages: %w", err)
		}
		return addCatalogInvalidation(ctx, tx)
	})
}

func (s *Store) DispatchOutbox(ctx context.Context, deliver func(context.Context, string, json.RawMessage) error) (int, error) {
	if err := s.DispatchCacheInvalidations(ctx); err != nil {
		return 0, err
	}
	delivered := 0
	dueBefore := time.Now().UTC()
	for {
		batchCount, batchSize := 0, 0
		err := s.WithTx(ctx, func(tx *gorm.DB) error {
			var rows []struct {
				ID               int64
				JobType, Payload string
			}
			if err := tx.WithContext(ctx).Raw("SELECT id,job_type,payload::text FROM job_outbox o WHERE job_type IN ('catalog:sync','analytics:sync','snapshot:build','translate:content','cleanup','assets:download-size','changelog:schedule','changelog:resolve','changelog:fetch','facts:sources','taps:sync') AND available_at<=? AND NOT EXISTS (SELECT 1 FROM packages p WHERE p.kind='formula' AND (p.id=(o.payload->>'packageId')::bigint OR p.id=(SELECT cs.package_id FROM changelog_sources cs WHERE cs.id=(o.payload->>'sourceId')::bigint))) ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED", dueBefore).Scan(&rows).Error; err != nil {
				return fmt.Errorf("load job outbox: %w", err)
			}
			if len(rows) == 0 {
				return nil
			}
			batchSize = len(rows)
			ids := make([]int64, 0, len(rows))
			for _, row := range rows {
				if err := deliver(ctx, row.JobType, json.RawMessage(row.Payload)); err != nil {
					if errors.Is(err, jobs.ErrOutboxDeferred) {
						// Defer this row so other objects can be delivered, avoiding repeated selection of the same job in this pass.
						if err := tx.WithContext(ctx).Exec("UPDATE job_outbox SET available_at=GREATEST(clock_timestamp(),?::timestamptz)+interval '1 second' WHERE id=?", dueBefore, row.ID).Error; err != nil {
							return fmt.Errorf("defer job outbox: %w", err)
						}
						continue
					}
					return err
				}
				ids = append(ids, row.ID)
			}
			if len(ids) > 0 {
				if err := tx.WithContext(ctx).Exec("DELETE FROM job_outbox WHERE id IN ?", ids).Error; err != nil {
					return fmt.Errorf("acknowledge job outbox: %w", err)
				}
			}
			batchCount = len(ids)
			return nil
		})
		if err != nil {
			return delivered, err
		}
		delivered += batchCount
		if batchSize == 0 {
			return delivered, nil
		}
	}
}
