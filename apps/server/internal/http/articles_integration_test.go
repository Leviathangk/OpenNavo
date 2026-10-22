//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestArticleWorkflowRevisionsTranslationsAndPublicReads(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "article_tester", hash))
	mutations := []store.CatalogMutation{}
	for _, raw := range []string{
		`{"token":"shottr","name":["Shottr"],"version":"1.9","desc":"Screenshot tool"}`,
		`{"token":"cleanshot","name":["CleanShot X"],"version":"4.7","desc":"Screenshot and recording tool"}`,
	} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: p})
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutations))
	var shottr, cleanshot int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='shottr'").Scan(&shottr).Error)
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='cleanshot'").Scan(&cleanshot).Error)
	asset := "https://cdn.opennavo.example/screenshots/uploads/cover/capture.png"
	require.NoError(t, st.DB.Exec(`INSERT INTO assets(kind,storage_key,url,mime,width,height,bytes,sha256) VALUES('cover','articles/capture.png',?,'image/png',1280,800,1,'hash')`, asset).Error)

	cfg := config.Config{AppEnv: "dev", JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, WebBaseURL: "https://site.example.test"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "article_tester", "isolated-test-password", store.AuditActor{})
	require.NoError(t, err)
	// Apply published invalidations at once (the API coalesces them over ten seconds).
	subscription := st.Redis.Subscribe(ctx, "cache:invalidate")
	_, err = subscription.Receive(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Close() })
	go func() {
		for message := range subscription.Channel() {
			var payload struct{ Patterns []string }
			if json.Unmarshal([]byte(message.Payload), &payload) == nil {
				_ = cache.Invalidate(context.Background(), st.Redis, payload.Patterns)
			}
		}
	}()
	app, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{
		Admin:  &admin.Handler{Auth: authentication, Management: &management.Service{Store: st, Config: cfg}},
		Public: &public.Handler{Catalog: &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, WebBaseURL: cfg.WebBaseURL, Secrets: cfg}},
	})
	require.NoError(t, err)
	adminSpec, err := adminapi.GetSpec()
	require.NoError(t, err)
	publicSpec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path string, body any, code string) map[string]any {
		t.Helper()
		var raw io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			raw = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, "/admin-api"+path, raw)
		req.Header.Set("Authorization", "Bearer "+pair.Token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var envelope struct {
			Code string         `json:"code"`
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		require.Equal(t, code, envelope.Code, rec.Body.String())
		testutil.ValidateResponse(t, adminSpec, "/admin-api", req, rec)
		return envelope.Data
	}
	read := func(path string, status int, target any) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1"+path, nil)
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		testutil.ValidateResponse(t, publicSpec, "/api/v1", req, rec)
		if target != nil {
			var envelope struct{ Data json.RawMessage }
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, target))
		}
	}
	lookup := func(slug, locale string) publicapi.ArticleLookup {
		t.Helper()
		var out publicapi.ArticleLookup
		read("/articles/"+slug+"?locale="+locale, 200, &out)
		return out
	}
	revision := func(view map[string]any) int64 { return int64(view["revision"].(float64)) }
	// Isolated server-captured evidence for this workflow, never a real vendor fetch.
	const sourceURL = "https://vendor.example.test/screenshot-guide"
	claimsFor := func(slug string, assertions ...string) []articles.Claim {
		t.Helper()
		sentences := make([]string, len(assertions))
		for i, assertion := range assertions {
			sentences[i] = strings.TrimRight(assertion, ".!?") + "."
		}
		body := strings.Join(sentences, " ")
		evidence := agentgate.Evidence{EntityKey: "article:new:" + slug, SourceURL: sourceURL, FinalURL: sourceURL,
			BodyText: body, Excerpt: body, BodyHash: seotasks.Hash([]byte(body)), FetchedAt: time.Now().UTC(), ValidUntil: time.Now().Add(time.Hour),
			Authority: "official", FetchStatus: 200, CreatedBy: "fixture", CaptureKey: "article-workflow-" + slug, RequestHash: seotasks.Hash([]byte(slug))}
		require.NoError(t, st.DB.Create(&evidence).Error)
		claims := make([]articles.Claim, len(assertions))
		for i, assertion := range assertions {
			claims[i] = articles.Claim{Text: assertion, Kind: "factual", EvidenceID: evidence.ID, Excerpt: body, Region: "ZZ"}
		}
		return claims
	}

	// Drafts: idempotent creation, unique slugs, problems listed before review.
	draft := map[string]any{"slug": "choose-a-screenshot-app", "sourceLocale": "en-US", "type": "roundup", "title": "How to choose a screenshot app", "idempotencyKey": "draft-1"}
	created := call("POST", "/articles", draft, "0000")
	id := int64(created["id"].(float64))
	again := call("POST", "/articles", draft, "0000")
	require.Equal(t, created["id"], again["id"], "the idempotency key returns the same article")
	call("POST", "/articles", map[string]any{"slug": "choose-a-screenshot-app", "sourceLocale": "en-US", "type": "news", "title": "Other"}, "1003")
	require.Equal(t, "draft", created["status"])
	require.ElementsMatch(t, []any{"summary", "body", "sources"}, created["readiness"])
	base := "/articles/" + strconv.FormatInt(id, 10)

	// Concurrent edits: a stale expectedRevision is rejected.
	call("PUT", base, map[string]any{"expectedRevision": revision(created) + 1, "summary": "Lost update"}, "1003")
	edited := call("PUT", base, map[string]any{"expectedRevision": revision(created), "summary": "What to compare.",
		"bodyMarkdown": "## Pick by task\n\n![Annotated capture](https://elsewhere.example/a.png)\n\n::app[cleanshot]",
		"sources":      []any{map[string]any{"title": "Fixture screenshot guide", "url": sourceURL}}}, "0000")
	require.Greater(t, revision(edited), revision(created))
	require.Equal(t, []any{"image_host"}, edited["readiness"], "readiness reports external image hosts")
	edited = call("POST", base+"/submit", map[string]any{"expectedRevision": revision(edited)}, "0000")
	require.Equal(t, "review", edited["status"], "readiness is advisory and does not block submission")
	require.Equal(t, []any{"image_host"}, edited["readiness"])

	// A dry run previews without writing.
	preview := call("PUT", base+"?dryRun=true", map[string]any{"expectedRevision": revision(edited), "title": "Preview only"}, "0000")
	require.Equal(t, true, preview["dryRun"])
	unchanged := call("GET", base, nil, "0000")
	require.Equal(t, "How to choose a screenshot app", unchanged["working"].(map[string]any)["title"])

	edited = call("PUT", base, map[string]any{"expectedRevision": revision(edited), "bodyMarkdown": "## Pick by task\n\n![Annotated capture](" + asset + ")\n\n::app[cleanshot]"}, "0000")
	edited = call("PUT", base+"/packages", map[string]any{"expectedRevision": revision(edited), "packageIds": []any{shottr}}, "0000")
	require.Empty(t, edited["readiness"])
	submitted := call("POST", base+"/submit", map[string]any{"expectedRevision": revision(edited)}, "0000")
	require.Equal(t, "review", submitted["status"])

	// Editing after submission withdraws the review.
	reworded := call("PUT", base, map[string]any{"expectedRevision": revision(submitted), "summary": "What to compare when choosing."}, "0000")
	require.Equal(t, "draft", reworded["status"])
	require.Equal(t, "review_invalidated", reworded["events"].([]any)[1].(map[string]any)["action"])

	// Drafts are not public, and draft edits queue no page refreshes.
	read("/articles/choose-a-screenshot-app", 404, nil)
	var queued int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_url_changes").Scan(&queued).Error)
	require.Zero(t, queued)

	// Supplied stale revisions still reject publication without changing public state.
	call("POST", base+"/publish", map[string]any{"expectedRevision": revision(reworded) - 1}, "1003")
	rejected := call("GET", base, nil, "0000")
	require.Equal(t, revision(reworded), revision(rejected))
	require.Equal(t, "draft", rejected["status"])
	require.Nil(t, rejected["published"])
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_url_changes").Scan(&queued).Error)
	require.Zero(t, queued, "rejected publication cannot refresh public pages")
	// Publish directly after editing, without resubmission, claims or a target.
	published := call("POST", base+"/publish", map[string]any{"expectedRevision": revision(reworded)}, "0000")
	require.Empty(t, published["working"].(map[string]any)["claimEvidence"], "publication does not create evidence receipts")
	firstPublishedRevision := publishedRevisionID(t, st, id)
	require.Equal(t, "published", published["status"])
	require.NotNil(t, published["firstPublishedAt"])
	require.Equal(t, published["firstPublishedAt"], published["contentUpdatedAt"])
	require.Equal(t, false, published["hasPendingChanges"])
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_url_changes WHERE '/articles/choose-a-screenshot-app'=ANY(paths) AND '/apps/shottr'=ANY(paths) AND announce").Scan(&queued).Error)
	require.Equal(t, 1, queued, "the article, the list and the pages of its apps")
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_url_changes WHERE paths='{/articles/feed.xml}' AND NOT announce").Scan(&queued).Error)
	require.Equal(t, 1, queued, "feeds are refreshed but not announced")

	// Public: source text until a language is ready; apps as the catalog shows them now.
	page := lookup("choose-a-screenshot-app", "zh-CN")
	require.Equal(t, publicapi.Published, page.State)
	article := page.Article
	require.Equal(t, publicapi.Locale("en-US"), article.Locale)
	require.Equal(t, []publicapi.Locale{"en-US"}, article.AvailableLocales)
	require.Equal(t, []string{"shottr", "cleanshot"}, tokens(article.Packages))
	require.Equal(t, asset, article.Images[0].Url)
	require.Equal(t, 1280, *article.Images[0].Width)
	var list publicapi.ArticlePage
	read("/articles?locale=en-US", 200, &list)
	require.Equal(t, 1, list.Total)
	var detail publicapi.PackageDetail
	read("/packages/cask/shottr?locale=en-US", 200, &detail)
	require.Equal(t, "choose-a-screenshot-app", (*detail.Articles)[0].Slug)

	// A Chinese translation becomes ready for this revision only.
	ref := content.Ref{Entity: "article", ID: publishedRevisionID(t, st, id)}
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.Contains(t, source.Targets, "zh-CN")
	// At the store boundary, model attribution comes from the authenticated writer context.
	translationCtx := domain.WithOperation(ctx, &domain.Operation{AgentClientID: 1, AgentTokenID: 1})
	applied, err := st.WriteTranslationSnapshot(translationCtx, source, "zh-CN", map[string]string{
		"title": "如何选择截图工具", "summary": "选择时应比较的事项。",
		"body_markdown": "## 根据任务选择\n\n![标注截图](" + asset + ")\n\n::app[cleanshot]",
	}, "fixture-translator")
	require.NoError(t, err)
	require.True(t, applied)
	// The worker dispatches queued cache invalidations; the language's page is queued for the URL feed.
	require.NoError(t, st.DispatchCacheInvalidations(ctx))
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_url_changes WHERE '/articles/choose-a-screenshot-app'=ANY(paths)").Scan(&queued).Error)
	require.Equal(t, 2, queued)
	require.Eventually(t, func() bool { return lookup("choose-a-screenshot-app", "zh-CN").Article.Locale == "zh-CN" }, 5*time.Second, 50*time.Millisecond)
	zh := lookup("choose-a-screenshot-app", "zh-CN").Article
	require.Equal(t, "如何选择截图工具", zh.Title)
	require.True(t, zh.MachineTranslated)
	require.ElementsMatch(t, []publicapi.Locale{"en-US", "zh-CN"}, zh.AvailableLocales)

	// Holding a language back hides it again.
	current := call("GET", base, nil, "0000")
	require.Equal(t, revision(published), revision(current), "translations do not count as edits")
	held := call("PUT", base+"/locales/zh-CN", map[string]any{"expectedRevision": revision(current), "withheld": true}, "0000")
	require.Eventually(t, func() bool { return lookup("choose-a-screenshot-app", "zh-CN").Article.Locale == "en-US" }, 5*time.Second, 50*time.Millisecond)
	released := call("PUT", base+"/locales/zh-CN", map[string]any{"expectedRevision": revision(held), "withheld": false}, "0000")

	// Editing the published article forks a draft; the live page does not change.
	forked := call("PUT", base, map[string]any{"expectedRevision": revision(released), "seoTitle": "Screenshot apps compared"}, "0000")
	require.Equal(t, true, forked["hasPendingChanges"])
	require.EqualValues(t, 2, forked["working"].(map[string]any)["number"].(float64), "revision 1 is live, 2 is the new draft")
	require.Nil(t, lookup("choose-a-screenshot-app", "en-US").Article.SeoTitle)
	forked = call("POST", base+"/submit", map[string]any{"expectedRevision": revision(forked)}, "0000")
	republished := call("POST", base+"/publish", map[string]any{"expectedRevision": revision(forked)}, "0000")
	require.Equal(t, published["contentUpdatedAt"], republished["contentUpdatedAt"], "a search snippet change is not a content update")

	// A late translation for the previous revision changes nothing.
	applied, err = st.WriteTranslationSnapshot(ctx, source, "ja-JP", map[string]string{"title": "スクリーンショットアプリの選び方"}, "fake")
	require.NoError(t, err)
	require.False(t, applied)
	require.Eventually(t, func() bool {
		return lookup("choose-a-screenshot-app", "zh-CN").Article.Locale == "en-US"
	}, 5*time.Second, 50*time.Millisecond, "the new revision waits for its own translation")

	// Renamed slugs redirect; withdrawn articles answer as withdrawn and leave lists.
	current = call("GET", base, nil, "0000")
	moved := call("PUT", base+"/slug", map[string]any{"expectedRevision": revision(current), "slug": "screenshot-apps-for-mac"}, "0000")
	require.Equal(t, []any{"choose-a-screenshot-app"}, moved["aliases"])
	require.Eventually(t, func() bool {
		old := lookup("choose-a-screenshot-app", "en-US")
		return old.State == "moved" && old.Slug == "screenshot-apps-for-mac"
	}, 5*time.Second, 50*time.Millisecond)
	withdrawn := call("POST", base+"/withdraw", map[string]any{"expectedRevision": revision(moved), "reason": "Prices changed"}, "0000")
	require.Equal(t, "archived", withdrawn["status"])
	require.Eventually(t, func() bool { return lookup("screenshot-apps-for-mac", "en-US").State == "withdrawn" }, 5*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		read("/articles?locale=en-US", 200, &list)
		return list.Total == 0
	}, 5*time.Second, 50*time.Millisecond)
	var sitemap publicapi.ArticleSitemap
	read("/sitemap/articles", 200, &sitemap)
	require.Empty(t, sitemap.Articles)
	restored := call("POST", base+"/restore", map[string]any{"expectedRevision": revision(withdrawn)}, "0000")
	require.Equal(t, "published", restored["status"])
	require.Eventually(t, func() bool { return lookup("screenshot-apps-for-mac", "en-US").State == "published" }, 5*time.Second, 50*time.Millisecond)

	// Related apps follow renames by ID.
	var row store.CatalogRow
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	row = *rows["shottr"]
	renamed, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"shottr-app","name":["Shottr"],"version":"2.0","desc":"Screenshot tool","old_tokens":["shottr"]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: renamed, Previous: &row, RenamedFrom: "shottr"}}))
	require.NoError(t, cache.PublishInvalidation(ctx, st.Redis, "c:*"))
	require.Eventually(t, func() bool {
		return tokens(lookup("screenshot-apps-for-mac", "en-US").Article.Packages)[0] == "shottr-app"
	}, 5*time.Second, 50*time.Millisecond)

	// Reverting a published revision restores the live pointer and clears pending changes.
	reverted := call("POST", base+"/revert", map[string]any{"expectedRevision": revision(restored), "revisionNumber": 1}, "0000")
	require.Equal(t, "How to choose a screenshot app", reverted["working"].(map[string]any)["title"])
	require.EqualValues(t, 1, reverted["working"].(map[string]any)["number"].(float64))
	require.EqualValues(t, 1, reverted["published"].(map[string]any)["number"])
	require.Equal(t, "published", reverted["status"])
	require.Equal(t, false, reverted["hasPendingChanges"])
	require.Equal(t, firstPublishedRevision, publishedRevisionID(t, st, id))
	require.Eventually(t, func() bool {
		return lookup("screenshot-apps-for-mac", "en-US").Article.SeoTitle == nil
	}, 5*time.Second, 50*time.Millisecond, "the restored revision reaches the public cache")
	require.Equal(t, "revert", reverted["events"].([]any)[0].(map[string]any)["action"])

	// Authors: profile page and byline.
	author := call("POST", "/article-authors", map[string]any{"slug": "lin-wei", "name": "Lin Wei", "sourceLocale": "en-US", "role": "Editor", "bio": "Tests Mac apps."}, "0000")
	authorID := author["id"]
	call("PUT", base, map[string]any{"expectedRevision": revision(reverted), "authorId": authorID, "title": "How to choose a screenshot app", "summary": "What to compare."}, "0000")
	current = call("GET", base, nil, "0000")
	current = call("POST", base+"/submit", map[string]any{"expectedRevision": revision(current)}, "0000")
	call("POST", base+"/publish", map[string]any{"expectedRevision": revision(current)}, "0000")
	var profile publicapi.AuthorPage
	require.Eventually(t, func() bool {
		read("/authors/lin-wei?locale=en-US", 200, &profile)
		return profile.Articles.Total == 1
	}, 5*time.Second, 50*time.Millisecond)
	require.Equal(t, "Editor", *profile.Author.Role)
	require.Equal(t, "lin-wei", lookup("screenshot-apps-for-mac", "en-US").Article.Author.Slug)
	read("/sitemap/articles", 200, &sitemap)
	require.Equal(t, "lin-wei", sitemap.Authors[0].Slug)

	// Related articles: another published article covering the same app.
	require.Empty(t, lookup("screenshot-apps-for-mac", "en-US").Article.Related)
	second := call("POST", "/articles", map[string]any{"slug": "annotate-screenshots", "sourceLocale": "en-US", "type": "tutorial", "title": "Annotate screenshots",
		"summary": "Arrows and blur.", "bodyMarkdown": "## Steps", "sources": []any{map[string]any{"title": "Fixture screenshot guide", "url": sourceURL}}, "packageIds": []any{shottr},
		"claims": claimsFor("annotate-screenshots", "Annotate screenshots", "Arrows and blur.", "Steps"),
		"target": articles.Target{EntityKey: "pkg:" + strconv.FormatInt(shottr, 10), Intent: "topic"}}, "0000")
	secondBase := "/articles/" + strconv.FormatInt(int64(second["id"].(float64)), 10)
	second = call("POST", secondBase+"/submit", map[string]any{"expectedRevision": revision(second)}, "0000")
	require.Equal(t, "review", second["status"])
	second = call("POST", secondBase+"/publish", map[string]any{}, "0000")
	require.Empty(t, second["working"].(map[string]any)["claimEvidence"], "even supplied claims do not fabricate receipts")
	require.Eventually(t, func() bool {
		related := lookup("screenshot-apps-for-mac", "en-US").Article.Related
		return len(related) == 1 && related[0].Slug == "annotate-screenshots"
	}, 5*time.Second, 50*time.Millisecond)
	require.Equal(t, "screenshot-apps-for-mac", lookup("annotate-screenshots", "en-US").Article.Related[0].Slug)
	require.Eventually(t, func() bool {
		read("/packages/cask/shottr-app?locale=en-US", 200, &detail)
		return len(*detail.Articles) == 2
	}, 5*time.Second, 50*time.Millisecond, "the app page lists both")
}

func tokens(items []publicapi.PackageSummary) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Token)
	}
	return out
}

func publishedRevisionID(t *testing.T, st *store.Store, id int64) int64 {
	t.Helper()
	var revision int64
	require.NoError(t, st.DB.Raw("SELECT published_revision_id FROM articles WHERE id=?", id).Scan(&revision).Error)
	return revision
}
