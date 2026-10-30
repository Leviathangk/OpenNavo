package store

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
)

// HistoryPatch restores only fields actually changed by the revision, preventing icon/publish permissions from indirectly overwriting text or other fields.
func HistoryPatch(current, before, after, target HistoryState) HistoryState {
	if current == nil || after == nil || target == nil {
		return target
	}
	raw, _ := json.Marshal(current)
	out := HistoryState{}
	_ = json.Unmarshal(raw, &out)
	tables := map[string]bool{}
	for table := range before {
		tables[table] = true
	}
	for table := range after {
		tables[table] = true
	}
	for table := range tables {
		oldRows := before[table]
		newRows := after[table]
		desiredRows := target[table]
		oldByKey, newByKey, wantByKey := historyRows(oldRows), historyRows(newRows), historyRows(desiredRows)
		currentByKey := historyRows(out[table])
		lifecycleTable := table == "packages" && (HistoryStateIncludesPackageLifecycle(before) || HistoryStateIncludesPackageLifecycle(after))
		keys := map[string]bool{}
		for key := range oldByKey {
			keys[key] = true
		}
		for key := range newByKey {
			keys[key] = true
		}
		for key := range keys {
			previous, next := oldByKey[key], newByKey[key]
			if reflect.DeepEqual(previous, next) {
				continue
			}
			if table == "package_meta" && previous == nil && next != nil && currentByKey[key] != nil {
				previous = map[string]any{}
				for field := range next {
					previous[field] = nil
				}
				previous["package_id"] = next["package_id"]
				previous["hidden"], previous["editor_choice"] = false, false
				previous["private"], previous["visibility_reason"] = false, ""
				previous["tags"] = []any{}
			}
			desired := wantByKey[key]
			if desired == nil {
				delete(currentByKey, key)
				continue
			}
			if next == nil || currentByKey[key] == nil {
				currentByKey[key] = desired
				continue
			}
			for field, value := range desired {
				if table == "package_meta" && (field == "private" || field == "visibility_reason") {
					continue // Visibility and its required reason must move together.
				}
				// Creation grants content writes only; defaults must not roll back existing publication state or later audit attribution.
				if before == nil && (field == "created_at" || field == "created_by" || field == "updated_at" || field == "updated_by" || table == "collections" && (field == "status" || field == "publish_at" || field == "unpublish_at")) {
					continue
				}
				if previous == nil || !reflect.DeepEqual(previous[field], next[field]) {
					currentByKey[key][field] = value
				}
			}
			if table == "package_meta" {
				oldPrivate, oldReason := historyVisibility(previous)
				newPrivate, newReason := historyVisibility(next)
				if oldPrivate != newPrivate || oldReason != newReason {
					private, reason := historyVisibility(desired)
					currentByKey[key]["private"], currentByKey[key]["visibility_reason"] = private, reason
				}
			}
			if lifecycleTable {
				overrideChanged := false
				for _, field := range packageDisabledOverrideFields {
					if !reflect.DeepEqual(previous[field], next[field]) {
						overrideChanged = true
						break
					}
				}
				// A clear-disabled revision captures the complete package lifecycle,
				// but an undo must only restore lifecycle fields changed by that
				// revision. Upstream may have changed an untouched field since then.
				for _, field := range packageLifecycleFields {
					if slices.Contains(packageDisabledOverrideFields[:], field) {
						continue
					}
					if value, exists := desired[field]; exists && !reflect.DeepEqual(previous[field], next[field]) {
						currentByKey[key][field] = value
					}
				}
				if overrideChanged {
					for _, field := range packageDisabledOverrideFields {
						if value, exists := desired[field]; exists {
							currentByKey[key][field] = value
						}
					}
				}
			}
		}
		ordered := []string{}
		for key := range currentByKey {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		out[table] = []map[string]any{}
		for _, key := range ordered {
			out[table] = append(out[table], currentByKey[key])
		}
	}
	return out
}

func historyVisibility(row map[string]any) (bool, string) {
	private, _ := row["private"].(bool)
	reason, _ := row["visibility_reason"].(string)
	return private, reason
}

func historyRows(rows []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, row := range rows {
		key := map[string]any{}
		// Prefer standalone IDs; association tables use composite foreign keys and locale.
		if id, exists := row["id"]; exists {
			key["id"] = id
		} else {
			for _, field := range []string{"key", "package_id", "alternative_id", "category_id", "collection_id", "feature_id", "release_id", "screenshot_id", "desktop_release_id", "mirror_id", "config_key", "locale"} {
				if value, exists := row[field]; exists {
					key[field] = value
				}
			}
		}
		raw, _ := json.Marshal(key)
		out[string(raw)] = row
	}
	return out
}
