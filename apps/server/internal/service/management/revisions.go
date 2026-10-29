package management

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

const revisionJSON = `jsonb_build_object('id',r.id,'object',jsonb_build_object('entity',r.entity,'objectKey',r.object_key),'version',r.version,'locale',r.locale,'action',r.action,'actor',jsonb_build_object('type',r.actor_type,'id',r.actor_id,'name',r.actor_name),'requestId',r.request_id,'before',r.before_data,'after',r.after_data,'createdAt',r.created_at,'canRestore',true,'affectedUrls',r.affected_urls,'operationId',r.operation_id,'requiredPermissions',r.required_permissions)`
const trashJSON = `jsonb_build_object('id',r.id,'object',jsonb_build_object('entity',r.entity,'objectKey',r.object_key),'label',r.label,'snapshot',r.snapshot,'actor',jsonb_build_object('type',r.actor_type,'id',r.actor_id,'name',r.actor_name),'requestId',r.request_id,'deletedAt',r.deleted_at,'expiresAt',r.expires_at,'restoredAt',r.restored_at,'canRestore',r.restored_at IS NULL AND r.expires_at>now(),'affectedUrls',r.affected_urls)`

func historyFilter(ctx context.Context, in Input) (string, []any) {
	where, args := "TRUE", []any{}
	for param, col := range map[string]string{"entity": "r.entity", "objectkey": "r.object_key", "requestid": "r.request_id", "locale": "r.locale"} {
		if v := text(in.Params, param); v != "" {
			where += " AND " + col + "=?"
			args = append(args, v)
		}
	}
	if actorID := number(in.Params, "actorid", 0); actorID > 0 {
		where += " AND r.actor_id=?"
		args = append(args, actorID)
	}
	if op := domain.CurrentOperation(ctx); op != nil && op.AgentClientID > 0 {
		where += " AND r.actor_id=? AND r.entity<>'mirror'"
		args = append(args, op.ActorID)
		allowed := allowedHistoryEntities(op)
		where += " AND r.entity IN ?"
		args = append(args, allowed)

	}
	return where, args
}
func (s *Service) historyRead(ctx context.Context, op string, in Input) (any, error) {
	expr, table, dateColumn := revisionJSON, "content_revisions", "r.created_at"
	if strings.Contains(op, "Trash") {
		expr, table, dateColumn = trashJSON, "content_trash", "r.deleted_at"
	}
	if scope := domain.CurrentOperation(ctx); scope != nil {
		if !scope.Allowed("content:revision:restore") {
			if table == "content_revisions" {
				expr = strings.Replace(expr, "'canRestore',true", "'canRestore',false", 1)
			} else {
				expr = strings.Replace(expr, "'canRestore',r.restored_at IS NULL AND r.expires_at>now()", "'canRestore',false", 1)
			}
		} else if table == "content_revisions" && !scope.Allowed("*") {
			raw, _ := json.Marshal(scope.Permissions)
			expr = strings.Replace(expr, "'canRestore',true", "'canRestore',r.required_permissions <@ ARRAY(SELECT jsonb_array_elements_text("+sqlString(string(raw))+"::jsonb))", 1)
		}
	}
	where, args := historyFilter(ctx, in)
	for _, bound := range []struct{ param, comparison string }{{"from", ">="}, {"to", "<"}} {
		if v := text(in.Params, bound.param); v != "" {
			where += " AND " + dateColumn + bound.comparison + "?::timestamptz"
			args = append(args, v)
		}
	}
	if scope := domain.CurrentOperation(ctx); scope != nil && scope.AgentClientID > 0 && table == "content_revisions" {
		raw, _ := json.Marshal(scope.Permissions)
		where += " AND r.required_permissions <@ ARRAY(SELECT jsonb_array_elements_text(?::jsonb))"
		args = append(args, string(raw))
	}

	if table == "content_trash" {
		if v, ok := in.Params["restored"].(bool); ok {
			where += " AND (r.restored_at IS NOT NULL)=?"
			args = append(args, v)
		}
		where += " AND r.expires_at>now()"
	}
	if strings.HasPrefix(op, "Get") {
		where += " AND r.id=?"
		args = append(args, in.ID)
		return s.Store.JSONRow(ctx, "SELECT "+expr+" data FROM "+table+" r WHERE "+where, args...)
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	return s.Store.AdminPage(ctx, expr, "FROM "+table+" r", where, "r.id DESC", args, current, size)
}

type revisionRow struct {
	ID, Version                                          int64
	Entity, ObjectKey, RequestID, OperationID, AfterHash string
	RequiredPermissions                                  json.RawMessage
	ActorID                                              *int64
	BeforeData, AfterData                                json.RawMessage
}

func revisionState(raw json.RawMessage) (store.HistoryState, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var state store.HistoryState
	err := json.Unmarshal(raw, &state)
	return state, err
}
func restorePermission(ctx context.Context, entity string, permissions []string, actorID *int64) error {
	op := domain.CurrentOperation(ctx)
	if op == nil {
		return nil
	}
	if !op.Allowed("content:revision:restore") {
		return forbidden("permission_required:content:revision:restore")
	}
	// Revisions from generic endpoints such as translation record endpoint permissions only; restoration must also check the Agent's current object permissions.
	if err := assertContentAccess(ctx, content.Ref{Entity: entity}); err != nil {
		return err
	}
	if op.AgentClientID > 0 && entity == "mirror" {
		return forbidden("operation_not_exposed")
	}
	if op.AgentClientID > 0 && (actorID == nil || op.ActorID == nil || *actorID != *op.ActorID) {
		return forbidden("revision_actor_mismatch")
	}
	for _, p := range permissions {
		if !op.Allowed(p) {
			return forbidden("permission_required:" + p)
		}
	}
	return nil
}
func (s *Service) restoreHistory(ctx context.Context, opName string, in Input) (any, error) {
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		op := domain.CurrentOperation(ctx)
		var rows []revisionRow
		var trashID int64
		if opName == "RestoreTrash" {
			var trash struct {
				ID                           int64
				Entity, ObjectKey, RequestID string
				Snapshot                     json.RawMessage
				ActorID                      *int64
				ExpiresAt                    time.Time
				RestoredAt                   *time.Time
			}
			if err := tx.Raw("SELECT * FROM content_trash WHERE id=? FOR UPDATE", in.ID).Scan(&trash).Error; err != nil {
				return err
			}
			if trash.ID == 0 {
				return fail(domain.CodeNotFound)
			}
			if trash.RestoredAt != nil || !trash.ExpiresAt.After(s.now()) {
				return fail(domain.CodeInvalidState)
			}
			ref, err := store.HistoryRef(trash.Entity, trash.ObjectKey)
			if err != nil {
				return err
			}
			state, err := st.HistorySnapshot(ctx, ref)
			if err != nil {
				return err
			}
			if state != nil {
				return fail(domain.CodeConflict)
			}
			permissions := []string{historyPermission("Delete" + strings.ToUpper(trash.Entity[:1]) + trash.Entity[1:])}
			if trash.Entity == "screenshot" {
				permissions = []string{"catalog:asset:upload"}
			}
			if err := restorePermission(ctx, trash.Entity, permissions, trash.ActorID); err != nil {
				return err
			}
			raw, _ := json.Marshal(permissions)
			rows = []revisionRow{{Entity: trash.Entity, ObjectKey: trash.ObjectKey, AfterData: trash.Snapshot, RequiredPermissions: raw, ActorID: trash.ActorID}}
			trashID = trash.ID
		} else {
			query := "SELECT r.id,r.version,r.entity,r.object_key,r.request_id,r.operation_id,r.after_hash,r.actor_id,r.before_data,r.after_data,to_jsonb(r.required_permissions) required_permissions FROM content_revisions r WHERE "
			args := []any{in.ID}
			if opName == "RevertRequest" {
				query += "r.request_id=? ORDER BY r.id DESC"
				args = []any{in.RequestID}
				var manifest struct {
					RevisionCount int
					Reversible    bool
				}
				if err := tx.Raw("SELECT revision_count,reversible FROM content_revision_requests WHERE request_id=? FOR UPDATE", in.RequestID).Scan(&manifest).Error; err != nil {
					return err
				}
				if manifest.RevisionCount == 0 || !manifest.Reversible {
					return fail(domain.CodeInvalidState)
				}
				var reverted int64
				if err := tx.Table("content_request_reverts").Where("request_id=?", in.RequestID).Count(&reverted).Error; err != nil {
					return err
				}
				if reverted > 0 {
					return fail(domain.CodeInvalidState)
				}
				if err := tx.Raw(query, args...).Scan(&rows).Error; err != nil {
					return err
				}
				if len(rows) != manifest.RevisionCount {
					return fail(domain.CodeInvalidState)
				}
			} else {
				query += "r.id=?"
				if err := tx.Raw(query, args...).Scan(&rows).Error; err != nil {
					return err
				}
				if len(rows) == 0 {
					return fail(domain.CodeNotFound)
				}
			}
		}
		if opName == "RevertRequest" {
			merged := []revisionRow{}
			indices := map[string]int{}
			for _, row := range rows {
				key := row.Entity + ":" + row.ObjectKey
				if i, exists := indices[key]; exists {
					var a, b []string
					if json.Unmarshal(merged[i].RequiredPermissions, &a) != nil || json.Unmarshal(row.RequiredPermissions, &b) != nil {
						return domain.Rejected("revision_permissions_invalid")
					}
					for _, p := range b {
						if !slices.Contains(a, p) {
							a = append(a, p)
						}
					}
					merged[i].RequiredPermissions, _ = json.Marshal(a)
					merged[i].BeforeData = row.BeforeData
				} else {
					indices[key] = len(merged)
					merged = append(merged, row)
				}
			}
			rows = merged
		}
		restored := []any{}
		affectedURLs := []string{}
		revisionIDs := []int64{}
		changes := []any{}
		translateLocales := []string{}
		for _, r := range rows {
			var permissions []string
			if json.Unmarshal(r.RequiredPermissions, &permissions) != nil {
				return domain.Rejected("revision_permissions_invalid")
			}
			if err := restorePermission(ctx, r.Entity, permissions, r.ActorID); err != nil {
				return err
			}
			ref, err := store.HistoryRef(r.Entity, r.ObjectKey)
			if err != nil {
				return err
			}
			var originalBefore, originalAfter store.HistoryState
			includePackageLifecycle := false
			if opName != "RestoreTrash" {
				originalBefore, err = revisionState(r.BeforeData)
				if err != nil {
					return err
				}
				originalAfter, err = revisionState(r.AfterData)
				if err != nil {
					return err
				}
				includePackageLifecycle = ref.Entity == "package" && (store.HistoryStateIncludesPackageLifecycle(originalBefore) || store.HistoryStateIncludesPackageLifecycle(originalAfter))
			}
			var current store.HistoryState
			if includePackageLifecycle {
				current, err = st.HistorySnapshotWithPackageLifecycle(ctx, ref)
			} else {
				current, err = st.HistorySnapshot(ctx, ref)
			}
			if err != nil {
				return err
			}
			if expected, ok := in.Body["expectedVersion"].(float64); ok {
				var latest int64
				if err := tx.Raw("SELECT COALESCE(max(version),0) FROM content_revisions WHERE entity=? AND object_key=?", r.Entity, r.ObjectKey).Scan(&latest).Error; err != nil {
					return err
				}
				if latest != int64(expected) {
					return fail(domain.CodeConflict)
				}
			}
			target, err := revisionState(r.AfterData)
			if err != nil {
				return err
			}
			if opName == "RevertRequest" {
				if !store.HistoryHashMatches(current, r.AfterHash) {
					return fail(domain.CodeConflict)
				}
				target, err = revisionState(r.BeforeData)
				if err != nil {
					return err
				}
			}
			if opName != "RestoreTrash" {
				target = store.HistoryPatch(current, originalBefore, originalAfter, target)
			}
			if restoresDeletedContent(r.Entity, current, target) && op != nil && !op.AllowDelete {
				return forbidden("delete_not_allowed")
			}
			if r.Entity == "category" && target == nil {
				var children int64
				if err := tx.Raw("SELECT (SELECT count(*) FROM categories WHERE parent_id=?)+(SELECT count(*) FROM package_categories WHERE category_id=?)", ref.ID, ref.ID).Scan(&children).Error; err != nil {
					return err
				}
				if children > 0 {
					return fail(domain.CodeInvalidState)
				}
			}
			if err := st.RestoreHistoryState(ctx, ref, target); err != nil {
				return err
			}
			if target != nil && ref.Valid() {
				refs := []content.Ref{ref}
				if ref.Entity == "collection" {
					for _, item := range target["collection_items"] {
						refs = append(refs, content.Ref{Entity: "collection_item", ID: ref.ID, SecondaryID: valueID(item["package_id"])})
					}
				}
				for _, translatedRef := range refs {
					if err := st.ForgetContentTranslation(ctx, translatedRef); err != nil {
						return err
					}
					if err := st.ScheduleContentTranslation(ctx, translatedRef); err != nil {
						return err
					}
					src, err := st.ReadContentSource(ctx, translatedRef)
					if err == nil {
						for locale := range src.Targets {
							if !slices.Contains(translateLocales, locale) {
								translateLocales = append(translateLocales, locale)
							}
						}
					}
				}
				if err := st.ContentChanged(ctx, ref); err != nil {
					return err
				}
			} else if target == nil && (ref.Entity == "release" || ref.Entity == "screenshot") {
				for _, row := range current[content.Registry[ref.Entity].Parent] {
					if err := st.AppendContentChange(ctx, valueID(row["package_id"])); err != nil {
						return err
					}
				}
			}

			var after store.HistoryState
			if includePackageLifecycle {
				after, err = st.HistorySnapshotWithPackageLifecycle(ctx, ref)
			} else {
				after, err = st.HistorySnapshot(ctx, ref)
			}
			if err != nil {
				return err
			}
			urls, err := st.HistoryURLs(ctx, ref, op.WebBaseURL, current, after)
			if err != nil {
				return err
			}
			for _, link := range urls {
				if !slices.Contains(affectedURLs, link) {
					affectedURLs = append(affectedURLs, link)
				}
			}
			object := map[string]any{"entity": ref.Entity, "objectKey": ref.Key()}
			restored = append(restored, object)
			changes = append(changes, map[string]any{"object": object, "action": "restore", "before": current, "after": after})
			if !op.DryRun {
				copy := *op
				copy.RequiredPermissions = permissions
				id, err := st.RecordRevision(ctx, ref, current, after, "restore", &copy)
				if err != nil {
					return err
				}
				if id > 0 {
					revisionIDs = append(revisionIDs, id)
				}
				if err := store.Audit(ctx, tx, in.Actor, opName, ref.Entity, ref.Key(), current, after); err != nil {
					return err
				}
			}
		}
		if op.DryRun {
			result = map[string]any{"dryRun": true, "requestId": op.RequestID, "changes": changes, "affectedUrls": affectedURLs}
			return errPreviewRollback
		}
		if trashID > 0 {
			if err := tx.Exec("UPDATE content_trash SET restored_at=now(),restored_by_request_id=? WHERE id=?", op.RequestID, trashID).Error; err != nil {
				return err
			}
		}
		if opName == "RevertRequest" {
			if err := tx.Exec("INSERT INTO content_request_reverts(request_id,reverted_by_request_id,actor_id) VALUES(?,?,?)", in.RequestID, op.RequestID, in.Actor.ID).Error; err != nil {
				return err
			}
		}
		status := "not_applicable"
		if len(translateLocales) > 0 {
			status = "pending"
		}
		result = map[string]any{"requestId": op.RequestID, "restored": restored, "revisionIds": revisionIDs, "translation": map[string]any{"status": status, "locales": translateLocales}, "affectedUrls": affectedURLs}
		return nil
	})
	if errors.Is(err, errPreviewRollback) {
		return result, nil
	}
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	return result, s.invalidate(ctx)
}

// Restoring deleted field values is also subject to the deletion switch; revisions cannot bypass direct-delete restrictions.
func restoresDeletedContent(entity string, current, target store.HistoryState) bool {
	if target == nil {
		return true
	}
	if entity == "package" {
		var oldIcon, newIcon any
		for _, row := range current["package_meta"] {
			oldIcon = row["icon_asset_id"]
		}
		for _, row := range target["package_meta"] {
			newIcon = row["icon_asset_id"]
		}
		return oldIcon != nil && newIcon == nil
	}
	if entity == "release" {
		for _, row := range target["releases"] {
			if row["source"] == "editorial" && row["hidden"] == true {
				return true
			}
		}
	}
	return false
}
