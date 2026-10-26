package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

var errPreviewRollback = errors.New("rollback validated preview")

func operationContext(ctx context.Context, op string, in Input) context.Context {
	operation := domain.CurrentOperation(ctx)
	if operation == nil {
		operation = &domain.Operation{RequestID: uuid.NewString(), Name: strings.ToLower(op[:1]) + op[1:], ActorID: in.Actor.ID, Permissions: []string{"*"}, RequiredPermissions: []string{historyPermission(op)}, AllowDelete: true, DryRun: boolean(in.Params, "dryrun")}
	}
	copy := *operation
	copy.Locale = in.Locale
	if copy.Locale == "" {
		copy.Locale = text(in.Body, "locale")
	}
	if copy.Locale == "" {
		copy.Locale = text(in.Body, "sourceLocale")
	}
	return domain.WithOperation(ctx, &copy)
}
func historyPermission(op string) string {
	switch {
	case strings.Contains(op, "Article"):
		// Before the package rule: SetArticlePackages edits an article, not packages.
		if strings.Contains(op, "Author") || op == "PublishArticle" || op == "WithdrawArticle" || op == "RestoreArticle" || op == "SetArticleSlug" || op == "SetArticleLocale" {
			return "content:article:publish"
		}
		return "content:article:edit"
	case strings.Contains(op, "Screenshot") || strings.Contains(op, "Icon") || op == "UploadAsset":
		return "catalog:asset:upload"
	case strings.Contains(op, "BackgroundItem"):
		return "catalog:package:edit"
	case strings.Contains(op, "Package"):
		return "catalog:package:edit"
	case strings.Contains(op, "Category") || op == "ReorderCategories":
		return "catalog:category:edit"
	case strings.Contains(op, "Collection"):
		if op == "PublishCollection" || op == "UnpublishCollection" {
			return "content:collection:publish"
		}
		return "content:collection:edit"
	case strings.Contains(op, "Feature"):
		return "content:feature:edit"
	case strings.Contains(op, "Desktop"):
		return "release:desktop:notes"
	case strings.Contains(op, "Announcement"):
		return "content:announcement:edit"
	case strings.Contains(op, "Glossary"):
		return "content:glossary:edit"
	case strings.Contains(op, "Translation") || strings.HasPrefix(op, "Retranslate"):
		return "translation:review"
	case strings.Contains(op, "Release") || op == "UpsertReleaseNotes":
		return "changelog:release:edit"
	case strings.Contains(op, "Synonym"):
		return "ops:search:edit"
	case strings.Contains(op, "Feedback"):
		return "ops:feedback:handle"
	case strings.Contains(op, "Mirror"):
		return "system:config:edit"
	}
	return "system:audit:view"
}
func valueID(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
func historyRefs(ctx context.Context, st *store.Store, op, entity string, in Input, result any) ([]content.Ref, error) {
	if len(historyEntities(entity)) == 0 {
		return nil, nil
	}
	if op == "UpsertReleaseNotes" {
		// Only the written scope: a build's notes never record the shared base note, nor the reverse.
		target, found, err := st.ResolveReleaseNotesTarget(ctx, in.ID, in.Version)
		if err != nil || !found {
			return nil, err
		}
		scope, scopeArgs := target.Scope()
		var ids []int64
		if err := st.DB.WithContext(ctx).Raw("SELECT id FROM releases WHERE package_id=? AND version=? AND source='editorial' AND "+scope+" ORDER BY hidden,id DESC", append([]any{in.ID, target.Base}, scopeArgs...)...).Scan(&ids).Error; err != nil {
			return nil, err
		}
		refs := []content.Ref{}
		for _, id := range ids {
			refs = append(refs, content.Ref{Entity: "release", ID: id})
		}
		return refs, nil
	}
	if op == "UpsertGlossaryTerm" {
		var id int64
		if err := st.DB.WithContext(ctx).Table("i18n_glossary").Select("id").Where("term=?", text(in.Body, "term")).Scan(&id).Error; err != nil {
			return nil, err
		}
		if id == 0 {
			return nil, nil
		}
		return []content.Ref{{Entity: "glossary", ID: id}}, nil
	}
	id := in.ID
	if m, ok := result.(map[string]any); ok && id == 0 {
		id = valueID(m["id"])
	}
	if entity == "translation" {
		if r, err := contentRef(in.Body["ref"]); err == nil {
			return []content.Ref{r}, nil
		}
		return nil, nil
	}
	if strings.Contains(op, "Screenshot") {
		switch op {
		case "AddPackageScreenshot":
			if m, ok := result.(map[string]any); ok {
				id = valueID(m["id"])
			} else {
				return nil, nil
			}
		case "ReorderPackageScreenshots":
			var ids []int64
			if err := st.DB.WithContext(ctx).Table("package_screenshots").Where("package_id=?", in.ID).Pluck("id", &ids).Error; err != nil {
				return nil, err
			}
			out := []content.Ref{}
			for _, id := range ids {
				out = append(out, content.Ref{Entity: "screenshot", ID: id})
			}
			return out, nil
		default:
			id = in.ScreenshotID
		}
		entity = "screenshot"
	}
	if op == "ReorderCategories" {
		items, ok := in.Body["items"].([]any)
		if !ok {
			return nil, fieldError(in.Body, "items")
		}
		out := []content.Ref{}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok || valueID(item["id"]) < 1 {
				return nil, domain.Rejected("category_items_invalid")
			}
			out = append(out, content.Ref{Entity: "category", ID: valueID(item["id"])})
		}
		return out, nil
	}
	if entity == "announcement" {
		return []content.Ref{{Entity: entity}}, nil
	}
	if id < 1 {
		return nil, nil
	}
	return []content.Ref{{Entity: entity, ID: id}}, nil
}
func historyEntities(entity string) string {
	if _, ok := content.Registry[entity]; ok {
		return entity
	}
	if slices.Contains([]string{"synonym", "glossary", "feedback", "asset", "translation"}, entity) {
		return entity
	}
	return ""
}
func captureHistory(ctx context.Context, st *store.Store, refs []content.Ref) (map[string]store.HistoryState, error) {
	out := map[string]store.HistoryState{}
	for _, r := range refs {
		state, err := st.HistorySnapshot(ctx, r)
		if err != nil {
			return nil, err
		}
		out[r.Entity+":"+r.Key()] = state
	}
	return out, nil
}
func finishHistory(ctx context.Context, st *store.Store, refs []content.Ref, before map[string]store.HistoryState) (map[string]any, error) {
	op := domain.CurrentOperation(ctx)
	changes := []any{}
	revisionIDs := []int64{}
	affectedURLs := []string{}
	for _, r := range refs {
		previous := before[r.Entity+":"+r.Key()]
		after, err := st.HistorySnapshot(ctx, r)
		if err != nil {
			return nil, err
		}
		if store.HistoryHash(previous) == store.HistoryHash(after) {
			continue
		}
		action := "update"
		if previous == nil {
			action = "create"
		}
		if after == nil {
			action = "delete"
		}
		urls, err := st.HistoryURLs(ctx, r, op.WebBaseURL, previous, after)
		if err != nil {
			return nil, err
		}
		for _, link := range urls {
			if !slices.Contains(affectedURLs, link) {
				affectedURLs = append(affectedURLs, link)
			}
		}
		changes = append(changes, map[string]any{"object": map[string]any{"entity": r.Entity, "objectKey": r.Key()}, "action": action, "before": previous, "after": after})
		if !op.DryRun {
			id, err := st.RecordRevision(ctx, r, previous, after, "", op)
			if err != nil {
				return nil, err
			}
			revisionIDs = append(revisionIDs, id)
		}
	}
	return map[string]any{"dryRun": true, "requestId": op.RequestID, "changes": changes, "affectedUrls": affectedURLs, "revisionIds": revisionIDs}, nil
}
func contentRef(value any) (content.Ref, error) {
	var ref content.Ref
	b, err := json.Marshal(value)
	if err != nil || json.Unmarshal(b, &ref) != nil || !ref.Valid() {
		return ref, domain.Rejected("content_ref_invalid")
	}
	return ref, nil
}
func assertContentAccess(ctx context.Context, ref content.Ref) error {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.AgentClientID == 0 {
		return nil
	}
	if ref.Entity == "mirror" {
		return forbidden("operation_not_exposed")
	}
	permission := entityPermission(ref.Entity)
	if !op.Allowed(permission) {
		return forbidden("permission_required:" + permission)
	}
	return nil
}
func mutationResult(ctx context.Context, st *store.Store, ref content.Ref) (any, error) {
	source, err := st.ReadContentSource(ctx, ref)
	status := "unchanged"
	locales := []string{}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	for locale := range source.Targets {
		locales = append(locales, locale)
	}
	slices.Sort(locales)
	if len(locales) > 0 {
		status = "pending"
	}
	if err != nil {
		status = "not_applicable"
	}
	requestID := ""
	if op := domain.CurrentOperation(ctx); op != nil {
		requestID = op.RequestID
	}
	return map[string]any{"ref": ref, "revisionId": nil, "requestId": requestID, "translation": map[string]any{"status": status, "locales": locales}, "affectedUrls": []string{}}, nil
}
func historyName(ref content.Ref) string { return fmt.Sprintf("%s:%s", ref.Entity, ref.Key()) }

func entityPermission(entity string) string {
	return map[string]string{"package": "catalog:package:edit", "release": "changelog:release:edit", "category": "catalog:category:edit", "collection": "content:collection:edit", "collection_item": "content:collection:edit", "feature": "content:feature:edit", "screenshot": "catalog:asset:upload", "asset": "catalog:asset:upload", "desktop_release": "release:desktop:notes", "announcement": "content:announcement:edit", "glossary": "content:glossary:edit", "synonym": "ops:search:edit", "feedback": "ops:feedback:handle", "article": "content:article:edit", "article_author": "content:article:publish"}[entity]
}
