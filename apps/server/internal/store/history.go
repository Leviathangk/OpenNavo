package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
)

type HistoryState map[string][]map[string]any
type historyTable struct {
	Name, Where, Columns string
	Args                 []any
}

var packageLifecycleFields = [...]string{"disabled", "stale_disabled", "disable_date", "disable_reason", "disable_replacement", "disabled_override_at", "disabled_override_date", "disabled_override_reason", "disabled_override_replacement", "disabled_override_stale", "disabled_override_version"}
var packageDisabledOverrideFields = [...]string{"disabled_override_at", "disabled_override_date", "disabled_override_reason", "disabled_override_replacement", "disabled_override_stale", "disabled_override_version"}

func historyTables(ref content.Ref, includePackageLifecycle bool) []historyTable {
	spec, ok := content.Registry[ref.Entity]
	if !ok {
		table := map[string]string{"synonym": "search_synonyms", "glossary": "i18n_glossary", "feedback": "feedback", "asset": "assets"}[ref.Entity]
		if table == "" {
			return nil
		}
		return []historyTable{{table, "id=?", "", []any{ref.ID}}}
	}
	where, args := "id=?", []any{ref.ID}
	cols := ""
	if ref.Entity == "package" {
		cols = "id,source_locale,download_size"
		if includePackageLifecycle {
			cols += ",disabled,stale_disabled,disable_date,disable_reason,disable_replacement,disabled_override_at,disabled_override_date,disabled_override_reason,disabled_override_replacement,disabled_override_stale,disabled_override_version"
		}
	}
	if ref.Entity == "desktop_release" {
		cols = "id,source_locale"
	}
	if ref.Entity == "announcement" {
		where, args = "key=?", []any{ref.Key()}
	}
	if ref.Entity == "collection_item" {
		where, args = "collection_id=? AND package_id=?", []any{ref.ID, ref.SecondaryID}
	}
	out := []historyTable{{spec.Parent, where, cols, args}}
	tw, ta := textWhere(ref)
	out = append(out, historyTable{spec.Table, tw, "", ta})
	if ref.Entity == "package" {
		out = append(out, historyTable{"package_meta", "package_id=?", "", args}, historyTable{"package_categories", "package_id=?", "", args},
			historyTable{"package_fact_records", "package_id=?", "", args}, historyTable{"package_facts", "package_id=?", "", args}, historyTable{"package_alternatives", "package_id=?", "", args})
	}
	if ref.Entity == "collection" {
		out = append(out, historyTable{"collection_setup_criteria", "collection_id=?", "", args}, historyTable{"collection_items", "collection_id=?", "", args}, historyTable{"collection_item_i18n", "collection_id=?", "", args})
	}
	return out
}
func (s *Store) HistorySnapshot(ctx context.Context, ref content.Ref) (HistoryState, error) {
	return s.historySnapshot(ctx, ref, false)
}

// HistorySnapshotWithPackageLifecycle includes upstream disabled state for the
// explicit clear-disabled operation. Normal package history remains unchanged.
func (s *Store) HistorySnapshotWithPackageLifecycle(ctx context.Context, ref content.Ref) (HistoryState, error) {
	return s.historySnapshot(ctx, ref, true)
}

// HistoryStateIncludesPackageLifecycle identifies snapshots captured for an
// explicit clear-disabled operation. Normal package history intentionally omits
// upstream lifecycle fields so catalog synchronization remains authoritative.
func HistoryStateIncludesPackageLifecycle(state HistoryState) bool {
	for _, row := range state["packages"] {
		for _, field := range packageLifecycleFields {
			if _, ok := row[field]; ok {
				return true
			}
		}
	}
	return false
}

// HistoryMergeBefore preserves the earlier snapshot while carrying lifecycle
// fields from the earliest later revision that captured them. Missing fields
// mean uncaptured, so only explicit values (including false and null) are copied.
func HistoryMergeBefore(earlier, later HistoryState) HistoryState {
	laterRows := historyRows(later["packages"])
	for key, row := range historyRows(earlier["packages"]) {
		for _, field := range packageLifecycleFields {
			if _, exists := row[field]; exists {
				continue
			}
			if value, exists := laterRows[key][field]; exists {
				row[field] = value
			}
		}
	}
	return earlier
}

// HistoryMergeAfter preserves the latest snapshot while carrying lifecycle
// fields from the latest earlier revision that captured them. Explicit values,
// including null, take precedence over fallback values just as for before.
func HistoryMergeAfter(earlier, later HistoryState) HistoryState {
	return HistoryMergeBefore(later, earlier)
}

func (s *Store) historySnapshot(ctx context.Context, ref content.Ref, includePackageLifecycle bool) (HistoryState, error) {
	tables := historyTables(ref, includePackageLifecycle)
	if len(tables) == 0 {
		return nil, nil
	}
	out := HistoryState{}
	for i, t := range tables {
		expr := "to_jsonb(r)"
		if t.Columns != "" {
			pairs := []string{}
			for _, c := range strings.Split(t.Columns, ",") {
				pairs = append(pairs, "'"+c+"',r."+c)
			}
			expr = "jsonb_build_object(" + strings.Join(pairs, ",") + ")"
		}
		rows, err := s.JSONRows(ctx, "SELECT "+expr+" data FROM "+t.Name+" r WHERE "+t.Where+" ORDER BY to_jsonb(r)::text", t.Args...)
		if err != nil {
			return nil, err
		}
		if i == 0 && len(rows) == 0 {
			return nil, nil
		}
		out[t.Name] = []map[string]any{}
		for _, raw := range rows {
			var row map[string]any
			if err := json.Unmarshal(raw, &row); err != nil {
				return nil, err
			}
			if includePackageLifecycle && ref.Entity == "package" && i == 0 && row["stale_disabled"] == true && row["disable_reason"] == nil {
				row["disable_reason"] = "stale_version"
			}
			out[t.Name] = append(out[t.Name], row)
		}
	}
	return out, nil
}
func HistoryHash(state HistoryState) string {
	raw, _ := json.Marshal(state)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// HistoryHashMatches accepts snapshots from before build-specific notes and private visibility.
// Only migration defaults/backfills may be omitted; real visibility, reason or build edits conflict.
// Never mutate the snapshot.
func HistoryHashMatches(state HistoryState, expected string) bool {
	if HistoryHash(state) == expected {
		return true
	}
	candidates := []HistoryState{state}
	if rows := state["package_meta"]; len(rows) > 0 {
		legacy := maps.Clone(state)
		legacy["package_meta"] = make([]map[string]any, len(rows))
		for i, row := range rows {
			copy := maps.Clone(row)
			if row["private"] == false {
				delete(copy, "private")
			}
			if row["visibility_reason"] == "" || row["hidden"] == true && row["visibility_reason"] == "Legacy setting; reason not recorded." {
				delete(copy, "visibility_reason")
			}
			legacy["package_meta"][i] = copy
		}
		if HistoryHash(legacy) == expected {
			return true
		}
		candidates = append(candidates, legacy)
	}
	for _, candidate := range candidates {
		rows := candidate["releases"]
		if len(rows) == 0 {
			continue
		}
		legacy := maps.Clone(candidate)
		legacy["releases"] = make([]map[string]any, len(rows))
		for i, row := range rows {
			legacy["releases"][i] = maps.Clone(row)
			if row["package_version_id"] == nil {
				delete(legacy["releases"][i], "package_version_id")
			}
		}
		if HistoryHash(legacy) == expected {
			return true
		}
	}
	return false
}
func historyAssets(states ...HistoryState) []int64 {
	ids := []int64{}
	for _, state := range states {
		for _, rows := range state {
			for _, row := range rows {
				for _, k := range []string{"asset_id", "icon_asset_id", "cover_asset_id"} {
					if n, ok := row[k].(float64); ok && !slices.Contains(ids, int64(n)) {
						ids = append(ids, int64(n))
					}
				}
			}
		}
	}
	return ids
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func (s *Store) RecordRevision(ctx context.Context, ref content.Ref, before, after HistoryState, action string, op *domain.Operation) (int64, error) {
	if before == nil && after == nil || HistoryHash(before) == HistoryHash(after) {
		return 0, nil
	}
	if op == nil {
		op = &domain.Operation{RequestID: uuid.NewString(), Name: "translateContent", RequiredPermissions: []string{"translation:review"}}
	}
	if op.RequestID == "" {
		op.RequestID = uuid.NewString()
	}
	if len(op.RequiredPermissions) == 0 {
		return 0, fmt.Errorf("missing revision permissions")
	}
	actorType := "admin"
	if op.AgentClientID > 0 {
		actorType = "agent"
	} else if op.ActorID == nil {
		actorType = "system"
	}
	if err := s.DB.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "history:"+ref.Entity+":"+ref.Key()).Error; err != nil {
		return 0, err
	}
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO content_revision_requests(request_id,actor_id) VALUES(?,?) ON CONFLICT DO NOTHING`, op.RequestID, op.ActorID).Error; err != nil {
		return 0, err
	}
	var id int64
	var previous, next any
	if before != nil {
		previous = jsonText(before)
	}
	if after != nil {
		next = jsonText(after)
	}
	if action == "" {
		action = "update"
		if before == nil {
			action = "create"
		}
		if after == nil {
			action = "delete"
		}
	}
	assets := jsonText(historyAssets(before, after))
	permissions := jsonText(op.RequiredPermissions)
	err := s.DB.WithContext(ctx).Raw(`INSERT INTO content_revisions(entity,object_key,version,action,operation_id,required_permissions,asset_ids,actor_type,actor_id,actor_name,request_id,before_data,after_data,before_hash,after_hash,locale) VALUES(?,?,COALESCE((SELECT max(version)+1 FROM content_revisions WHERE entity=? AND object_key=?),1),?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),ARRAY(SELECT jsonb_array_elements_text(?::jsonb)::bigint),?,?,?,?,?::jsonb,?::jsonb,?,?,NULLIF(?,'')) RETURNING id`, ref.Entity, ref.Key(), ref.Entity, ref.Key(), action, op.Name, permissions, assets, actorType, op.ActorID, op.ActorName, op.RequestID, previous, next, HistoryHash(before), HistoryHash(after), op.Locale).Scan(&id).Error
	if err != nil {
		return 0, err
	}
	if err := s.DB.WithContext(ctx).Exec(`UPDATE content_revision_requests SET revision_count=revision_count+1 WHERE request_id=?`, op.RequestID).Error; err != nil {
		return 0, err
	}
	if after == nil && slices.Contains([]string{"category", "collection", "feature", "synonym", "glossary", "screenshot"}, ref.Entity) {
		if err := s.DB.WithContext(ctx).Exec(`INSERT INTO content_trash(entity,object_key,label,snapshot,snapshot_hash,asset_ids,actor_type,actor_id,actor_name,request_id) VALUES(?,?,?,?::jsonb,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)::bigint),?,?,?,?)`, ref.Entity, ref.Key(), ref.Entity+" #"+ref.Key(), previous, HistoryHash(before), assets, actorType, op.ActorID, op.ActorName, op.RequestID).Error; err != nil {
			return 0, err
		}
	}
	if before == nil && after != nil {
		if err := s.DB.WithContext(ctx).Exec("UPDATE content_trash SET restored_at=now(),restored_by_request_id=? WHERE entity=? AND object_key=? AND restored_at IS NULL", op.RequestID, ref.Entity, ref.Key()).Error; err != nil {
			return 0, err
		}
	}
	urls, err := s.HistoryURLs(ctx, ref, op.WebBaseURL, before, after)
	if err != nil {
		return 0, err
	}
	if err := s.DB.WithContext(ctx).Exec("UPDATE content_revisions SET affected_urls=?::jsonb WHERE id=?", jsonText(urls), id).Error; err != nil {
		return 0, err
	}
	if after == nil {
		if err := s.DB.WithContext(ctx).Exec("UPDATE content_trash SET affected_urls=?::jsonb WHERE entity=? AND object_key=? AND restored_at IS NULL", jsonText(urls), ref.Entity, ref.Key()).Error; err != nil {
			return 0, err
		}
	}
	// Trash retention is independent; prune only revisions here and retain original request totals to reject partial undo.
	err = s.DB.WithContext(ctx).Exec(`DELETE FROM content_revisions WHERE entity=? AND object_key=? AND id NOT IN (SELECT id FROM content_revisions WHERE entity=? AND object_key=? ORDER BY version DESC LIMIT 50)`, ref.Entity, ref.Key(), ref.Entity, ref.Key()).Error
	return id, err
}
func HistoryRef(entity, key string) (content.Ref, error) {
	r := content.Ref{Entity: entity}
	if entity == "announcement" && key == "desktop.announcement" {
		return r, nil
	}
	parts := strings.Split(key, ":")
	var err error
	r.ID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || r.ID < 1 {
		return r, domain.Rejected("object_key_invalid")
	}
	if entity == "collection_item" {
		if len(parts) != 2 {
			return r, domain.Rejected("object_key_invalid")
		}
		r.SecondaryID, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || r.SecondaryID < 1 {
			return r, domain.Rejected("object_key_invalid")
		}
	}
	if len(historyTables(r, false)) == 0 {
		return r, domain.RejectedDetail("history_entity_invalid", entity)
	}
	return r, nil
}

// Restore snapshots only for the server's fixed table list; never use request-provided SQL identifiers.
func (s *Store) RestoreHistoryState(ctx context.Context, ref content.Ref, state HistoryState) error {
	tables := historyTables(ref, false)
	if len(tables) == 0 {
		return domain.RejectedDetail("history_entity_invalid", ref.Entity)
	}
	current, err := s.HistorySnapshot(ctx, ref)
	if err != nil {
		return err
	}
	if ref.Entity == "release" && state != nil {
		if err := validateReleaseNotesHistory(current, state); err != nil {
			return err
		}
	}
	if ref.Entity == "package" && state != nil && len(state["package_meta"]) == 0 && len(current["package_meta"]) > 0 {
		private, reason := historyVisibility(current["package_meta"][0])
		if private || reason != "" {
			// A pre-migration snapshot may predate the metadata row itself.
			// Recreate its defaults while retaining the later visibility group.
			state = maps.Clone(state)
			state["package_meta"] = []map[string]any{{"package_id": ref.ID, "private": private, "visibility_reason": reason}}
		}
	}
	for i := len(tables) - 1; i >= 1; i-- {
		t := tables[i]
		if err := s.DB.WithContext(ctx).Exec("DELETE FROM "+t.Name+" WHERE "+t.Where, t.Args...).Error; err != nil {
			return err
		}
	}
	parent := tables[0]
	if state == nil {
		return s.DB.WithContext(ctx).Exec("DELETE FROM "+parent.Name+" WHERE "+parent.Where, parent.Args...).Error
	}
	for i, t := range tables {
		rows := state[t.Name]
		if i == 0 && len(rows) != 1 {
			return domain.Rejected("history_snapshot_invalid")
		}
		for _, row := range rows {
			if ref.Entity == "package" && t.Name == "package_meta" {
				// Full snapshots from before 00126 have no visibility group. Keep
				// the live decision when recreating this child row instead of using
				// INSERT defaults, which would silently make a private app public.
				if _, exists := row["private"]; !exists {
					row = maps.Clone(row)
					live := historyRows(current["package_meta"])
					for key := range historyRows([]map[string]any{row}) {
						row["private"], row["visibility_reason"] = historyVisibility(live[key])
					}
				}
			}
			if ref.Entity == "announcement" && t.Name == "app_config" {
				if value, ok := row["value"].(map[string]any); ok && (value["sourceLocale"] == nil || value["sourceLocale"] == "") {
					// Snapshots predating English authoring used Chinese implicitly.
					// Normalize only the restored copy, preserving historical data and hashes.
					row = maps.Clone(row)
					value = maps.Clone(value)
					value["sourceLocale"] = "zh-CN"
					row["value"] = value
				}
			}
			keys := []string{}
			for k := range row {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			columns := strings.Join(keys, ",")
			if i == 0 && current != nil {
				sets := []string{}
				for _, k := range keys {
					if k == "id" || k == "key" || k == "collection_id" || k == "package_id" {
						continue
					}
					// populate_record converts JSON null to SQL NULL; announcement seeds allow the former while the column forbids the latter.
					if ref.Entity == "announcement" && t.Name == "app_config" && k == "value" {
						sets = append(sets, "value=COALESCE(v.value,'null'::jsonb)")
						continue
					}
					sets = append(sets, k+"=v."+k)
				}
				args := append([]any{jsonText(row)}, t.Args...)
				if err := s.DB.WithContext(ctx).Exec("UPDATE "+t.Name+" SET "+strings.Join(sets, ",")+" FROM jsonb_populate_record(NULL::"+t.Name+",?::jsonb) v WHERE "+qualifiedWhere(t.Where, t.Name), args...).Error; err != nil {
					return MapAdminError(err)
				}
			} else {
				selected := append([]string(nil), keys...)
				if ref.Entity == "announcement" && t.Name == "app_config" {
					for index, key := range selected {
						if key == "value" {
							selected[index] = "COALESCE(value,'null'::jsonb)"
						}
					}
				}
				if err := s.DB.WithContext(ctx).Exec("INSERT INTO "+t.Name+" ("+columns+") OVERRIDING SYSTEM VALUE SELECT "+strings.Join(selected, ",")+" FROM jsonb_populate_record(NULL::"+t.Name+",?::jsonb)", jsonText(row)).Error; err != nil {
					return MapAdminError(err)
				}
			}
		}
	}
	return nil
}
func qualifiedWhere(where, table string) string {
	parts := strings.Split(where, " AND ")
	for i, p := range parts {
		parts[i] = table + "." + p
	}
	return strings.Join(parts, " AND ")
}
