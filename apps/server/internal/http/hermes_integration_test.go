//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestHermesAgentHTTPAuthorizationAndContracts(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("hermes-test-only")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "hermes-admin", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("h", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour * 24, AgentGatewaySecret: strings.Repeat("g", 32), WebBaseURL: "https://example.test"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "hermes-admin", "hermes-test-only", store.AuditActor{})
	require.NoError(t, err)
	manager := &management.Service{Store: st, Config: cfg, Desktop: &desktop.Service{Store: st}}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: manager}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	token := ""
	fixture := httpExecutionFixture{t: t, st: st}
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET automation=jsonb_set(jsonb_set(automation,'{actions,content,mode}','"live"'),'{actions,article,mode}','"live"')`).Error)
	call := func(method, path, body, expected string, agent bool) json.RawMessage {
		t.Helper()
		base, bearer := "/admin-api", pair.Token
		if agent {
			base, bearer = "/agent-api", token
		}
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if agent {
			r.Header.Set("X-Agent-Gateway-Key", cfg.AgentGatewaySecret)
			r.Header.Set("X-Agent-Client-IP", "127.0.0.1")
			r.Header.Set("X-Agent-Tool", "test_tool")
			r.Header.Set("X-Request-ID", uuid.NewString())
			if method != "GET" && path != "/introspect" && !strings.HasPrefix(path, "/desktop-releases") {
				fixture.attach(r, fixture.scope(path, body))
			}
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code)
		var out struct {
			Code string
			Data json.RawMessage
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		require.Equal(t, expected, out.Code, path+" "+w.Body.String())
		if expected == "0000" {
			testutil.ValidateResponse(t, spec, base, r, w)
		}
		return out.Data
	}
	raw := call("POST", "/agent/clients", `{"name":"Test Hermes","notes":"isolated"}`, "0000", false)
	var client struct{ ID int64 }
	require.NoError(t, json.Unmarshal(raw, &client))
	require.Positive(t, client.ID)
	raw = call("POST", "/agent/clients/"+strconv.FormatInt(client.ID, 10)+"/tokens", `{"name":"restricted","permissions":["catalog:package:view","content:glossary:edit","agent:log:view","content:revision:restore"],"allowDelete":false,"ipAllowlist":["127.0.0.1/32"]}`, "0000", false)
	var credential struct {
		Plaintext string
		Token     struct{ ID int64 }
	}
	require.NoError(t, json.Unmarshal(raw, &credential))
	token = credential.Plaintext
	fixture.owner = seotasks.Owner(client.ID, credential.Token.ID)
	require.NotEmpty(t, token)
	call("POST", "/introspect", "", "0000", true)
	call("POST", "/introspect", "", "1004", false)
	for _, path := range []string{"/agent/settings", "/agent/clients", "/agent/clients/" + strconv.FormatInt(client.ID, 10) + "/tokens", "/content/revisions", "/content/trash", "/agent/calls", "/translations/logs", "/glossary", "/translations", "/catalog/changes", "/versions", "/announcement", "/dashboard/overview"} {
		call("GET", path, "", "0000", false)
	}
	call("GET", "/packages?gaps=zhName&gapMode=any", "", "0000", true)
	call("GET", "/app-config", "", "1004", true)
	call("POST", "/desktop-releases", "{}", "1004", true)
	call("PUT", "/glossary?dryRun=true", `{"term":"Hermes","translations":{},"doNotTranslate":true}`, "0000", true)
	var n int64
	require.NoError(t, st.DB.Table("i18n_glossary").Where("term='Hermes'").Count(&n).Error)
	require.Zero(t, n)
	call("PUT", "/glossary", `{"term":"Hermes","translations":{},"doNotTranslate":true}`, "0000", true)
	var gid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM i18n_glossary WHERE term='Hermes'").Scan(&gid).Error)
	call("DELETE", "/glossary/"+strconv.FormatInt(gid, 10), "", "1004", true)
	call("GET", "/agent/calls", "", "0000", true)
	call("DELETE", "/glossary/"+strconv.FormatInt(gid, 10), "", "0000", false)
	for _, path := range []string{"/content/revisions", "/content/trash"} {
		var unfiltered, filtered store.AdminPage
		require.NoError(t, json.Unmarshal(call("GET", path, "", "0000", false), &unfiltered))
		require.Positive(t, unfiltered.Total)
		require.NoError(t, json.Unmarshal(call("GET", path+"?from=2000-01-01T00:00:00Z&to=2001-01-01T00:00:00Z", "", "0000", false), &filtered))
		require.Zero(t, filtered.Total)
		require.Empty(t, filtered.Records)
	}
	call("PUT", "/agent/tokens/"+strconv.FormatInt(credential.Token.ID, 10), `{"permissions":["release:desktop:notes"]}`, "0000", false)
	call("GET", "/desktop-releases", "", "0000", true)
	call("GET", "/dashboard/overview", "", "1004", true)
	call("POST", "/desktop-releases", "{}", "1004", true)
	// After object permission is revoked, retained translation/restore permissions must not let old revisions or requests write the object.
	tokenPath := "/agent/tokens/" + strconv.FormatInt(credential.Token.ID, 10)
	t.Run("complete article management through agent gateway", func(t *testing.T) {
		call("PUT", tokenPath, `{"permissions":["content:article:edit"]}`, "0000", false)
		call("GET", "/article-authors", "", "0000", true)
		var article struct {
			ID       int64
			Revision int64
			Status   string
		}
		_, proof := fixture.evidence("article:new:agent-guide")
		articleBody, err := json.Marshal(map[string]any{"slug": "agent-guide", "sourceLocale": "en-US", "type": "tutorial", "title": "Mac application guide.", "summary": "Mac application guide.", "bodyMarkdown": "Follow these steps on your Mac.", "sources": []any{map[string]any{"title": "Vendor guide", "url": executionFixtureURL}}, "target": map[string]any{"entityKey": "topic:agent-guide", "intent": "topic"}, "claims": []any{map[string]any{"text": "Mac application guide.", "kind": "factual", "evidenceId": proof.ID, "excerpt": executionFixtureText, "appVersion": "", "region": "ZZ"}, map[string]any{"text": "Follow these steps on your Mac.", "kind": "factual", "evidenceId": proof.ID, "excerpt": executionFixtureText, "appVersion": "", "region": "ZZ"}}})
		require.NoError(t, err)
		raw := call("POST", "/articles", string(articleBody), "0000", true)
		require.NoError(t, json.Unmarshal(raw, &article))
		path := "/articles/" + strconv.FormatInt(article.ID, 10)
		change := func(method, suffix string, fields map[string]any, expected string) json.RawMessage {
			t.Helper()
			fields["expectedRevision"] = article.Revision
			body, err := json.Marshal(fields)
			require.NoError(t, err)
			return call(method, path+suffix, string(body), expected, true)
		}
		raw = change("POST", "/submit", map[string]any{}, "0000")
		require.NoError(t, json.Unmarshal(raw, &article))
		change("POST", "/publish", map[string]any{}, "1004")
		call("POST", "/article-authors", `{"slug":"agent-editor","name":"Editor","sourceLocale":"en-US"}`, "1004", true)
		call("PUT", tokenPath, `{"permissions":["content:article:edit","content:article:publish"]}`, "0000", false)
		change("POST", "/publish?dryRun=true", map[string]any{}, "0000")
		var previewState struct {
			Revision int64
			Status   string
		}
		require.NoError(t, json.Unmarshal(call("GET", path, "", "0000", true), &previewState))
		require.Equal(t, article.Revision, previewState.Revision)
		require.Equal(t, "review", previewState.Status)
		var quotaBefore struct {
			Limits struct{ LLMOpsPerDay, WritesPerDay int }
			Quota  struct{ LLMOpsRemaining, WritesRemaining int }
		}
		require.NoError(t, json.Unmarshal(call("POST", "/introspect", "", "0000", true), &quotaBefore))
		require.Positive(t, quotaBefore.Quota.WritesRemaining)
		writeKey := "agent:quota:" + strconv.FormatInt(credential.Token.ID, 10) + ":write:" + time.Now().UTC().Format("20060102")
		require.NoError(t, st.Redis.Set(ctx, writeKey, quotaBefore.Limits.WritesPerDay, time.Hour).Err())
		change("POST", "/publish", map[string]any{}, "1005")
		require.NoError(t, json.Unmarshal(call("GET", path, "", "0000", true), &previewState))
		require.Equal(t, "review", previewState.Status)
		require.Equal(t, article.Revision, previewState.Revision)
		require.NoError(t, st.Redis.Set(ctx, writeKey, quotaBefore.Limits.WritesPerDay-quotaBefore.Quota.WritesRemaining, time.Hour).Err())
		raw = change("POST", "/publish", map[string]any{}, "0000")
		require.NoError(t, json.Unmarshal(raw, &article))
		require.Equal(t, "published", article.Status)
		var quotaAfter struct {
			Quota struct{ LLMOpsRemaining, WritesRemaining int }
		}
		require.NoError(t, json.Unmarshal(call("POST", "/introspect", "", "0000", true), &quotaAfter))
		require.Equal(t, quotaBefore.Quota.LLMOpsRemaining, quotaAfter.Quota.LLMOpsRemaining)
		require.Equal(t, quotaBefore.Quota.WritesRemaining-1, quotaAfter.Quota.WritesRemaining)
		// S8 no longer creates pending rows at publication. Prepare a real, current
		// translation before exercising the Agent's locale withholding operation.
		source, err := st.ReadContentSource(ctx, content.Ref{Entity: "article", ID: publishedRevisionID(t, st, article.ID)})
		require.NoError(t, err)
		translationCtx := domain.WithOperation(ctx, &domain.Operation{AgentClientID: client.ID, AgentTokenID: credential.Token.ID})
		applied, err := st.WriteTranslationSnapshot(translationCtx, source, "zh-CN", map[string]string{
			"title": "Mac 应用程序指南。", "summary": "Mac 应用程序指南。",
			"body_markdown": "请在你的 Mac 上按照以下步骤操作。",
		}, "fixture")
		require.NoError(t, err)
		require.True(t, applied)

		call("PUT", path+"/slug", `{"expectedRevision":1,"slug":"stale-guide"}`, "1003", true)
		raw = change("PUT", "/slug", map[string]any{"slug": "renamed-agent-guide"}, "0000")
		require.NoError(t, json.Unmarshal(raw, &article))
		raw = change("PUT", "/locales/zh-CN", map[string]any{"withheld": true}, "0000")
		require.NoError(t, json.Unmarshal(raw, &article))
		raw = change("POST", "/withdraw", map[string]any{"reason": "Updating tutorial"}, "0000")
		require.NoError(t, json.Unmarshal(raw, &article))
		require.Equal(t, "archived", article.Status)
		raw = change("POST", "/restore", map[string]any{}, "0000")
		require.NoError(t, json.Unmarshal(raw, &article))
		require.Equal(t, "published", article.Status)
		change("POST", "/revert", map[string]any{"revisionNumber": 1}, "0000")
		var author struct{ ID int64 }
		raw = call("POST", "/article-authors", `{"slug":"agent-editor","name":"Editor","sourceLocale":"en-US"}`, "0000", true)
		require.NoError(t, json.Unmarshal(raw, &author))
		require.Positive(t, author.ID)
		call("PUT", "/article-authors/"+strconv.FormatInt(author.ID, 10), `{"slug":"agent-editor","name":"Updated Editor","sourceLocale":"en-US"}`, "0000", true)
	})
	call("PUT", tokenPath, `{"permissions":["catalog:package:edit","translation:review","agent:log:view","content:revision:restore"]}`, "0000", false)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,homepage) VALUES('cask','revoked-history','revoked-history','homebrew/cask','Revoked History','1','1','{}','hash','https://vendor.example.test')`).Error)
	var packageID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='revoked-history'").Scan(&packageID).Error)
	packagePath := "/packages/" + strconv.FormatInt(packageID, 10) + "/text"
	call("PUT", packagePath, `{"sourceLocale":"zh-CN","summary":"原文简介"}`, "0000", true)
	translationBody := `{"ref":{"entity":"package","id":` + strconv.FormatInt(packageID, 10) + `},"locale":"en-US","model":"fixture","fields":{"summary":"First translated summary"}}`
	call("PUT", "/translations/fix", translationBody, "0000", true)
	var firstRevision int64
	require.NoError(t, st.DB.Raw("SELECT max(id) FROM content_revisions WHERE entity='package' AND operation_id='fixTranslation'").Scan(&firstRevision).Error)
	require.Positive(t, firstRevision)
	call("PUT", "/translations/fix", strings.Replace(translationBody, "First", "Second", 1), "0000", true)
	var requestID string
	require.NoError(t, st.DB.Raw("SELECT request_id FROM content_revisions WHERE entity='package' ORDER BY id DESC LIMIT 1").Scan(&requestID).Error)
	restorePath := "/content/revisions/" + strconv.FormatInt(firstRevision, 10) + "/restore"
	call("POST", restorePath+"?dryRun=true", "{}", "0000", true)
	call("PUT", tokenPath, `{"permissions":["translation:review","agent:log:view","content:revision:restore"]}`, "0000", false)
	var historyCount, auditCount, outboxCount int64
	require.NoError(t, st.DB.Table("content_revisions").Count(&historyCount).Error)
	require.NoError(t, st.DB.Table("audit_logs").Count(&auditCount).Error)
	require.NoError(t, st.DB.Table("job_outbox").Count(&outboxCount).Error)
	call("PUT", "/translations/fix", translationBody, "1004", true)
	call("POST", restorePath, "{}", "1004", true)
	call("POST", restorePath+"?dryRun=true", "{}", "1004", true)
	call("POST", "/content/requests/"+requestID+"/revert", "", "1004", true)
	var summary string
	require.NoError(t, st.DB.Raw("SELECT summary FROM package_i18n WHERE package_id=? AND locale='en-US'", packageID).Scan(&summary).Error)
	require.Equal(t, "Second translated summary", summary)
	require.NoError(t, st.DB.Table("content_revisions").Count(&n).Error)
	require.Equal(t, historyCount, n)
	require.NoError(t, st.DB.Table("audit_logs").Count(&n).Error)
	require.Equal(t, auditCount, n)
	require.NoError(t, st.DB.Table("job_outbox").Count(&n).Error)
	require.Equal(t, outboxCount, n)
	call("PUT", tokenPath, `{"permissions":["catalog:package:edit","translation:review","agent:log:view","content:revision:restore"]}`, "0000", false)
	call("POST", restorePath, "{}", "0000", true)
	require.NoError(t, st.DB.Raw("SELECT summary FROM package_i18n WHERE package_id=? AND locale='en-US'", packageID).Scan(&summary).Error)
	require.Equal(t, "First translated summary", summary)
	// A creation revision restores only original content, without overwriting later administrator publishing or added items.
	call("PUT", tokenPath, `{"permissions":["content:collection:edit","agent:log:view","content:revision:restore"]}`, "0000", false)
	raw = call("POST", "/collections", `{"slug":"creation-history","sort":0,"sourceLocale":"zh-CN","i18n":{"zh-CN":{"title":"创建时的标题"}}}`, "0000", true)
	var collection struct{ ID int64 }
	require.NoError(t, json.Unmarshal(raw, &collection))
	collectionPath := "/collections/" + strconv.FormatInt(collection.ID, 10)
	var creation struct {
		ID, ActorID int64
		RequestID   string
	}
	require.NoError(t, st.DB.Raw("SELECT id,actor_id,request_id FROM content_revisions WHERE entity='collection' AND object_key=? AND operation_id='createCollection'", strconv.FormatInt(collection.ID, 10)).Scan(&creation).Error)
	require.Positive(t, creation.ID)
	call("PUT", collectionPath, `{"slug":"creation-history","sort":0,"unpublishAt":"2100-01-01T00:00:00Z","sourceLocale":"zh-CN","i18n":{"zh-CN":{"title":"管理员修改的标题"}}}`, "0000", false)
	call("PUT", collectionPath+"/items", `{"items":[{"packageId":`+strconv.FormatInt(packageID, 10)+`,"sourceLocale":"zh-CN","i18n":{"zh-CN":{"note":"管理员添加的推荐语"}}}]}`, "0000", false)
	call("POST", collectionPath+"/publish", `{"publishAt":"2000-01-01T00:00:00Z"}`, "0000", false)
	call("POST", collectionPath+"/unpublish", "", "1004", true)
	ref := content.Ref{Entity: "collection", ID: collection.ID}
	published, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	creationRestore := "/content/revisions/" + strconv.FormatInt(creation.ID, 10) + "/restore"
	call("POST", creationRestore+"?dryRun=true", "{}", "0000", true)
	afterPreview, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, published, afterPreview)
	call("POST", creationRestore, "{}", "0000", true)
	restored, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	for _, field := range []string{"status", "publish_at", "unpublish_at", "updated_by"} {
		require.Equal(t, published["collections"][0][field], restored["collections"][0][field])
	}
	require.Equal(t, "published", restored["collections"][0]["status"])
	require.Equal(t, published["collection_items"], restored["collection_items"])
	var title, note string
	require.NoError(t, st.DB.Raw("SELECT title FROM collection_i18n WHERE collection_id=? AND locale='zh-CN'", collection.ID).Scan(&title).Error)
	require.Equal(t, "创建时的标题", title)
	require.NoError(t, st.DB.Raw("SELECT note FROM collection_item_i18n WHERE collection_id=? AND package_id=? AND locale='zh-CN'", collection.ID, packageID).Scan(&note).Error)
	require.Equal(t, "管理员添加的推荐语", note)
	for _, agent := range []bool{false, true} {
		var filtered store.AdminPage
		require.NoError(t, json.Unmarshal(call("GET", "/content/revisions?actorId=999999", "", "0000", agent), &filtered))
		require.Zero(t, filtered.Total)
		require.Empty(t, filtered.Records)
		query := "/content/revisions?actorId=" + strconv.FormatInt(creation.ActorID, 10) + "&requestId=" + creation.RequestID
		require.NoError(t, json.Unmarshal(call("GET", query, "", "0000", agent), &filtered))
		require.EqualValues(t, 1, filtered.Total)
		require.Len(t, filtered.Records, 1)
	}
	require.NoError(t, st.DB.Exec("UPDATE agent_settings SET enabled=false").Error)
	call("POST", "/introspect", "", "8889", true)
	require.NoError(t, st.DB.Exec("UPDATE agent_settings SET enabled=true").Error)
	call("POST", "/agent/tokens/"+strconv.FormatInt(credential.Token.ID, 10)+"/revoke", "", "0000", false)
	call("POST", "/introspect", "", "7777", true)
}
