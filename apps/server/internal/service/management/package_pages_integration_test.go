//go:build integration

package management

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Agents write intent switches and overview overrides through the write layer: the expected revision,
// idempotency, previews and the write log work as for every other catalog edit.
func TestAgentWritesIntentPagesAndOverviewSEO(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en) VALUES
 (601,'cask','agent-pages','agent-pages','homebrew/cask','Agent Pages','1','1','{}','fixture','Edits pages')`).Error)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	execute := func(name, key string, dry bool, revision *int64, in Input) (any, error) {
		t.Helper()
		execution := &domain.AgentExecution{IdempotencyKey: key}
		if revision != nil {
			execution.ExpectedRevision, execution.ExpectedRevisionSet = *revision, true
		}
		permissions := []string{"catalog:package:edit"}
		user := int64(5)
		op := &domain.Operation{RequestID: uuid.NewString(), Name: name, AgentClientID: 3, AgentTokenID: 7, ActorID: &user, Permissions: permissions, RequiredPermissions: permissions, DryRun: dry, Execution: execution}
		return svc.Execute(domain.WithOperation(ctx, op), name, in)
	}
	count := func(table, where string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, st.DB.Table(table).Where(where, args...).Count(&n).Error)
		return n
	}
	intent := Input{ID: 601, Body: map[string]any{"items": []any{map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": true}}}}

	// The preview reports the revision to commit with and writes nothing.
	preview, err := execute("SetPackageIntentPages", "intent-preview-01", true, nil, intent)
	require.NoError(t, err)
	view := hermesMap(preview)
	require.Equal(t, true, view["dryRun"])
	require.Equal(t, "preview", view["mode"])
	require.EqualValues(t, 0, view["expectedRevision"])
	require.Equal(t, []any{"https://fixture.example/apps/agent-pages/alternatives"}, view["affectedUrls"])
	after := view["changes"].([]any)[0].(map[string]any)["after"].(map[string]any)["package_intent_pages"].([]any)
	require.Len(t, after, 1)
	require.Equal(t, "agent:3:7", after[0].(map[string]any)["created_by"])
	require.Zero(t, count("package_intent_pages", "package_id=601"))

	// A stale revision names the current one; the right one writes and logs the change.
	stale := int64(9)
	_, err = execute("SetPackageIntentPages", "intent-commit-01", false, &stale, intent)
	var app *domain.AppError
	require.ErrorAs(t, err, &app)
	require.Equal(t, domain.CodeConflict, app.Code)
	require.Equal(t, "rejected: revision_conflict:0", app.Msg)
	zero := int64(0)
	written, err := execute("SetPackageIntentPages", "intent-commit-02", false, &zero, intent)
	require.NoError(t, err)
	result := hermesMap(written)
	require.EqualValues(t, 1, result["revision"])
	require.Len(t, result["items"], 20)
	require.Equal(t, "agent:3:7", result["items"].([]any)[0].(map[string]any)["enabledBy"])
	require.EqualValues(t, 1, count("agent_write_events", "operation='SetPackageIntentPages' AND result='live'"))
	read, err := svc.CurrentRevision(ctx, "SetPackageIntentPages", Input{ID: 601})
	require.NoError(t, err)
	require.EqualValues(t, 1, read, "reads report the revision writes check")

	// A retry with the same key replays the first result; the key never names another request.
	replay, err := execute("SetPackageIntentPages", "intent-commit-02", false, &zero, intent)
	require.NoError(t, err)
	require.JSONEq(t, jsonText(t, written), jsonText(t, replay))
	other := Input{ID: 601, Body: map[string]any{"items": []any{map[string]any{"pageKind": "alternatives", "locale": "ja-JP", "enabled": true}}}}
	_, err = execute("SetPackageIntentPages", "intent-commit-02", false, nil, other)
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: idempotency_key_reused", app.Msg)

	// An unchanged write keeps the revision and logs nothing.
	one := int64(1)
	_, err = execute("SetPackageIntentPages", "intent-commit-03", false, &one, intent)
	require.NoError(t, err)
	require.EqualValues(t, 1, count("agent_write_events", "operation='SetPackageIntentPages' AND result='live'"))
	_, err = execute("SetPackageIntentPages", "intent-commit-04", false, nil, Input{ID: 601, Body: map[string]any{"items": []any{map[string]any{"pageKind": "alternatives", "locale": "zh-Hant", "enabled": true}}}})
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: derived_locale_read_only:zh-Hant:zh-CN", app.Msg)

	// Overview overrides follow the same write layer.
	seo := Input{ID: 601, Body: map[string]any{"locale": "en-US", "title": "Agent Pages for Mac"}}
	preview, err = execute("SetPackageSeo", "seo-preview-01", true, nil, seo)
	require.NoError(t, err)
	view = hermesMap(preview)
	require.Equal(t, true, view["dryRun"])
	require.Equal(t, []any{"https://fixture.example/apps/agent-pages"}, view["affectedUrls"])
	require.Zero(t, count("package_seo_overrides", "package_id=601"))
	written, err = execute("SetPackageSeo", "seo-commit-01", false, &zero, seo)
	require.NoError(t, err)
	result = hermesMap(written)
	require.EqualValues(t, 1, result["revision"])
	item := result["items"].([]any)[0].(map[string]any)
	require.Equal(t, "Agent Pages for Mac", item["title"])
	require.Equal(t, "agent:3:7", item["updatedBy"])
	_, err = execute("SetPackageSeo", "seo-commit-02", false, &zero, Input{ID: 601, Body: map[string]any{"locale": "en-US", "description": "Stale"}})
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: revision_conflict:1", app.Msg)
	_, err = execute("SetPackageSeo", "seo-commit-03", false, nil, Input{ID: 601, Body: map[string]any{"locale": "en-US", "title": "Line\nbreak"}})
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: seo_text_invalid:title", app.Msg)
	_, err = execute("SetPackageSeo", "seo-commit-04", false, nil, Input{ID: 601, Body: map[string]any{"locale": "en-US", "keywords": "nope"}})
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: unknown_field:keywords", app.Msg)
	require.EqualValues(t, 1, count("agent_write_events", "operation='SetPackageSeo' AND result='live'"))

	// A removed app has no pages: reads answer 404 like writes, and its stored rows stay.
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE id=601").Error)
	for _, op := range []string{"GetPackageSeo", "GetPackageIntentPages"} {
		_, err = svc.Execute(ctx, op, Input{ID: 601})
		require.ErrorAs(t, err, &app, op)
		require.Equal(t, domain.CodeNotFound, app.Code, op)
	}
	_, err = execute("SetPackageSeo", "seo-commit-05", false, nil, Input{ID: 601, Body: map[string]any{"locale": "en-US", "title": "Gone"}})
	require.ErrorAs(t, err, &app)
	require.Equal(t, domain.CodeNotFound, app.Code)
	require.EqualValues(t, 1, count("package_seo_overrides", "package_id=601"))
}

// The dashboard's agent card counts every language an agent translated today, by FixTranslation or a
// WriteTranslations batch.
func TestAgentCardCountsBatchTranslations(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO content_revision_requests(request_id) VALUES('card-batch'),('card-fix'),('card-admin');
 INSERT INTO content_revisions(entity,object_key,version,action,operation_id,required_permissions,actor_type,request_id,after_data,locale) VALUES
 ('package','1',1,'update','WriteTranslations',ARRAY['translation:review'],'agent','card-batch','{}','de-DE'),
 ('package','1',2,'update','WriteTranslations',ARRAY['translation:review'],'agent','card-batch','{}','ja-JP'),
 ('package','1',3,'update','fixTranslation',ARRAY['translation:review'],'agent','card-fix','{}','zh-CN'),
 ('package','1',4,'update','fixTranslation',ARRAY['translation:review'],'admin','card-admin','{}','ja-JP'),
 ('package','1',5,'update','updatePackageText',ARRAY['catalog:package:edit'],'agent','card-fix','{}','en-US')`).Error)
	svc := &Service{Store: st}
	overview, err := svc.Execute(context.Background(), "GetDashboardOverview", Input{})
	require.NoError(t, err)
	require.EqualValues(t, 3, hermesMap(overview)["hermes"].(map[string]any)["translations"])
}
