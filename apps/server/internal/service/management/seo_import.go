package management

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/service/seotune"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

type importedEntry struct {
	Ref           content.Ref    `json:"ref"`
	Field         string         `json:"field"`
	Locale        string         `json:"locale"`
	SourceVersion string         `json:"sourceVersion"`
	State         string         `json:"state"`
	Reason        string         `json:"reason"`
	Evidence      map[string]any `json:"evidence"`
	LastAttemptAt time.Time      `json:"lastAttemptAt"`
	ValueHash     string         `json:"valueHash"`
}
type importRequest struct {
	IdempotencyKey string          `json:"idempotencyKey"`
	Profile        string          `json:"profile"`
	Cursor         map[string]any  `json:"cursor"`
	Entries        []importedEntry `json:"entries"`
}

func (s *Service) importSeoTasks(ctx context.Context, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:task:handle"); err != nil {
		return nil, err
	}
	var request importRequest
	raw, _ := json.Marshal(in.Body)
	if json.Unmarshal(raw, &request) != nil || len(request.Entries) < 1 || len(request.Entries) > 2000 || !validLabel(request.Profile, 100) || len(raw) > 4<<20 {
		return nil, domain.Validation()
	}
	for _, e := range request.Entries {
		if !e.Ref.Valid() || !i18n.Valid(e.Locale) || !validLabel(e.Field, 80) || !validLabel(e.SourceVersion, 200) || (e.State != "processed" && e.State != "deferred") || len(e.Reason) > 2000 || e.LastAttemptAt.IsZero() || e.LastAttemptAt.After(s.now().Add(5*time.Minute)) {
			return nil, domain.Validation()
		}
		if err := assertContentAccess(ctx, e.Ref); err != nil {
			return nil, err
		}
	}
	result, err := seotune.Receipt(ctx, s.Store.DB, seoOwner(ctx), "ImportSeoTasks", request.IdempotencyKey, "Hermes state import", in.Body, domain.CurrentOperation(ctx).DryRun, func(tx *gorm.DB) (any, any, any, error) {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return nil, nil, nil, err
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		results := []any{}
		processed, deferred := 0, 0
		for _, entry := range request.Entries {
			value, version, err := importCurrent(ctx, st, entry)
			if err != nil {
				return nil, nil, nil, err
			}
			data, _ := json.Marshal(value)
			hash := seotasks.Hash(data)
			matched := importValuePresent(value) && entry.ValueHash == hash && entry.SourceVersion == version
			state, reason := "open", "processed_mismatch"
			periodic := entry.Field == "price" || entry.Field == "repoUrl" || entry.Field == "screenshots" || entry.Field == "icon" || entry.Field == "macos"
			var retry *time.Time
			if entry.State == "deferred" {
				state, reason = "deferred", "imported_deferred"
				deferred++
			} else {
				processed++
				if matched {
					state, reason = "done", "matched"
				}
			}
			if state == "deferred" || (state == "done" && periodic) {
				v := entry.LastAttemptAt.Add(30 * 24 * time.Hour)
				retry = &v
			}
			entity := entry.Ref.Entity + ":" + entry.Ref.Key()
			dimensions, _ := json.Marshal([]string{entry.Field, entry.Locale, entry.SourceVersion})
			key := "import:" + entity + ":" + seotasks.Hash(dimensions)
			proof, _ := json.Marshal(entry)
			var task seotasks.Task
			r := tx.Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,evidence,evidence_hash,rule_version,state,result,last_attempt_at,next_retry_at)
    VALUES('hermes_import',?,?,?,?::jsonb,?,'hermes-import-v1',?,jsonb_build_object('reason',?::text),?,?)
    ON CONFLICT(dedupe_key) DO NOTHING RETURNING *`, entity, entry.Locale, key, string(proof), seotasks.Hash(proof), state, reason, entry.LastAttemptAt, retry).Scan(&task)
			if r.Error != nil {
				return nil, nil, nil, r.Error
			}
			if r.RowsAffected == 0 {
				if err := tx.Raw("SELECT * FROM seo_tasks WHERE dedupe_key=? FOR UPDATE", key).Scan(&task).Error; err != nil {
					return nil, nil, nil, err
				}
				// Once execution has started, the server owns its state. Imports remain
				// evidence and reconciliation results, never an override of committed work.
				if task.Attempts == 0 {
					var old importedEntry
					_ = json.Unmarshal(task.Evidence, &old)
					oldProof, _ := json.Marshal(old.Evidence)
					newProof, _ := json.Marshal(entry.Evidence)
					if seotasks.Hash(oldProof) != seotasks.Hash(newProof) {
						state, reason, retry = "open", "evidence_changed", nil
					}
					if !entry.LastAttemptAt.Before(old.LastAttemptAt) {
						if err := tx.Exec(`UPDATE seo_tasks SET state=?,result=jsonb_build_object('reason',?::text),evidence=?::jsonb,evidence_hash=?,last_attempt_at=?,next_retry_at=?,updated_at=clock_timestamp() WHERE id=?`, state, reason, string(proof), seotasks.Hash(proof), entry.LastAttemptAt, retry, task.ID).Error; err != nil {
							return nil, nil, nil, err
						}
						task.State = state
					}
				}
			}
			results = append(results, map[string]any{"taskId": task.ID, "state": task.State, "reconciliation": reason, "currentHash": hash, "currentVersion": version})
		}
		cursor, _ := json.Marshal(request.Cursor)
		if err := tx.Exec(`INSERT INTO seo_import_cursors(owner,profile,cursor) VALUES(?,?,?::jsonb) ON CONFLICT(owner,profile) DO UPDATE SET cursor=EXCLUDED.cursor,updated_at=clock_timestamp()`, seoOwner(ctx), request.Profile, string(cursor)).Error; err != nil {
			return nil, nil, nil, err
		}
		out := map[string]any{"results": results, "processed": processed, "deferred": deferred, "cursor": request.Cursor}
		return nil, request, out, nil
	})
	return result, store.MapAdminError(mapTaskError(err))
}

func importCurrent(ctx context.Context, st *store.Store, e importedEntry) (any, string, error) {
	// Only server-owned, allowlisted fields may participate in reconciliation.
	if e.Ref.Entity == "package" {
		queries := map[string]string{
			"price":       "SELECT price data FROM package_facts WHERE package_id=?",
			"macos":       "SELECT macos data FROM package_facts WHERE package_id=?",
			"repoUrl":     "SELECT to_jsonb(repo_url) data FROM package_meta WHERE package_id=?",
			"icon":        "SELECT to_jsonb(icon_asset_id) data FROM package_meta WHERE package_id=?",
			"screenshots": "SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY s.id),'[]'::jsonb) data FROM package_screenshots s WHERE package_id=?",
		}
		if query, ok := queries[e.Field]; ok {
			var version string
			r := st.DB.WithContext(ctx).Raw("SELECT version FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL", e.Ref.ID).Scan(&version)
			if r.Error != nil {
				return nil, "", r.Error
			}
			if r.RowsAffected == 0 {
				return nil, "", domain.Validation()
			}
			var raw struct{ Data json.RawMessage }
			if err := st.DB.WithContext(ctx).Raw(query, e.Ref.ID).Scan(&raw).Error; err != nil {
				return nil, "", err
			}
			var value any
			if len(raw.Data) > 0 {
				if err := json.Unmarshal(raw.Data, &value); err != nil {
					return nil, "", err
				}
			}
			return value, version, nil
		}
	}
	spec, ok := content.Registry[e.Ref.Entity]
	if !ok {
		return nil, "", domain.Validation()
	}
	valid := false
	for name := range store.ContentAPIFields(func() map[string]any {
		m := map[string]any{}
		for k := range spec.Fields {
			m[k] = nil
		}
		return m
	}()) {
		valid = valid || name == e.Field
	}
	if !valid {
		return nil, "", domain.Validation()
	}
	source, err := st.ReadContentSource(ctx, e.Ref)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	value, err := st.TranslationValue(ctx, e.Ref, e.Locale)
	if err != nil {
		return nil, "", err
	}
	if e.Locale != source.SourceLocale && (value.SourceHash != source.Hash || (value.Status != "manual" && value.Status != "machine")) {
		return nil, source.Hash, nil
	}
	version := source.Hash
	if e.Ref.Entity == "release" {
		if err := st.DB.WithContext(ctx).Raw("SELECT version FROM releases WHERE id=?", e.Ref.ID).Scan(&version).Error; err != nil {
			return nil, "", err
		}
	}
	if e.Ref.Entity == "package" {
		if err := st.DB.WithContext(ctx).Raw("SELECT version FROM packages WHERE id=?", e.Ref.ID).Scan(&version).Error; err != nil {
			return nil, "", err
		}
	}
	v := value.Fields[e.Field]
	if str, ok := v.(string); ok && strings.TrimSpace(str) == "" {
		v = nil
	}
	return v, version, nil
}

// Matching an empty value does not establish that a deferred fact was filled.
func importValuePresent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}
