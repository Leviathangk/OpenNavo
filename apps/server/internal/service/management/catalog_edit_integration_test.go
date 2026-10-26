//go:build integration

package management

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCatalogMaintenanceWithoutTaskOrEvidence(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(901,'cask','autonomous','autonomous','homebrew/cask','Arc','2.0','2.0','{}','fixture');
INSERT INTO package_versions(package_id,version,version_base) VALUES(901,'2.0','2.0');
INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(902,'screenshot','shot','https://assets.example.test/shot.png',1280,800,1,'shot','image/png')`).Error)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	execute := func(name, key string, dry bool, revision *int64, in Input, permissions ...string) (any, error) {
		execution := &domain.AgentExecution{IdempotencyKey: key}
		if revision != nil {
			execution.ExpectedRevision, execution.ExpectedRevisionSet = *revision, true
		}
		op := &domain.Operation{RequestID: uuid.NewString(), Name: strings.ToLower(name[:1]) + name[1:], AgentClientID: 1, AgentTokenID: 2, Permissions: permissions, RequiredPermissions: permissions, DryRun: dry, Execution: execution}
		return svc.Execute(domain.WithOperation(context.Background(), op), name, in)
	}
	count := func(table, where string, args ...any) int64 {
		var n int64
		require.NoError(t, st.DB.Table(table).Where(where, args...).Count(&n).Error)
		return n
	}
	in := Input{ID: 901, Version: "2.0", Body: map[string]any{"sourceLocale": "en-US", "title": "Arc search", "summary": "Arc search is faster.", "bodyMarkdown": "Arc search is faster.", "sections": []any{}}}

	// The preview needs no revision and reports the one to commit with.
	preview, err := execute("UpsertReleaseNotes", "notes-preview-01", true, nil, in, "changelog:release:edit")
	require.NoError(t, err)
	view := preview.(map[string]any)
	require.Equal(t, true, view["dryRun"])
	require.Equal(t, "preview", view["mode"])
	require.EqualValues(t, 0, view["expectedRevision"])
	require.Zero(t, count("releases", "package_id=901"), "preview must not persist content")
	require.Zero(t, count("agent_write_controls", "1=1"), "a preview does not consume its idempotency key")

	// A direct commit needs neither a preview nor a revision.
	var app *domain.AppError
	zero := int64(0)
	first, err := execute("UpsertReleaseNotes", "notes-commit-01", false, nil, in, "changelog:release:edit")
	require.NoError(t, err)
	var releaseID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM releases WHERE package_id=901 AND version='2.0'").Scan(&releaseID).Error)
	require.Positive(t, releaseID)
	require.Positive(t, count("content_revisions", "entity='release' AND object_key=?", strconv.FormatInt(releaseID, 10)), "direct edits retain revision history")
	require.EqualValues(t, 1, count("agent_write_events", "operation='UpsertReleaseNotes' AND result='live'"), "direct writes remain visible to write statistics")

	// A lost response is retried with the same key: the first result, no second write.
	revisions := count("content_revisions", "entity='release' AND object_key=?", strconv.FormatInt(releaseID, 10))
	replay, err := execute("UpsertReleaseNotes", "notes-commit-01", false, nil, in, "changelog:release:edit")
	require.NoError(t, err)
	require.JSONEq(t, jsonText(t, first), jsonText(t, replay))
	require.Equal(t, revisions, count("content_revisions", "entity='release' AND object_key=?", strconv.FormatInt(releaseID, 10)))
	changed := Input{ID: 901, Version: "2.0", Body: map[string]any{"sourceLocale": "en-US", "title": "Arc search", "summary": "Different summary.", "bodyMarkdown": "Arc search is faster.", "sections": []any{}}}
	_, err = execute("UpsertReleaseNotes", "notes-commit-01", false, nil, changed, "changelog:release:edit")
	require.ErrorAs(t, err, &app)
	require.Equal(t, domain.CodeConflict, app.Code, "one key never names two different requests")
	require.Equal(t, "rejected: idempotency_key_reused", app.Msg)

	// A revision that changed since the preview rejects the commit and names the current one.
	_, err = execute("UpsertReleaseNotes", "notes-commit-02", false, &zero, changed, "changelog:release:edit")
	require.ErrorAs(t, err, &app)
	require.Equal(t, domain.CodeConflict, app.Code)
	preview, err = execute("UpsertReleaseNotes", "notes-commit-02", true, nil, changed, "changelog:release:edit")
	require.NoError(t, err)
	current := toInt64(preview.(map[string]any)["expectedRevision"])
	require.Positive(t, current)
	require.Equal(t, "rejected: revision_conflict:"+strconv.FormatInt(current, 10), app.Msg)
	read, err := svc.CurrentRevision(context.Background(), "UpsertReleaseNotes", Input{ID: 901, Version: "2.0"})
	require.NoError(t, err)
	require.Equal(t, current, read, "reads report the revision the preview reports")

	// An unchanged write with the current revision succeeds without a statistic.
	_, err = execute("UpsertReleaseNotes", "notes-commit-03", false, &current, in, "changelog:release:edit")
	require.NoError(t, err)
	require.EqualValues(t, 1, count("agent_write_events", "operation='UpsertReleaseNotes' AND result='live'"))

	// Adding a screenshot twice with one key keeps a single screenshot.
	shot := Input{ID: 901, Body: map[string]any{"assetId": float64(902), "theme": "dark"}}
	preview, err = execute("AddPackageScreenshot", "shot-add-0001", true, nil, shot, "catalog:package:edit")
	require.NoError(t, err)
	shotRevision := toInt64(preview.(map[string]any)["expectedRevision"])
	for range 2 {
		_, err = execute("AddPackageScreenshot", "shot-add-0001", false, &shotRevision, shot, "catalog:package:edit")
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, count("package_screenshots", "package_id=901"))

	ref := content.Ref{Entity: "release", ID: releaseID}
	source, err := st.ReadContentSource(context.Background(), ref)
	require.NoError(t, err)
	value, err := st.TranslationValue(context.Background(), ref, "de-DE")
	require.NoError(t, err)
	translated := map[string]string{}
	for key := range content.Flatten(source.Fields) {
		translated[key] = "Arc: verbesserte Suche"
	}
	fields := content.Replace(source.Fields, translated)
	request := translationBatch{Ref: ref, Model: "fixture", Translations: []translationItem{{Locale: "de-DE", SourceHash: source.Hash, ExpectedRevision: value.Revision, IdempotencyKey: "direct-translation", Fields: fields}}}
	ctx := hermesContext("translation:review", "changelog:release:edit")
	domain.CurrentOperation(ctx).DryRun = true
	previewResult, err := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "preview", hermesMap(previewResult)["results"].([]any)[0].(map[string]any)["status"])
	domain.CurrentOperation(ctx).DryRun = false
	result, err := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "written", hermesMap(result)["results"].([]any)[0].(map[string]any)["status"])
	// A lost response can be retried with the same key and original revision.
	result, err = svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "written", hermesMap(result)["results"].([]any)[0].(map[string]any)["status"])
	// A new request carrying unchanged content also adds no write statistic.
	currentTranslation, err := st.TranslationValue(context.Background(), ref, "de-DE")
	require.NoError(t, err)
	request.Translations[0].ExpectedRevision = currentTranslation.Revision
	request.Translations[0].IdempotencyKey = "direct-unchanged"
	result, err = svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "written", hermesMap(result)["results"].([]any)[0].(map[string]any)["status"])
	require.EqualValues(t, 1, count("agent_write_events", "operation='WriteTranslations' AND result='live'"))
	request.Translations[0].Fields["summary"] = "different payload"
	result, err = svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "failed", hermesMap(result)["results"].([]any)[0].(map[string]any)["status"])
	require.Equal(t, "idempotency_key_reused", hermesMap(result)["results"].([]any)[0].(map[string]any)["reason"])
	request.Translations[0].IdempotencyKey = "different-request"
	request.Translations[0].ExpectedRevision = value.Revision
	result, err = svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "failed", hermesMap(result)["results"].([]any)[0].(map[string]any)["status"])
	require.Equal(t, "revision_conflict", hermesMap(result)["results"].([]any)[0].(map[string]any)["reason"])

	// The translation added a revision: preview again for the current one.
	preview, err = execute("UpsertReleaseNotes", "notes-commit-04", true, nil, changed, "changelog:release:edit")
	require.NoError(t, err)
	current = toInt64(preview.(map[string]any)["expectedRevision"])
	denied := &domain.Operation{RequestID: uuid.NewString(), Name: "upsertReleaseNotes", AgentClientID: 1, AgentTokenID: 2, RequiredPermissions: []string{"changelog:release:edit"}, Execution: &domain.AgentExecution{IdempotencyKey: "notes-commit-04", ExpectedRevision: current, ExpectedRevisionSet: true}}
	_, err = svc.Execute(domain.WithOperation(context.Background(), denied), "UpsertReleaseNotes", changed)
	require.True(t, errors.As(err, &app))
	require.Equal(t, domain.CodeForbidden, app.Code)
	_, err = execute("UpsertReleaseNotes", "notes-commit-05", false, &current, changed, "changelog:release:edit")
	require.NoError(t, err)
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func toInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	}
	return -1
}
