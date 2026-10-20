package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/store"
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
}
type Input struct {
	ID, ScreenshotID                         int64
	Locale, Key, JobType, Version, RequestID string
	Params                                   map[string]any
	Body                                     map[string]any
	Actor                                    store.AuditActor
	CI                                       bool
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
				return out, domain.Validation()
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
func ids(value any) ([]int64, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, domain.Validation()
	}
	out := []int64{}
	seen := map[int64]bool{}
	for _, v := range list {
		n, ok := v.(float64)
		if !ok || n < 1 || n >= float64(math.MaxInt64) || n != math.Trunc(n) || seen[int64(n)] {
			return nil, domain.Validation()
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
	if current < 1 || size < 1 || size > 100 || current > math.MaxInt/size {
		return 0, 0, domain.Validation()
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
func snapshotEntity(ctx context.Context, st *store.Store, entity string, in Input) (any, error) {
	id := in.ID
	if entity == "listing" {
		return st.ListingCandidate(ctx, in.Key)
	}
	if entity == "listing_baseline" {
		return st.ListingBaseline(ctx, in.ID)
	}
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
		action, err := beginAgentAction(ctx, st, in)
		if err != nil {
			return err
		}
		if action != nil && action.Replayed {
			result, err = replayAction(action)
			return err
		}
		if entity == "admin_user" || entity == "role" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended('admin:super',0))").Error; err != nil {
				return err
			}
		}
		refs, err := historyRefs(ctx, st, op, entity, in, nil)
		if err != nil {
			return err
		}
		if err := checkActionRevision(ctx, st, action, refs); err != nil {
			return err
		}
		states, err := captureHistory(ctx, st, refs)
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
		preview, err := finishHistory(ctx, st, refs, states)
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
		var after any = mapped(in.Body, map[string]string{"name": "name", "permissions": "permissions", "allowDelete": "allowDelete", "ipAllowlist": "ipAllowlist", "expiresAt": "expiresAt", "limits": "limits", "sourceLocale": "sourceLocale", "title": "title", "body": "body", "sections": "sections", "term": "term", "translations": "translations", "doNotTranslate": "doNotTranslate", "developer": "developer", "repoUrl": "repoUrl", "hidden": "hidden", "editorChoice": "editorChoice", "tags": "tags", "notes": "notes", "downloadSize": "downloadSize", "accentColor": "accentColor", "displayName": "displayName", "summary": "summary", "description": "description", "assetId": "assetId", "captionZh": "captionZh", "captionEn": "captionEn", "theme": "theme", "items": "items", "ids": "ids", "slug": "slug", "icon": "icon", "appliesTo": "appliesTo", "visible": "visible", "hiddenByDefault": "hiddenByDefault", "parentId": "parentId", "i18n": "i18n", "roles": "roles", "status": "status", "nickName": "nickName", "email": "email", "userName": "userName", "roleCode": "roleCode", "roleName": "roleName", "roleDesc": "roleDesc", "permissionCodes": "permissionCodes", "terms": "terms", "enabled": "enabled", "handlerNote": "handlerNote", "manual": "manual", "autoEnabled": "autoEnabled", "value": "value", "key": "key", "nameZh": "nameZh", "nameEn": "nameEn", "probeUrl": "probeUrl", "recommended": "recommended", "sort": "sort", "apiDomain": "apiDomain", "bottleDomain": "bottleDomain", "brewGitRemote": "brewGitRemote", "coreGitRemote": "coreGitRemote", "publishAt": "publishAt", "unpublishAt": "unpublishAt", "coverAssetId": "coverAssetId", "placement": "placement", "targetType": "targetType", "packageId": "packageId", "collectionId": "collectionId", "url": "url", "glowColor": "glowColor", "startsAt": "startsAt", "endsAt": "endsAt", "type": "type", "amount": "amount", "currency": "currency", "region": "region", "plan": "plan", "period": "period", "appVersion": "appVersion", "sourceUrl": "sourceUrl", "method": "method", "verifiedAt": "verifiedAt", "ranges": "ranges", "note": "note"})
		if op != "UpsertReleaseNotes" && (entity == "release" || entity == "translation" || entity == "changelog_source" || entity == "link_app" || entity == "update_feed" || entity == "listing" || entity == "listing_baseline") {
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
		if err := store.Audit(ctx, tx, in.Actor, op, entity, entityID, before, redact(after)); err != nil {
			return err
		}
		if action != nil {
			afterStates, err := captureHistory(ctx, st, refs)
			if err != nil {
				return err
			}
			return finishAgentAction(ctx, st, action, in, entity, entityID, map[string]any{"history": states, "object": before}, map[string]any{"history": afterStates, "object": after}, result)
		}
		return nil
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
func (s *Service) Execute(ctx context.Context, op string, in Input) (any, error) {
	ctx = operationContext(ctx, op, in)
	domain.CurrentOperation(ctx).WebBaseURL = s.Config.WebBaseURL
	// Control-plane operations and batch translations have their own permission,
	// fencing and transaction boundaries; they never bypass the business gate.
	switch op {
	case "ClaimSeoTasks", "GetSeoTask", "CompleteSeoTask", "DeferSeoTask":
		return s.seoTaskControl(ctx, op, in)
	case "CreateSeoEvidence":
		return s.captureSeoEvidence(ctx, in)
	case "ListSeoActions":
		return s.seoActions(ctx, in)
	case "GetSeoAutomation", "UpdateSeoAutomation":
		return s.seoAutomation(ctx, op, in)
	case "RecordSeoGeo":
		return s.recordSeoGeo(ctx, in)
	case "ListSeoGeo", "GetSeoGeo":
		return s.readSeoGeo(ctx, op, in)
	case "ImportSeoTasks":
		return s.importSeoTasks(ctx, in)
	case "GetTranslationsSource":
		return s.translationsSource(ctx, in)
	case "WriteTranslations":
		return s.writeTranslations(ctx, in)
	}
	if current := domain.CurrentOperation(ctx); current.AgentClientID > 0 && !strings.HasPrefix(op, "Get") && !strings.HasPrefix(op, "List") {
		return s.executeAgent(ctx, op, in)
	}
	return s.execute(ctx, op, in)
}

func (s *Service) execute(ctx context.Context, op string, in Input) (any, error) {
	switch op {
	case "ProposeSnippetTemplate":
		return s.proposeSnippet(ctx, in)
	case "CompensateSeoAction":
		return s.compensateAction(ctx, in)
	case "GetSeoListingCandidate", "UpsertSeoListingCandidate":
		return s.listingCandidate(ctx, op, in)
	case "LockSeoListingBaseline":
		return s.lockListingBaseline(ctx, in)
	case "GetSeoOverview":
		return s.seoOverview(ctx)
	case "ListSeoAlerts":
		return s.seoAlerts(ctx, in)
	case "GetSeoPage":
		return s.seoPage(ctx, in)
	case "ListSeoKeywords", "UpsertSeoKeywords":
		return s.seoKeywords(ctx, op, in)
	case "UpsertUpdateFeed", "DisableUpdateFeed":
		return s.updateFeedOperation(ctx, op, in)
	case "ListLinkPackages", "AddLinkPackage", "UpdateLinkPackage", "RemoveLinkPackage":
		return s.linkOperation(ctx, op, in)
	case "ListAdminVersions":
		return s.adminVersions(ctx, in)
	case "ListAdminCatalogChanges":
		return s.catalogActivity(ctx, in)
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
	case "GetSeoProvider", "UpdateSeoProvider":
		return s.seoProvider(ctx, op, in)
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
			return nil, domain.Validation()
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
