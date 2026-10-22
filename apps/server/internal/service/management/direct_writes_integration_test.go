//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDirectAgentBusinessWrites(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'direct-agent','isolated-test');
UPDATE seo_settings SET automation=jsonb_set(automation,'{actions}',(SELECT jsonb_object_agg(key,'{"enabled":false,"mode":"proposal"}'::jsonb) FROM jsonb_each(automation->'actions')));
INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(900,'icon','icon','https://cdn.example.test/icon.png',256,256,1,'icon','image/png')`).Error)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://site.example.test"}}
	actor := int64(1)
	run := func(name string, in Input, dry bool) any {
		t.Helper()
		in.Body = hermesMap(in.Body)
		op := &domain.Operation{Name: name, AgentClientID: 1, AgentTokenID: 2, ActorID: &actor, RequestID: uuid.NewString(), Permissions: []string{"*"}, RequiredPermissions: []string{historyPermission(name)}, DryRun: dry}
		result, err := svc.Execute(domain.WithOperation(context.Background(), op), name, in)
		require.NoError(t, err, name)
		return result
	}
	count := func(table string) int64 {
		var n int64
		require.NoError(t, st.DB.Table(table).Count(&n).Error)
		return n
	}
	link := Input{Body: map[string]any{"token": "direct-app", "name": "Direct App", "homepage": "https://vendor.example.test", "downloadUrl": "https://downloads.example.test/app.dmg", "githubRepo": "example/app"}}
	preview := run("AddLinkPackage", link, true).(map[string]any)
	require.Equal(t, "preview", preview["mode"])
	require.Zero(t, count("packages"))
	created := run("AddLinkPackage", link, false).(map[string]any)
	id := valueID(created["id"])
	require.Positive(t, id)
	run("UpdateLinkPackage", Input{ID: id, Body: map[string]any{"downloadUrl": "https://downloads.example.test/app-v2.dmg", "sparkleFeed": "https://vendor.example.test/appcast.xml"}}, false)
	run("UpdatePackageText", Input{ID: id, Body: map[string]any{"sourceLocale": "en-US", "summary": "A utility for Mac and Windows.", "description": "Our tests: the new version is faster."}}, false)
	var textRevision int64
	require.NoError(t, st.DB.Raw("SELECT max(id) FROM content_revisions WHERE entity='package' AND object_key=?", (content.Ref{Entity: "package", ID: id}).Key()).Scan(&textRevision).Error)
	run("UpdatePackageText", Input{ID: id, Body: map[string]any{"sourceLocale": "en-US", "description": "A later edit."}}, false)
	run("RestoreRevision", Input{ID: textRevision, Body: map[string]any{}}, false)
	var restoredText string
	require.NoError(t, st.DB.Raw("SELECT description FROM package_i18n WHERE package_id=? AND locale='en-US'", id).Scan(&restoredText).Error)
	require.Equal(t, "Our tests: the new version is faster.", restoredText)
	run("SetPackageIcon", Input{ID: id, Body: map[string]any{"assetId": 900}}, false)
	category := run("CreateCategory", Input{Body: map[string]any{"slug": "direct-tools", "icon": "lucide:box", "sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{"name": "Tools"}}}}, false).(map[string]any)
	categoryID := valueID(category["id"])
	require.Positive(t, categoryID)
	translation := run("WriteTranslations", Input{Body: map[string]any{"ref": map[string]any{"entity": "category", "id": categoryID}, "translations": []any{map[string]any{"locale": "zh-CN", "fields": map[string]any{"name": "A completely different label"}}}}}, false).(map[string]any)
	require.Equal(t, "written", translation["results"].([]any)[0].(map[string]any)["status"])
	value, err := st.TranslationValue(context.Background(), content.Ref{Entity: "category", ID: categoryID}, "zh-CN")
	require.NoError(t, err)
	require.Equal(t, "agent:unspecified", *value.Model)
	article := run("CreateArticle", Input{Body: map[string]any{"slug": "direct-article", "sourceLocale": "en-US", "type": "review", "title": "Direct publication", "bodyMarkdown": "A short review without an SEO task or test artifact.", "claims": []any{map[string]any{"kind": "test", "text": "An annotation without captured evidence."}}}}, false).(map[string]any)
	articleID := valueID(article["id"])
	run("PublishArticle", Input{ID: articleID, Body: map[string]any{}}, false)
	a, found, err := st.LoadArticle(context.Background(), articleID, false)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "published", a.Status)
	// Candidate publication is equally direct, and subsequent edits must not hit
	// the old database identity/download trigger.
	candidate := run("UpsertSeoListingCandidate", Input{Key: "candidate-app", Body: map[string]any{"action": "publish", "link": map[string]any{"token": "candidate-app", "name": "Candidate App", "homepage": "https://vendor.example.test", "downloadUrl": "https://downloads.example.test/installer.pkg", "githubRepo": "example/candidate"}}}, false).(map[string]any)
	candidateID := *candidate["packageId"].(*int64)
	candidateInput := Input{Key: "candidate-app", Body: map[string]any{"action": "save", "link": map[string]any{"token": "candidate-app", "name": "Candidate App", "homepage": "https://vendor.example.test"}}}
	saved := run("UpsertSeoListingCandidate", candidateInput, false).(map[string]any)
	require.Equal(t, candidateID, *saved["packageId"].(*int64))
	candidateInput.Body["action"] = "publish"
	republished := run("UpsertSeoListingCandidate", candidateInput, false).(map[string]any)
	require.Equal(t, candidateID, *republished["packageId"].(*int64))
	run("UpdateLinkPackage", Input{ID: candidateID, Body: map[string]any{"name": "Renamed App", "downloadUrl": "https://downloads.example.test/new.pkg", "sparkleFeed": "https://vendor.example.test/feed.xml"}}, false)
	records := []any{}
	for _, query := range []string{"platform tools", "platform-tools", "platform_tools"} {
		records = append(records, map[string]any{"keyword": query, "source": "homebrew", "engine": "homebrew", "country": "US", "language": "en-US", "device": "all", "period": "2026-10", "volumeState": "unknown", "observedAt": time.Now().UTC().Format(time.RFC3339)})
	}
	run("UpsertSeoKeywords", Input{Body: map[string]any{"records": records}}, false)
	require.EqualValues(t, 3, count("seo_keywords"))
	require.Zero(t, count("seo_daily_entities"), "business writes never reserve an SEO quota")
	require.Zero(t, count("seo_actions"), "business writes do not fabricate task actions")
	require.Positive(t, count("content_revisions"))
	require.Positive(t, count("audit_logs"))
	var download string
	require.NoError(t, st.DB.Raw("SELECT download_url FROM link_apps WHERE package_id=?", id).Scan(&download).Error)
	require.Equal(t, "https://downloads.example.test/app-v2.dmg", download)
	denied := domain.WithOperation(context.Background(), &domain.Operation{AgentClientID: 1, RequiredPermissions: []string{"catalog:package:edit"}})
	_, err = svc.Execute(denied, "UpdatePackageText", Input{ID: id, Body: map[string]any{"summary": "denied"}})
	require.Error(t, err)
}
