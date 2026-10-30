package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"gorm.io/gorm"
)

type Enqueuer interface {
	Enqueue(context.Context, string, jobs.Payload, jobs.Metadata) (string, error)
	Trigger(context.Context, string, int64) (string, error)
}
type Service struct {
	Evidence   *agentgate.EvidenceService // injectable fetcher for deterministic evidence tests
	Desktop    *desktop.Service
	Store      *store.Store
	Queue      Enqueuer
	Config     config.Config
	Now        func() time.Time
	IconAccent func(context.Context, int64) (*string, error)
	Taps       *taps.Service // reads third-party tap casks for AddTapPackage
	// Visibility changes clear the public cache after commit, before acknowledging the write.
	InvalidatePublic func(context.Context) error
	Snapshots        *snapshot.Service
	// InvalidateShareImages queues the origin and CDN share-image purge. It accepts
	// func(context.Context, string) error for the package token, or the same
	// function with []string app paths, int64 package ID, or no arguments.
	InvalidateShareImages any
}
type Input struct {
	ID, ScreenshotID                         int64
	Locale, Key, JobType, Version, RequestID string
	Params                                   map[string]any
	Body                                     map[string]any
	Actor                                    store.AuditActor
	CI                                       bool
	tapCandidate                             *taps.Candidate
	translationRef                           *content.Ref // pinned inside the agent write transaction
}

func Decode(request any, body []byte, actor store.AuditActor, ci bool) (Input, error) {
	out := Input{Actor: actor, CI: ci, Params: map[string]any{}, Body: map[string]any{}}
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return out, err
	}
	for key, v := range root {
		switch strings.ToLower(key) {
		case "id":
			err = json.Unmarshal(v, &out.ID)
		case "screenshotid":
			err = json.Unmarshal(v, &out.ScreenshotID)
		case "locale":
			err = json.Unmarshal(v, &out.Locale)
		case "key", "label", "token":
			err = json.Unmarshal(v, &out.Key)
		case "version":
			err = json.Unmarshal(v, &out.Version)
		case "requestid":
			err = json.Unmarshal(v, &out.RequestID)
		case "jobtype":
			err = json.Unmarshal(v, &out.JobType)
		case "params":
			var p map[string]any
			err = json.Unmarshal(v, &p)
			for k, v := range p {
				out.Params[strings.ToLower(k)] = v
			}
		case "body":
			if len(body) == 0 && string(v) != "null" {
				body = v
			}
		}
		if err != nil {
			return out, err
		}
	}
	if len(body) > 0 {
		err = json.Unmarshal(body, &out.Body)
		if err == nil && out.Body["downloadSize"] != nil {
			// Decode byte counts as int64 to avoid integer precision loss from generic map float64 values.
			var fields struct {
				DownloadSize int64 `json:"downloadSize"`
			}
			if json.Unmarshal(body, &fields) != nil || fields.DownloadSize < 0 {
				return out, domain.Rejected("download_size_invalid:downloadSize")
			}
			out.Body["downloadSize"] = fields.DownloadSize
		}
	}
	return out, err
}
func number(m map[string]any, key string, def int) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return def
}
func text(m map[string]any, key string) string  { v, _ := m[key].(string); return v }
func boolean(m map[string]any, key string) bool { v, _ := m[key].(bool); return v }

// fieldError names a body field that is missing or has the wrong type.
func fieldError(body map[string]any, key string) error {
	if value, ok := body[key]; !ok || value == nil {
		return domain.Rejected("field_required:" + key)
	}
	return domain.Rejected("field_type_invalid:" + key)
}
func ids(value any) ([]int64, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, fieldError(map[string]any{"ids": value}, "ids")
	}
	out := []int64{}
	seen := map[int64]bool{}
	for _, v := range list {
		n, ok := v.(float64)
		if !ok || n < 1 || n >= float64(math.MaxInt64) || n != math.Trunc(n) || seen[int64(n)] {
			return nil, domain.Rejected("ids_invalid")
		}
		seen[int64(n)] = true
		out = append(out, int64(n))
	}
	return out, nil
}
func fail(code string) error { return &domain.AppError{Code: code, HTTPStatus: 409} }
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func page(in Input) (int, int, error) {
	current, size := number(in.Params, "current", 1), number(in.Params, "size", 20)
	if size < 1 || size > 100 {
		return 0, 0, domain.Rejected("parameter_invalid:size")
	}
	if current < 1 || current > math.MaxInt/size {
		return 0, 0, domain.Rejected("parameter_invalid:current")
	}
	return current, size, nil
}
func mapped(body map[string]any, names map[string]string) map[string]any {
	out := map[string]any{}
	for key, col := range names {
		if value, ok := body[key]; ok {
			out[col] = value
		}
	}
	return out
}

func packageLifecycle(in Input) bool {
	value, ok := in.Body["clearDisabled"].(bool)
	return ok && value
}

func snapshotEntity(ctx context.Context, st *store.Store, entity string, in Input) (any, error) {
	id := in.ID
	if entity == "update_feed" {
		return st.UpdateFeed(ctx, domain.UpdateFeedKey{BundleID: text(in.Body, "bundleId"), Source: text(in.Body, "source")})
	}
	if entity == "link_app" && id > 0 {
		return st.LinkApp(ctx, id)
	}
	if entity == "release" {
		return releaseSnapshot(ctx, st, in)
	}
	if entity == "translation" {
		if value, ok := in.Body["ref"]; ok {
			ref, err := contentRef(value)
			if err != nil {
				return nil, err
			}
			return st.HistorySnapshot(ctx, ref)
		}
		return translationSnapshot(ctx, st, in)
	}
	if (entity == "collection" || entity == "feature") && id > 0 {
		return contentSnapshot(ctx, st, entity, id)
	}
	tables := map[string]string{"package": "packages", "category": "categories", "mirror": "mirrors", "synonym": "search_synonyms", "feedback": "feedback", "admin_user": "admin_users", "role": "admin_roles"}
	if entity == "package" && id > 0 {
		raw, err := st.AdminPackage(ctx, id)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		delete(out, "raw")
		if packageLifecycle(in) {
			var lifecycle struct {
				Disabled           bool
				StaleDisabled      bool
				DisableDate        *string
				DisableReason      *string
				DisableReplacement *string
			}
			if err := st.DB.WithContext(ctx).Raw(`SELECT (disabled OR stale_disabled) AS disabled,stale_disabled,disable_date::text,disable_reason,disable_replacement FROM packages WHERE id=?`, id).Scan(&lifecycle).Error; err != nil {
				return nil, err
			}
			out["disabled"] = lifecycle.Disabled
			out["disableDate"] = lifecycle.DisableDate
			if lifecycle.StaleDisabled && lifecycle.DisableReason == nil {
				out["disableReason"] = "stale_version"
			} else {
				out["disableReason"] = lifecycle.DisableReason
			}
			out["disableReplacement"] = lifecycle.DisableReplacement
		}
		return out, nil
	}
	queries := map[string]string{
		"role":       "SELECT " + roleJSON + " AS data FROM admin_roles r WHERE r.id=?",
		"admin_user": "SELECT " + adminUserJSON + " AS data FROM admin_users u LEFT JOIN admin_users creator ON creator.id=u.created_by LEFT JOIN admin_users editor ON editor.id=u.updated_by WHERE u.id=?",
		"category":   "SELECT to_jsonb(c)||jsonb_build_object('i18n',COALESCE((SELECT jsonb_object_agg(i.locale,to_jsonb(i)-'category_id') FROM category_i18n i WHERE i.category_id=c.id),'{}'::jsonb)) AS data FROM categories c WHERE c.id=?",
		"app_config": "SELECT to_jsonb(c) AS data FROM app_config c WHERE c.key=?",
	}
	if query, ok := queries[entity]; ok && (id > 0 || in.Key != "") {
		var arg any = id
		if entity == "app_config" {
			arg = in.Key
		}
		rows, err := st.JSONRows(ctx, query, arg)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			if entity == "app_config" {
				return nil, nil
			}
			return nil, fail(domain.CodeNotFound)
		}
		var out any
		err = json.Unmarshal(rows[0], &out)
		return out, err
	}
	table, ok := tables[entity]
	if !ok || id == 0 {
		return nil, nil
	}
	rows, err := st.JSONRows(ctx, "SELECT to_jsonb(e)-ARRAY['password_hash','raw','search_vector','search_text'] AS data FROM "+table+" e WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fail(domain.CodeNotFound)
	}
	var out any
	err = json.Unmarshal(rows[0], &out)
	return out, err
}
func (s *Service) write(ctx context.Context, in Input, op, entity string, fn func(*store.Store) (any, error)) (any, error) {
	ctx = operationContext(ctx, op, in)
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if historyEntities(entity) != "" || entity == "changelog_source" || entity == "link_app" {
			if err := store.LockCatalogWrites(ctx, tx); err != nil {
				return err
			}
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		if entity == "admin_user" || entity == "role" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended('admin:super',0))").Error; err != nil {
				return err
			}
		}
		refs, err := historyRefs(ctx, st, op, entity, in, nil)
		if err != nil {
			return err
		}
		states, err := captureHistory(ctx, st, refs, packageLifecycle(in))
		if err != nil {
			return err
		}
		auditEntity := entity
		if op == "UpsertReleaseNotes" {
			auditEntity = ""
		}
		before, err := snapshotEntity(ctx, st, auditEntity, in)
		if err != nil {
			return err
		}
		result, err = fn(st)
		if err != nil {
			return err
		}
		afterRefs, err := historyRefs(ctx, st, op, entity, in, result)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, r := range refs {
			seen[historyName(r)] = true
		}
		for _, r := range afterRefs {
			if !seen[historyName(r)] {
				refs = append(refs, r)
			}
		}
		preview, err := finishHistory(ctx, st, refs, states, packageLifecycle(in))
		if err != nil {
			return err
		}
		if m, ok := result.(map[string]any); ok {
			if _, present := m["revisionId"]; present {
				m["affectedUrls"] = preview["affectedUrls"]
				if ref, err := contentRef(m["ref"]); err == nil && !domain.CurrentOperation(ctx).DryRun {
					var revisionID int64
					if err := st.DB.WithContext(ctx).Raw("SELECT id FROM content_revisions WHERE request_id=? AND entity=? AND object_key=? ORDER BY id DESC LIMIT 1", domain.CurrentOperation(ctx).RequestID, ref.Entity, ref.Key()).Scan(&revisionID).Error; err != nil {
						return err
					}
					if revisionID > 0 {
						m["revisionId"] = revisionID
					}
				}
			}
		}
		if domain.CurrentOperation(ctx).DryRun {
			delete(preview, "revisionIds")
			if view, ok := result.(map[string]any); ok && slices.Contains(factWrites, op) {
				preview["sourceGaps"] = view["gaps"]
			}
			result = preview
			return errPreviewRollback
		}
		var after any = mapped(in.Body, map[string]string{"name": "name", "permissions": "permissions", "allowDelete": "allowDelete", "ipAllowlist": "ipAllowlist", "expiresAt": "expiresAt", "limits": "limits", "sourceLocale": "sourceLocale", "title": "title", "body": "body", "sections": "sections", "term": "term", "translations": "translations", "doNotTranslate": "doNotTranslate", "developer": "developer", "repoUrl": "repoUrl", "hidden": "hidden", "private": "private", "visibilityReason": "visibilityReason", "clearDisabled": "clearDisabled", "editorChoice": "editorChoice", "tags": "tags", "notes": "notes", "downloadSize": "downloadSize", "accentColor": "accentColor", "displayName": "displayName", "summary": "summary", "description": "description", "assetId": "assetId", "captionZh": "captionZh", "captionEn": "captionEn", "theme": "theme", "items": "items", "ids": "ids", "slug": "slug", "icon": "icon", "appliesTo": "appliesTo", "visible": "visible", "hiddenByDefault": "hiddenByDefault", "parentId": "parentId", "i18n": "i18n", "roles": "roles", "status": "status", "nickName": "nickName", "email": "email", "userName": "userName", "roleCode": "roleCode", "roleName": "roleName", "roleDesc": "roleDesc", "permissionCodes": "permissionCodes", "terms": "terms", "enabled": "enabled", "handlerNote": "handlerNote", "manual": "manual", "autoEnabled": "autoEnabled", "value": "value", "key": "key", "nameZh": "nameZh", "nameEn": "nameEn", "probeUrl": "probeUrl", "recommended": "recommended", "sort": "sort", "apiDomain": "apiDomain", "bottleDomain": "bottleDomain", "brewGitRemote": "brewGitRemote", "coreGitRemote": "coreGitRemote", "publishAt": "publishAt", "unpublishAt": "unpublishAt", "coverAssetId": "coverAssetId", "placement": "placement", "targetType": "targetType", "packageId": "packageId", "collectionId": "collectionId", "url": "url", "glowColor": "glowColor", "startsAt": "startsAt", "endsAt": "endsAt", "type": "type", "amount": "amount", "currency": "currency", "region": "region", "plan": "plan", "period": "period", "appVersion": "appVersion", "sourceUrl": "sourceUrl", "method": "method", "verifiedAt": "verifiedAt", "ranges": "ranges", "note": "note"})
		if entity == "package" && packageLifecycle(in) {
			afterMap := after.(map[string]any)
			afterMap["disabled"] = false
		}
		if op != "UpsertReleaseNotes" && (entity == "release" || entity == "translation" || entity == "changelog_source" || entity == "link_app" || entity == "update_feed") {
			auditInput := in
			if entity == "link_app" && auditInput.ID == 0 {
				if created, ok := result.(map[string]any); ok {
					auditInput.ID = valueID(created["id"])
				}
			}
			after, err = snapshotEntity(ctx, st, entity, auditInput)
			if err != nil {
				return err
			}
		}
		entityID := fmt.Sprint(in.ID)
		if created, ok := result.(map[string]any); ok {
			if id, ok := created["id"]; ok && in.ID == 0 {
				entityID = fmt.Sprint(id)
			}
		}
		if in.Key != "" {
			entityID = in.Key
		}
		if in.JobType != "" {
			entityID = in.JobType
		}
		return store.Audit(ctx, tx, in.Actor, op, entity, entityID, before, redact(after))
	})
	if errors.Is(err, errPreviewRollback) {
		return result, nil
	}
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	if s.Store.Redis != nil && !agentTransaction(ctx) {
		if err := cache.PublishInvalidation(ctx, s.Store.Redis, "c:*"); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// shareImagePackageIDs resolves the package IDs affected by a visibility change.
// UpdatePackageMeta carries the package ID directly, while restore paths carry a
// revision, trash, or request ID, so the affected package rows must be resolved
// before the purge can target the right token.
func (s *Service) shareImagePackageIDs(ctx context.Context, op string, in Input) []int64 {
	var query string
	var arg any
	switch op {
	case "UpdatePackageMeta":
		return []int64{in.ID}
	case "RestoreRevision":
		query, arg = "SELECT object_key::bigint FROM content_revisions WHERE id=? AND entity='package'", in.ID
	case "RestoreTrash":
		query, arg = "SELECT object_key::bigint FROM content_trash WHERE id=? AND entity='package'", in.ID
	case "RevertRequest":
		query, arg = "SELECT DISTINCT object_key::bigint FROM content_revisions WHERE request_id=? AND entity='package'", in.RequestID
	default:
		return []int64{in.ID}
	}
	var packageIDs []int64
	if err := s.Store.DB.WithContext(ctx).Raw(query, arg).Scan(&packageIDs).Error; err != nil {
		slog.Default().Error("share image package lookup failed", "operation", op, "error", err)
		return nil
	}
	return packageIDs
}

// invalidateShareImages resolves the app paths and hands the purge to the
// configured queue. Queue errors are logged but never returned: visibility already
// committed, and the queue retries delivery. A paused purge returns a warning.
func (s *Service) invalidateShareImages(ctx context.Context, packageID int64) (warning string) {
	if s.InvalidateShareImages == nil || packageID <= 0 {
		return ""
	}
	var row struct{ Token string }
	if err := s.Store.DB.WithContext(ctx).Raw("SELECT token FROM packages WHERE id=?", packageID).Scan(&row).Error; err != nil {
		slog.Default().Error("share image invalidation lookup failed", "package_id", packageID, "error", err)
		return ""
	}
	if row.Token == "" {
		return ""
	}
	paths := make([]string, 0, len(i18n.SiteLocales))
	for _, locale := range i18n.SiteLocales {
		paths = append(paths, i18n.Metadata[locale].Prefix+"/apps/"+row.Token)
	}
	var problem error
	switch hook := s.InvalidateShareImages.(type) {
	case func(context.Context, string) error:
		problem = hook(ctx, row.Token)
	case func(context.Context, []string) error:
		problem = hook(ctx, paths)
	case func(context.Context, int64) error:
		problem = hook(ctx, packageID)
	case func(context.Context) error:
		problem = hook(ctx)
	case func(context.Context, int64, string) error:
		problem = hook(ctx, packageID, row.Token)
	case func(context.Context, string, []string) error:
		problem = hook(ctx, row.Token, paths)
	case func(context.Context, string) (string, error):
		warning, problem = hook(ctx, row.Token)
	case func(context.Context, []string) (string, error):
		warning, problem = hook(ctx, paths)
	default:
		problem = fmt.Errorf("unsupported InvalidateShareImages hook %T", hook)
	}
	if problem != nil {
		slog.Default().Error("share image invalidation failed", "package_id", packageID, "token", row.Token, "error", problem)
		return ""
	}
	if warning != "" {
		slog.Default().Error("share image CDN purge paused", "package_id", packageID, "token", row.Token, "status", "paused: Cloudflare purge disabled", "warning", warning)
	}
	return warning
}

func (s *Service) Execute(ctx context.Context, op string, in Input) (result any, err error) {
	ctx = operationContext(ctx, op, in)
	defer func() {
		_, hidden := in.Body["hidden"]
		_, private := in.Body["private"]
		visibility := op == "UpdatePackageMeta" && (hidden || private)
		// A restore can also apply a recorded visibility change.
		visibility = visibility || op == "RestoreRevision" || op == "RestoreTrash" || op == "RevertRequest"
		if err == nil && visibility && !domain.CurrentOperation(ctx).DryRun && !agentTransaction(ctx) {
			if s.InvalidatePublic != nil {
				err = s.InvalidatePublic(ctx)
			}
			if s.Snapshots != nil {
				err = errors.Join(err, s.Snapshots.RevokePrivate(ctx))
			}
			// Queue, never await, the share-image purge: a CDN or web outage must not
			// turn a committed visibility change into a failed admin write.
			var warning string
			for _, packageID := range s.shareImagePackageIDs(ctx, op, in) {
				if current := s.invalidateShareImages(ctx, packageID); current != "" && warning == "" {
					warning = current
				}
			}
			if warning != "" && err == nil && op == "UpdatePackageMeta" {
				if view, ok := result.(map[string]any); ok {
					view["warning"] = warning
				} else {
					result = map[string]any{"warning": warning}
				}
			}
		}
	}()
	// CI may carry all ten release-note languages as a consistency assertion.
	// Discard the asserted derived text; WriteLocalized generates the stored row.
	if in.CI && op == "CreateDesktopRelease" {
		if err := verifyDerivedReleaseNotes(in.Body); err != nil {
			return nil, err
		}
	}
	if !strings.HasPrefix(op, "Get") && !strings.HasPrefix(op, "List") && !strings.HasPrefix(op, "Preview") {
		if in.Locale == "zh-Hant" || derivedWrite(in.Body) {
			return nil, domain.Rejected("derived_locale_read_only:zh-Hant:zh-CN")
		}
	}
	domain.CurrentOperation(ctx).WebBaseURL = s.Config.WebBaseURL
	// Batch translations have their own permission and transaction boundaries.
	switch op {
	case "GetTranslationsSource":
		return s.translationsSource(ctx, in)
	case "WriteTranslations":
		return s.writeTranslations(ctx, in)
	}
	if current := domain.CurrentOperation(ctx); current.AgentClientID > 0 && !strings.HasPrefix(op, "Get") && !strings.HasPrefix(op, "List") {
		return s.executeAgent(ctx, op, in)
	}
	// SEO and intent-page views attach their own revisions inside execute; they need
	// the same snapshot boundary as reads augmented by withReadRevision.
	if _, ok := readRevisionWrites[op]; ok || op == "GetPackageSeo" || op == "GetPackageIntentPages" || op == "GetTranslationStatus" {
		return s.readWithRevision(ctx, op, in)
	}
	return s.execute(ctx, op, in)
}

func (s *Service) execute(ctx context.Context, op string, in Input) (any, error) {
	switch op {
	case "UpsertUpdateFeed", "DisableUpdateFeed":
		return s.updateFeedOperation(ctx, op, in)
	case "ListLinkPackages", "AddLinkPackage", "UpdateLinkPackage", "RemoveLinkPackage":
		return s.linkOperation(ctx, op, in)
	case "ListAdminVersions":
		return s.adminVersions(ctx, in)
	case "ListAdminCatalogChanges":
		return s.catalogActivity(ctx, in)
	case "GetPageDecision":
		return s.pageDecision(ctx, in)
	case "GetSiteIndexSettings":
		return s.siteIndexSettings(ctx)
	case "GetPackageIntentPages", "SetPackageIntentPages", "ListIntentPages":
		return s.intentPages(ctx, op, in)
	case "GetPackageSeo", "SetPackageSeo":
		return s.packageSEO(ctx, op, in)
	case "ListDueFactRecords":
		return s.dueFactRecords(ctx, in)
	case "ListContentRevisions", "GetContentRevision", "ListTrash", "GetTrashItem":
		return s.historyRead(ctx, op, in)
	case "RestoreRevision", "RestoreTrash", "RevertRequest":
		return s.restoreHistory(ctx, op, in)
	case "ListAgentCalls", "GetAgentCall", "ListTranslationLogs", "GetTranslationLog":
		return s.agentLogs(ctx, op, in)
	case "UpdatePackageText", "GetAnnouncement", "UpdateAnnouncement", "UpdateDesktopReleaseNotes":
		return s.hermesText(ctx, op, in)
	case "UpsertReleaseNotes":
		return s.upsertNotes(ctx, in)
	case "ListGlossary", "UpsertGlossaryTerm", "DeleteGlossaryTerm":
		return s.glossary(ctx, op, in)
	case "ListTranslations":
		return s.translations(ctx, in)
	case "GetTranslationStatus", "FixTranslation":
		return s.translationManage(ctx, op, in)
	case "GetAgentSettings", "UpdateAgentSettings", "ListAgentClients", "CreateAgentClient", "GetAgentClient", "UpdateAgentClient", "ListAgentTokens", "CreateAgentToken", "UpdateAgentToken", "RevokeAgentToken":
		return s.agentOperation(ctx, op, in)
	case "GetGitHubSettings", "UpdateGitHubSettings":
		return s.githubSettings(ctx, op, in)
	case "GetSeoSettings", "UpdateSeoSettings":
		return s.seoSettings(ctx, op, in)
	case "GetPackageIndexStatus", "SetPackageIndexStatus":
		return s.packageIndexStatus(ctx, op, in)
	case "ListIndexReport":
		return s.indexReport(ctx, in)
	case "SetCollectionSetupCriteria":
		return s.saveCollectionCriteria(ctx, op, in)
	case "SetBackgroundItem":
		return s.saveBackgroundItem(ctx, op, in)
	case "GetPackageFactRecords", "SetPackageFactRecords":
		return s.factRecords(ctx, op, in)
	case "GetPackageFacts", "SetPackagePrice", "SetPackageMacosSupport", "SetPackageAlternatives":
		return s.packageFacts(ctx, op, in)
	case "CaptureArticleEvidence":
		return s.captureArticleEvidence(ctx, in)
	case "ListArticleTestArtifacts", "CreateArticleTestArtifact":
		return s.articleTestArtifacts(ctx, op, in)
	case "ListAdminArticles", "GetAdminArticle":
		return s.articleRead(ctx, op, in)
	case "CreateArticle":
		return s.createArticle(ctx, in)
	case "UpdateArticle", "SetArticlePackages":
		return s.updateArticle(ctx, op, in)
	case "SubmitArticleReview":
		return s.submitArticle(ctx, in)
	case "PublishArticle":
		return s.publishArticle(ctx, in)
	case "WithdrawArticle", "RestoreArticle":
		return s.withdrawArticle(ctx, op, in)
	case "RevertArticle":
		return s.revertArticle(ctx, in)
	case "SetArticleSlug":
		return s.setArticleSlug(ctx, in)
	case "SetArticleLocale":
		return s.setArticleLocale(ctx, in)
	case "ListArticleAuthors", "CreateArticleAuthor", "UpdateArticleAuthor":
		return s.articleAuthors(ctx, op, in)
	case "ListDesktopReleases":
		if s.Desktop == nil {
			return nil, domain.Internal(nil)
		}
		current, size, err := page(in)
		if err != nil {
			return nil, err
		}
		return s.Desktop.List(ctx, text(in.Params, "channel"), current, size)
	case "GetDesktopRelease":
		return s.Store.AdminDesktop(ctx, in.ID)
	case "CreateDesktopRelease", "UpdateDesktopRelease", "PublishDesktopRelease", "RollbackDesktopRelease":
		if s.Desktop == nil {
			return nil, domain.Internal(nil)
		}
		return s.Desktop.Mutate(ctx, op, in.ID, in.Body, in.Actor)
	case "ListAdminReleases", "GetAdminRelease", "ListTranslationQueue":
		return s.releaseRead(ctx, op, in)
	case "UpdateRelease", "UpdateReleaseI18n":
		return s.releaseWrite(ctx, op, in)
	case "ApproveTranslations":
		return s.approve(ctx, in)
	case "ListAdminCollections", "GetAdminCollection", "ListFeatures", "GetFeature":
		return s.contentRead(ctx, op, in)
	case "CreateCollection", "UpdateCollection", "DeleteCollection", "SetCollectionItems", "PublishCollection", "UnpublishCollection", "CreateFeature", "UpdateFeature", "DeleteFeature":
		return s.contentWrite(ctx, op, in)
	case "ListAdminPackages":
		return s.packages(ctx, in)
	case "GetAdminPackage":
		raw, err := s.Store.AdminPackage(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return s.withWebURL(raw, "package")
	case "ListPackageScreenshots":
		if _, err := s.Store.AdminPackage(ctx, in.ID); err != nil {
			return nil, err
		}
		return s.Store.AdminScreenshots(ctx, in.ID)
	case "ListPackageVersions":
		return s.versions(ctx, in)
	case "UpdatePackageMeta", "UpdatePackageI18n", "SetPackageCategories", "SetPackageIcon", "DeletePackageIcon", "AddPackageScreenshot", "ReorderPackageScreenshots", "UpdatePackageScreenshot", "DeletePackageScreenshot":
		return s.packageWrite(ctx, op, in)
	case "GetCategoryTree":
		return s.categories(ctx, in)
	case "CreateCategory", "UpdateCategory", "DeleteCategory", "ReorderCategories":
		return s.categoryWrite(ctx, op, in)
	case "GetDashboardOverview":
		return s.dashboard(ctx)
	case "GetDashboardUsageIntraday":
		return s.Store.UsageIntraday(ctx, s.now())
	case "ListAssets", "ListFeedback", "GetFeedback", "ListSynonyms", "ListTopQueries", "ListZeroResultQueries", "ListJobRuns", "GetJobRun", "ListMirrors", "ListAppConfig", "ListAdminUsers", "ListRoles", "ListPermissions", "ListAuditLogs":
		return s.read(ctx, op, in)
	case "CreateMirror", "UpdateMirror", "DeleteMirror", "UpdateAppConfig", "CreateSynonym", "UpdateSynonym", "DeleteSynonym", "UpdateFeedback", "CreateAdminUser", "UpdateAdminUser", "DeleteAdminUser", "ResetAdminUserPassword", "ForceLogoutAdminUser", "CreateRole", "UpdateRole", "DeleteRole", "SetRolePermissions":
		return s.systemWrite(ctx, op, in)
	case "ListQueues":
		return s.queues(ctx)
	case "ListTapPackages":
		return s.listTapPackages(ctx)
	case "AddTapPackage":
		return s.addTapPackage(ctx, op, in)
	case "RemoveTapPackage":
		return s.removeTapPackage(ctx, op, in)
	case "ResyncPackage", "TriggerJob":
		return s.trigger(ctx, op, in)
	case "GetPackageChangelogSettings":
		return s.Store.PackageChangelogSettings(ctx, in.ID)
	case "UpdatePackageChangelogSettings":
		excluded, ok := in.Body["excluded"].(bool)
		if !ok {
			return nil, fieldError(in.Body, "excluded")
		}
		return s.Store.SetPackageChangelogSettings(ctx, in.ID, excluded, in.Actor)
	case "ListChangelogSources":
		return s.sources(ctx, in)
	default:
		return nil, domain.Internal(errors.New("unregistered management operation"))
	}
}

func redact(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range v {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
				continue
			}
			out[k] = redact(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, redact(item))
		}
		return out
	default:
		return value
	}
}

// Reject explicit derived text at the shared API/MCP boundary, including nested
// localized maps and bulk operations. Read filters and SEO locale lists remain valid.
func derivedWrite(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			if k == "zh-Hant" {
				return true
			}
			if (k == "locale" || k == "sourceLocale") && func() bool { text, ok := x.(string); return ok && text == "zh-Hant" }() {
				return true
			}
			if derivedWrite(x) {
				return true
			}
		}
	case []any:
		for _, x := range v {
			if derivedWrite(x) {
				return true
			}
		}
	}
	return false
}

func verifyDerivedReleaseNotes(body map[string]any) error {
	values, ok := body["i18n"].(map[string]any)
	if !ok {
		return nil
	}
	supplied, present := values["zh-Hant"]
	if !present {
		return nil
	}
	cn, ok := values["zh-CN"].(map[string]any)
	if !ok {
		return domain.Rejected("simplified_notes_required")
	}
	text, ok := cn["notes"].(string)
	if !ok {
		return domain.Rejected("simplified_notes_required")
	}
	expected, err := traditional.Text(text, nil)
	if err != nil {
		return err
	}
	target, ok := supplied.(map[string]any)
	if !ok || len(target) != 1 || target["notes"] != expected {
		return domain.Rejected("derived_release_notes_mismatch")
	}
	delete(values, "zh-Hant")
	return nil
}
