package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeriveTraditional runs inside the caller's content transaction. It bypasses
// external authoring validation, but never bypasses source freshness or limits.
func (s *Store) DeriveTraditional(ctx context.Context, ref content.Ref) (bool, error) {
	if err := s.DB.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+ref.Entity+":"+ref.Key()).Error; err != nil {
		return false, err
	}
	source, err := s.ReadContentSource(ctx, ref)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if source.SourceLocale == traditional.Locale {
		return false, nil
	}
	spec := content.Registry[ref.Entity]
	where, args := textWhere(ref)
	var raw json.RawMessage
	if err = s.contentJSON(ctx, &raw, "SELECT to_jsonb(i) FROM "+spec.Table+" i WHERE "+where+" AND locale='zh-CN'", args...); err != nil {
		return false, err
	}
	if len(raw) == 0 {
		return s.removeTraditional(ctx, ref)
	}
	var cn map[string]any
	if err = json.Unmarshal(raw, &cn); err != nil {
		return false, err
	}
	if (cn["status"] != "source" && cn["status"] != "manual" && cn["status"] != "machine") || (source.SourceLocale != traditional.Source && cn["source_hash"] != source.Hash) {
		return s.removeTraditional(ctx, ref)
	}
	fields := map[string]any{}
	for key := range spec.Fields {
		fields[key] = cn[key]
	}
	_, protected, err := s.ContentGlossary(ctx)
	if err != nil {
		return false, err
	}
	protected = append(protected, source.Protected...)
	slices.Sort(protected)
	protected = slices.Compact(protected)

	// Empty Simplified content removes derived text as well; never retain an old translation.
	hash := content.Hash(traditional.Source, map[string]any{"fields": fields, "withheld": cn["withheld"], "protected": protected})
	var prior struct {
		SimplifiedHash, Converter string
		RestoredAt                *time.Time
		AfterData                 json.RawMessage
	}
	if err = s.DB.WithContext(ctx).Table("derived_traditional").Where("entity=? AND object_key=?", ref.Entity, ref.Key()).Scan(&prior).Error; err != nil {
		return false, err
	}
	var before json.RawMessage
	if err = s.contentJSON(ctx, &before, "SELECT to_jsonb(i) FROM "+spec.Table+" i WHERE "+where+" AND locale='zh-Hant'", args...); err != nil {
		return false, err
	}
	if prior.RestoredAt == nil && prior.SimplifiedHash == hash && prior.Converter == traditional.Version && len(before) > 0 && content.JSONHash(before) == content.JSONHash(prior.AfterData) {
		var old map[string]any
		_ = json.Unmarshal(before, &old)
		if old["source_hash"] == source.Hash {
			return false, nil
		}
	}

	converted, err := traditional.Fields(fields, protected)
	if err != nil {
		return false, err
	}
	if err = ValidateContentFields(ref.Entity, converted); err != nil {
		return false, fmt.Errorf("%w: %w", errTraditionalInvalid, err)
	}
	// Restoring an earlier content revision may already restore exactly the
	// right derived text. Refresh its receipt without changing provenance/times.
	if len(before) > 0 {
		var existing map[string]any
		if err = json.Unmarshal(before, &existing); err != nil {
			return false, err
		}
		oldFields := map[string]any{}
		for key := range spec.Fields {
			oldFields[key] = existing[key]
		}
		if prior.RestoredAt == nil && existing["source_hash"] == source.Hash && existing["model"] == traditional.Version && existing["status"] == "machine" && existing["withheld"] == cn["withheld"] && content.Hash(traditional.Locale, oldFields) == content.Hash(traditional.Locale, converted) {
			err = s.DB.WithContext(ctx).Exec("UPDATE derived_traditional SET simplified_hash=?,converter=?,after_data=?::jsonb,restored_at=NULL,updated_at=clock_timestamp() WHERE entity=? AND object_key=?", hash, traditional.Version, string(before), ref.Entity, ref.Key()).Error
			return false, err
		}
	}
	now := time.Now().UTC()
	row := map[string]any{spec.Key: ref.ID, "locale": traditional.Locale, "source_locale": source.SourceLocale, "source_hash": source.Hash, "status": "machine", "model": traditional.Version, "translated_at": now, "updated_at": now, "fail_reason": nil}
	keys := []clause.Column{{Name: spec.Key}, {Name: "locale"}}
	if ref.Entity == "announcement" {
		row[spec.Key] = ref.Key()
	}
	if ref.Entity == "collection_item" {
		row["package_id"] = ref.SecondaryID
		keys = append(keys, clause.Column{Name: "package_id"})
	}
	if ref.Entity == "article" {
		row["withheld"] = cn["withheld"]
	}
	if ref.Entity == "package" {
		row["source"] = "human"
	}
	for key, v := range converted {
		if key == "sections" {
			if v == nil {
				v = []any{}
			}
			row[key] = gorm.Expr("?::jsonb", jsonText(v))
		} else {
			row[key] = v
		}
	}
	if err = s.DB.WithContext(ctx).Table(spec.Table).Clauses(clause.OnConflict{Columns: keys, DoUpdates: clause.Assignments(row)}).Create(row).Error; err != nil {
		return false, err
	}
	var after json.RawMessage
	if err = s.contentJSON(ctx, &after, "SELECT to_jsonb(i) FROM "+spec.Table+" i WHERE "+where+" AND locale='zh-Hant'", args...); err != nil {
		return false, err
	}
	var backup any
	if len(before) > 0 {
		backup = string(before)
	}
	if err = s.DB.WithContext(ctx).Exec(`INSERT INTO derived_traditional(entity,object_key,simplified_hash,converter,before_data,after_data) VALUES(?,?,?,?,?::jsonb,?::jsonb) ON CONFLICT(entity,object_key) DO UPDATE SET before_data=CASE WHEN derived_traditional.restored_at IS NOT NULL THEN EXCLUDED.before_data ELSE derived_traditional.before_data END,simplified_hash=EXCLUDED.simplified_hash,converter=EXCLUDED.converter,after_data=EXCLUDED.after_data,updated_at=clock_timestamp(),restored_at=NULL`, ref.Entity, ref.Key(), hash, traditional.Version, backup, string(after)).Error; err != nil {
		return false, err
	}
	if err = s.ContentChanged(ctx, ref); err != nil {
		return false, err
	}
	return true, s.QueueTranslationInvalidation(ctx, ref)
}

// BackfillTraditional is restartable and bounded to 128 candidates per page and
// one object per transaction. It uses the same locks and cache outbox as live edits.
func (s *Store) BackfillTraditional(ctx context.Context, dryRun bool) (map[string]int, error) {
	counts := map[string]int{}
	for _, entity := range traditionalEntities() {
		spec := traditionalSpec(entity)
		cursor := ""
		for {
			var rows []struct {
				ObjectKey       string
				ID, SecondaryID int64
			}
			key := "i." + spec.Key + "::text"
			id := "i." + spec.Key
			secondary := "0"
			if entity == "collection_item" {
				key += "||':'||i.package_id::text"
				secondary = "i.package_id"
			}
			if entity == "announcement" || entity == "background_item" {
				id = "0"
			}
			query := "SELECT DISTINCT " + key + " object_key," + id + " id," + secondary + " secondary_id FROM " + spec.Table + " i WHERE locale IN ('zh-CN','zh-Hant') AND " + key + ">? ORDER BY " + key + " LIMIT 128"
			if err := s.DB.WithContext(ctx).Raw(query, cursor).Scan(&rows).Error; err != nil {
				return counts, err
			}
			for _, r := range rows {
				ref := content.Ref{Entity: entity, ID: r.ID, SecondaryID: r.SecondaryID}
				changed := false
				err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := LockCatalogWrites(ctx, tx); err != nil {
						return err
					}
					st := &Store{DB: tx, Redis: s.Redis}
					var err error
					if _, auxiliary := traditionalAuxiliary[entity]; auxiliary {
						changed, err = st.deriveAuxiliaryTraditional(ctx, entity, r.ObjectKey)
					} else {
						changed, err = st.DeriveTraditional(ctx, ref)
					}
					if err != nil {
						return err
					}
					if dryRun {
						return errTraditionalPreview
					}
					return nil
				})
				if errors.Is(err, errTraditionalInvalid) {
					counts[entity+":failed"]++
					cursor = r.ObjectKey
					continue
				}
				if err != nil && !errors.Is(err, errTraditionalPreview) {
					return counts, err
				}
				if changed {
					counts[entity]++
				}
				cursor = r.ObjectKey
			}
			if len(rows) < 128 {
				break
			}
		}
	}
	return counts, nil
}

var errTraditionalInvalid = errors.New("invalid derived Traditional content")

var errTraditionalPreview = errors.New("rollback Traditional Chinese preview")

// RollbackTraditional restores only rows still byte-for-byte equal to the last
// generated snapshot. Later edits are conflicts, never silently overwritten.
func (s *Store) RollbackTraditional(ctx context.Context, dryRun bool) (map[string]int, error) {
	counts := map[string]int{}
	for _, entity := range traditionalEntities() {
		spec := traditionalSpec(entity)
		var columns []string
		if err := s.DB.WithContext(ctx).Raw("SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND is_generated='NEVER' ORDER BY ordinal_position", spec.Table).Scan(&columns).Error; err != nil {
			return counts, err
		}
		for i, col := range columns {
			columns[i] = `"` + strings.ReplaceAll(col, `"`, `""`) + `"`
		}
		names := strings.Join(columns, ",")
		cursor := ""
		for {
			var rows []struct {
				ObjectKey             string
				BeforeData, AfterData json.RawMessage
			}
			if err := s.DB.WithContext(ctx).Table("derived_traditional").Where("entity=? AND object_key>? AND restored_at IS NULL", entity, cursor).Order("object_key").Limit(128).Find(&rows).Error; err != nil {
				return counts, err
			}
			for _, r := range rows {
				ref := content.Ref{Entity: entity}
				parts := strings.Split(r.ObjectKey, ":")
				if entity != "announcement" && entity != "background_item" {
					var err error
					ref.ID, err = strconv.ParseInt(parts[0], 10, 64)
					if err != nil {
						return counts, err
					}
				}
				if entity == "collection_item" {
					if len(parts) != 2 {
						return counts, errors.New("invalid rollback key")
					}
					var err error
					ref.SecondaryID, err = strconv.ParseInt(parts[1], 10, 64)
					if err != nil {
						return counts, err
					}
				}
				outcome := "conflict"
				err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := LockCatalogWrites(ctx, tx); err != nil {
						return err
					}
					if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+entity+":"+r.ObjectKey).Error; err != nil {
						return err
					}
					var where string
					var args []any
					if _, auxiliary := traditionalAuxiliary[entity]; auxiliary {
						where, args = spec.Key+"=?", []any{r.ObjectKey}
					} else {
						where, args = textWhere(ref)
					}
					var result *gorm.DB
					switch {
					case string(r.AfterData) == "null" && (len(r.BeforeData) == 0 || string(r.BeforeData) == "null"):
						// Absence is a valid restored state. Finish this cycle only
						// if no new row has appeared; a later upgrade needs a new backup.
						var exists bool
						if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM "+spec.Table+" WHERE "+where+" AND locale='zh-Hant')", args...).Scan(&exists).Error; err != nil {
							return err
						}
						if exists {
							return nil
						}
						outcome = "restored"
						if err := tx.Exec("UPDATE derived_traditional SET restored_at=clock_timestamp() WHERE entity=? AND object_key=?", entity, r.ObjectKey).Error; err != nil {
							return err
						}
						if dryRun {
							return errTraditionalPreview
						}
						return nil
					case string(r.AfterData) == "null" && len(r.BeforeData) > 0 && string(r.BeforeData) != "null":
						params := append([]any{string(r.BeforeData)}, args...)
						result = tx.Exec("INSERT INTO "+spec.Table+" ("+names+") SELECT "+names+" FROM jsonb_populate_record(NULL::"+spec.Table+",?::jsonb) WHERE NOT EXISTS(SELECT 1 FROM "+spec.Table+" WHERE "+where+" AND locale='zh-Hant')", params...)
					case len(r.BeforeData) == 0 || string(r.BeforeData) == "null":
						result = tx.Exec("DELETE FROM "+spec.Table+" WHERE "+where+" AND locale='zh-Hant' AND to_jsonb("+spec.Table+")=?::jsonb", append(args, string(r.AfterData))...)
					default:
						params := append([]any{string(r.BeforeData)}, args...)
						params = append(params, string(r.AfterData))
						result = tx.Exec("UPDATE "+spec.Table+" SET ("+names+")=(SELECT "+names+" FROM jsonb_populate_record(NULL::"+spec.Table+",?::jsonb)) WHERE "+where+" AND locale='zh-Hant' AND to_jsonb("+spec.Table+")=?::jsonb", params...)
					}
					if result.Error != nil {
						return result.Error
					}
					if result.RowsAffected == 0 {
						return nil
					}
					outcome = "restored"
					if err := tx.Exec("UPDATE derived_traditional SET restored_at=clock_timestamp() WHERE entity=? AND object_key=?", entity, r.ObjectKey).Error; err != nil {
						return err
					}
					st := &Store{DB: tx, Redis: s.Redis}
					if _, auxiliary := traditionalAuxiliary[entity]; auxiliary {
						if err := QueueCacheInvalidation(ctx, tx, "traditional:"+entity+":"+r.ObjectKey, "c:*"); err != nil {
							return err
						}
					} else {
						if err := st.ContentChanged(ctx, ref); err != nil {
							return err
						}
						if err := st.QueueTranslationInvalidation(ctx, ref); err != nil {
							return err
						}
					}
					if dryRun {
						return errTraditionalPreview
					}
					return nil
				})
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23503" {
					counts["conflict"]++
					cursor = r.ObjectKey
					continue
				}
				if err != nil && !errors.Is(err, errTraditionalPreview) {
					return counts, err
				}
				counts[outcome]++
				cursor = r.ObjectKey
			}
			if len(rows) < 128 {
				break
			}
		}
	}
	return counts, nil
}

// Withdraw a derived row when its Simplified dependency disappears or becomes
// stale. Retain a receipt so a migration rollback can restore the prior row.
func (s *Store) removeTraditional(ctx context.Context, ref content.Ref) (bool, error) {
	spec := content.Registry[ref.Entity]
	where, args := textWhere(ref)
	var before json.RawMessage
	if err := s.contentJSON(ctx, &before, "SELECT to_jsonb(i) FROM "+spec.Table+" i WHERE "+where+" AND locale='zh-Hant'", args...); err != nil {
		return false, err
	}
	if len(before) == 0 {
		return false, nil
	}
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO derived_traditional(entity,object_key,simplified_hash,converter,before_data,after_data) VALUES(?,?,'',?,?::jsonb,'null'::jsonb) ON CONFLICT(entity,object_key) DO UPDATE SET before_data=CASE WHEN derived_traditional.restored_at IS NOT NULL THEN EXCLUDED.before_data ELSE derived_traditional.before_data END,simplified_hash='',after_data='null'::jsonb,restored_at=NULL,updated_at=clock_timestamp()`, ref.Entity, ref.Key(), traditional.Version, string(before)).Error; err != nil {
		return false, err
	}
	if err := s.DB.WithContext(ctx).Exec("DELETE FROM "+spec.Table+" WHERE "+where+" AND locale='zh-Hant'", args...).Error; err != nil {
		return false, err
	}
	if err := s.ContentChanged(ctx, ref); err != nil {
		return false, err
	}
	return true, s.QueueTranslationInvalidation(ctx, ref)
}
