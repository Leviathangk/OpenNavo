//go:build integration

package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestMCPWorkflowContracts(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://audit.example"}}
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(90101,'audit-agent-one','isolated-test'),(90102,'audit-agent-two','isolated-test');
 INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (91001,'cask','audit-cask','audit-cask','homebrew/cask','Audit Cask','1.0','1.0','{}','audit'),
 (91002,'link','audit-link','audit-link','','Audit Link','1.0','1.0','{}','audit');
 INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(91003,'screenshot','audit-shot','https://assets.example.test/audit.png',1280,800,1,'audit','image/png')`).Error)
	category, err := svc.Execute(ctx, "CreateCategory", Input{Body: map[string]any{"slug": "audit-tools", "icon": "lucide:box", "appliesTo": "cask", "visible": true, "hiddenByDefault": false, "sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{"name": "Tools"}}}})
	require.NoError(t, err)
	categoryID := toInt64(hermesMap(category)["id"])
	agentContext := func(actor, client, token int64, key string, permissions ...string) context.Context {
		return domain.WithOperation(ctx, &domain.Operation{RequestID: uuid.NewString(), Name: "fixTranslation", ActorID: &actor, AgentClientID: client, AgentTokenID: token, Permissions: permissions, RequiredPermissions: permissions, AllowDelete: true, Execution: &domain.AgentExecution{IdempotencyKey: key}})
	}
	t.Run("link_can_use_app_category", func(t *testing.T) {
		_, err := svc.Execute(ctx, "SetPackageCategories", Input{ID: 91002, Body: map[string]any{"items": []any{map[string]any{"categoryId": float64(categoryID), "isPrimary": true}}}})
		t.Logf("link_category_assignment_error=%v", err)
		require.NoError(t, err, "link listings should accept the same app category as casks")
	})
	t.Run("announcement_read_has_revision", func(t *testing.T) {
		_, err := svc.Execute(ctx, "UpdateAnnouncement", Input{Body: map[string]any{"enabled": true, "sourceLocale": "en-US", "title": "Audit", "body": "Local audit announcement"}})
		require.NoError(t, err)
		result, err := svc.Execute(ctx, "GetAnnouncement", Input{})
		require.NoError(t, err)
		view := hermesMap(result)
		t.Logf("announcement_read_revision=%v", view["revision"])
		require.Contains(t, view, "revision", "the ordinary write guide requires the revision returned by the read")
	})
	t.Run("screenshot_guard_from_package_read", func(t *testing.T) {
		_, err := svc.Execute(ctx, "UpdatePackageText", Input{ID: 91001, Body: map[string]any{"sourceLocale": "en-US", "summary": "An audit cask", "description": "A local audit fixture."}})
		require.NoError(t, err)
		pkg, err := svc.Execute(ctx, "GetAdminPackage", Input{ID: 91001})
		require.NoError(t, err)
		readRevision := toInt64(hermesMap(pkg)["revision"])
		writeRevision, err := svc.CurrentRevision(ctx, "AddPackageScreenshot", Input{ID: 91001})
		require.NoError(t, err)
		require.Positive(t, readRevision)
		op := &domain.Operation{RequestID: uuid.NewString(), Name: "addPackageScreenshot", AgentClientID: 90101, AgentTokenID: 1, Permissions: []string{"catalog:asset:upload"}, RequiredPermissions: []string{"catalog:asset:upload"}, DryRun: true, Execution: &domain.AgentExecution{IdempotencyKey: "audit-screenshot-001", ExpectedRevisionSet: true, ExpectedRevision: readRevision}}
		_, err = svc.Execute(domain.WithOperation(ctx, op), "AddPackageScreenshot", Input{ID: 91001, Body: map[string]any{"assetId": float64(91003), "theme": "dark"}})
		t.Logf("package_read_revision=%d screenshot_write_revision=%d error=%v", readRevision, writeRevision, err)
		require.NoError(t, err, "the documented read revision must guard a package screenshot write")
		require.Equal(t, readRevision, writeRevision)
		read := func(id int64) int64 {
			view, err := svc.Execute(ctx, "GetAdminPackage", Input{ID: id})
			require.NoError(t, err)
			return toInt64(hermesMap(view)["revision"])
		}
		otherRevision := read(91002)
		mutate := func(name string, revision int64, in Input) (any, error) {
			scope := agentContext(90101, 90101, 1, uuid.NewString(), "catalog:asset:upload")
			operation := domain.CurrentOperation(scope)
			operation.Name = strings.ToLower(name[:1]) + name[1:]
			operation.Execution.ExpectedRevisionSet, operation.Execution.ExpectedRevision = true, revision
			return svc.Execute(scope, name, in)
		}
		created, err := mutate("AddPackageScreenshot", readRevision, Input{ID: 91001, Body: map[string]any{"assetId": float64(91003), "theme": "dark"}})
		require.NoError(t, err)
		shotID := toInt64(hermesMap(created)["id"])
		afterAdd := read(91001)
		require.Greater(t, afterAdd, readRevision)
		require.Equal(t, otherRevision, read(91002), "another package's screenshots never change this guard")
		_, err = mutate("UpdatePackageScreenshot", readRevision, Input{ID: 91001, ScreenshotID: shotID, Body: map[string]any{"theme": "light"}})
		require.ErrorContains(t, err, "revision_conflict")
		_, err = mutate("UpdatePackageScreenshot", afterAdd, Input{ID: 91001, ScreenshotID: shotID, Body: map[string]any{"theme": "light"}})
		require.NoError(t, err)
		afterUpdate := read(91001)
		require.Greater(t, afterUpdate, afterAdd)
		_, err = mutate("DeletePackageScreenshot", afterUpdate, Input{ID: 91001, ScreenshotID: shotID})
		require.NoError(t, err)
		afterDelete := read(91001)
		require.Greater(t, afterDelete, afterUpdate, "deleted screenshots remain part of the package revision")
		_, err = mutate("AddPackageScreenshot", afterDelete, Input{ID: 91001, Body: map[string]any{"assetId": float64(91003), "theme": "dark"}})
		require.NoError(t, err)
	})
	t.Run("legacy_translation_obeys_current_limits", func(t *testing.T) {
		fields := map[string]any{"name": strings.Repeat("X", 31)}
		ref := map[string]any{"entity": "category", "id": float64(categoryID)}
		scope := agentContext(90101, 90101, 1, "audit-legacy-translation", "translation:review", "catalog:category:edit")
		_, validErr := svc.Execute(scope, "FixTranslation", Input{Body: map[string]any{"ref": ref, "locale": "de-DE", "fields": map[string]any{"name": "Tools"}}})
		require.NoError(t, validErr)
		scope = agentContext(90101, 90101, 1, "audit-legacy-overlong", "translation:review", "catalog:category:edit")
		_, legacyErr := svc.Execute(scope, "FixTranslation", Input{Body: map[string]any{"ref": ref, "locale": "de-DE", "fields": fields}})
		result, batchErr := svc.Execute(scope, "WriteTranslations", Input{Body: map[string]any{"ref": ref, "translations": []any{map[string]any{"locale": "de-DE", "fields": fields, "idempotencyKey": "audit-batch-translation"}}}})
		require.NoError(t, batchErr)
		t.Logf("legacy_error=%v batch_result=%v", legacyErr, result)
		require.Error(t, legacyErr, "the legacy endpoint must reject the same overlong value the batch endpoint rejects")
		rows := hermesMap(result)["results"].([]any)
		require.Equal(t, "failed", rows[0].(map[string]any)["status"])
		var name string
		require.NoError(t, st.DB.Raw("SELECT name FROM category_i18n WHERE category_id=? AND locale='de-DE'", categoryID).Scan(&name).Error)
		require.Equal(t, "Tools", name, "failed validation never persists overlong text")
	})
	t.Run("article_idempotency_is_scoped_to_owner", func(t *testing.T) {
		create := func(actor, client, token int64, slug string) map[string]any {
			scope := agentContext(actor, client, token, "audit-shared-article-key", "content:article:edit")
			domain.CurrentOperation(scope).Name = "createArticle"
			result, err := svc.Execute(scope, "CreateArticle", Input{Body: map[string]any{"slug": slug, "sourceLocale": "en-US", "type": "news", "title": slug, "idempotencyKey": "audit-shared-article-key"}})
			require.NoError(t, err)
			return hermesMap(result)
		}
		first := create(90101, 90101, 1, "audit-article-one")
		replay := create(90101, 90101, 1, "audit-article-one")
		require.EqualValues(t, first["id"], replay["id"])
		second := create(90102, 90102, 2, "audit-article-two")
		t.Logf("first_id=%v first_slug=%v second_id=%v second_slug=%v", first["id"], first["slug"], second["id"], second["slug"])
		require.NotEqual(t, first["id"], second["id"], "independent owners with the same retry key must not silently share one article")
		changed := agentContext(90101, 90101, 1, "audit-new-outer-key", "content:article:edit")
		_, err := svc.Execute(changed, "CreateArticle", Input{Body: map[string]any{"slug": "changed-request", "sourceLocale": "en-US", "type": "news", "title": "Different request", "idempotencyKey": "audit-shared-article-key"}})
		require.ErrorContains(t, err, "idempotency_key_reused")
		for i, key := range []string{"1", "1.0"} {
			scope := agentContext(90101, 90101, 1, uuid.NewString(), "content:article:edit")
			domain.CurrentOperation(scope).Name = "createArticle"
			slug := []string{"opaque-key-one", "opaque-key-two"}[i]
			_, err := svc.Execute(scope, "CreateArticle", Input{Body: map[string]any{"slug": slug, "sourceLocale": "en-US", "type": "news", "title": "Opaque keys", "idempotencyKey": key}})
			require.NoError(t, err, "legacy keys are opaque strings, never normalized as JSON numbers")
		}
	})
	t.Run("article_calls_are_visible_to_authorized_agent", func(t *testing.T) {
		require.NoError(t, st.DB.Exec(`INSERT INTO agent_clients(id,user_id,name) OVERRIDING SYSTEM VALUE VALUES(90101,90101,'Audit agent');
  INSERT INTO agent_call_logs(request_id,client_id,tool,operation_id,objects,code) VALUES('audit-own-article-log',90101,'translations_write','writeTranslations','[{"entity":"article","objectKey":"1"}]','0000')`).Error)
		unfiltered, err := svc.agentLogs(ctx, "ListAgentCalls", Input{Params: map[string]any{}})
		require.NoError(t, err)
		actor := int64(90101)
		scope := domain.WithOperation(ctx, &domain.Operation{AgentClientID: 90101, ActorID: &actor, Permissions: []string{"content:article:edit", "agent:log:view"}})
		filtered, err := svc.agentLogs(scope, "ListAgentCalls", Input{Params: map[string]any{}})
		require.NoError(t, err)
		t.Logf("stored_call_total=%v authorized_agent_total=%v", hermesMap(unfiltered)["total"], hermesMap(filtered)["total"])
		require.EqualValues(t, 1, hermesMap(filtered)["total"], "own article calls must survive the entity permission filter")
		for _, tc := range []struct {
			entity, key string
			total       int
		}{{"article", "1", 1}, {"article", "2", 0}, {"category", "1", 0}} {
			result, err := svc.agentLogs(scope, "ListAgentCalls", Input{Params: map[string]any{"entity": tc.entity, "objectkey": tc.key}})
			require.NoError(t, err)
			require.EqualValues(t, tc.total, hermesMap(result)["total"])
		}
	})
}

func TestMCPTextContracts(t *testing.T) {
	t.Run("valid_section_combinations", func(t *testing.T) {
		groups := []any{}
		for range 8 {
			items := []any{}
			for range 6 {
				items = append(items, strings.Repeat("<>&字", 20))
			}
			groups = append(groups, map[string]any{"area": "Changes", "items": items})
		}
		fields := map[string]any{"sections": groups}
		encoded, _ := json.Marshal(fields)
		err := store.ValidateContentFields("release", fields)
		t.Logf("groups=8 items_per_group=6 item_chars=80 serialized_chars=%d error=%v", len(encoded), err)
		require.NoError(t, err, "a combination within every advertised section bound must be writable")
	})
	t.Run("character_limits_count_characters", func(t *testing.T) {
		now := time.Now().UTC()
		checked, until := now, now.Add(24*time.Hour)
		value := facts.BackgroundContent{Locale: "zh-CN", Description: strings.Repeat("x", 4000), ItemType: "unknown", Evidence: facts.Proof{SourceURL: "https://vendor.example/guide", CheckedAt: &checked, ValidUntil: &until, Status: "verified"}}
		require.NoError(t, value.Validate(now))
		value.Description = strings.Repeat("字", 4000)
		err := value.Validate(now)
		t.Logf("description_characters=%d utf8_bytes=%d advertised_limit=10000 error=%v", utf8.RuneCountInString(value.Description), len(value.Description), err)
		require.NoError(t, err, "the public 10000-character limit must accept 4000 Chinese characters")
	})
}

type auditLockGitHub struct {
	st            *store.Store
	lockAvailable bool
	calls         int
}

func (a *auditLockGitHub) Get(ctx context.Context, target string, maximum int64, headers http.Header) ([]byte, int, http.Header, error) {
	a.calls++
	err := a.st.DB.WithContext(ctx).Raw("SELECT pg_try_advisory_xact_lock(hashtextextended('catalog:changes',0))").Scan(&a.lockAvailable).Error
	if err != nil {
		return nil, 0, nil, err
	}
	return nil, 0, nil, errors.New("isolated audit stopped before remote I/O")
}
func TestMCPTapFetchDoesNotLockCatalog(t *testing.T) {
	st := testutil.NewSEOStore(t)
	fake := &auditLockGitHub{st: st}
	svc := &Service{Store: st, Taps: &taps.Service{Store: st, GitHub: fake}}
	ctx := domain.WithOperation(context.Background(), &domain.Operation{RequestID: uuid.NewString(), Name: "addTapPackage", AgentClientID: 90101, AgentTokenID: 1, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, DryRun: true, Execution: &domain.AgentExecution{IdempotencyKey: "audit-tap-lock-001"}})
	_, err := svc.Execute(ctx, "AddTapPackage", Input{Body: map[string]any{"cask": "audit-owner/tap/audit-app"}})
	require.Error(t, err)
	require.Positive(t, fake.calls)
	t.Logf("github_calls=%d catalog_lock_available_during_network=%v", fake.calls, fake.lockAvailable)
	require.True(t, fake.lockAvailable, "reading the upstream cask must not hold the global catalog write lock")
}
