//go:build integration

package management

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seoopportunities"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type articleAgent struct {
	t     *testing.T
	s     *Service
	st    *store.Store
	lease seotasks.Task
	actor int64
}

func (a articleAgent) write(op string, id int64, body map[string]any, evidence ...int64) (any, error) {
	a.t.Helper()
	var expected int64
	if id > 0 {
		require.NoError(a.t, a.st.DB.Raw("SELECT revision FROM articles WHERE id=?", id).Scan(&expected).Error)
	}
	permission := "content:article:edit"
	if op == "PublishArticle" {
		permission = "content:article:publish"
	}
	operation := &domain.Operation{Name: op, RequestID: uuid.NewString(), ActorID: &a.actor, AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"content:article:edit", "content:article:publish"}, RequiredPermissions: []string{permission}, Execution: &domain.AgentExecution{TaskID: a.lease.ID, Fencing: a.lease.Fencing, IdempotencyKey: uuid.NewString(), ExpectedRevision: expected, EvidenceIDs: evidence}}
	if id > 0 {
		body["expectedRevision"] = expected
	}
	return a.s.Execute(domain.WithOperation(context.Background(), operation), op, Input{ID: id, Body: listingBody(a.t, body)})
}

func articleFixture(t *testing.T) (*store.Store, *Service, *seotasks.Service) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'article-owner','isolated-test');
 INSERT INTO article_authors(id,slug,name) VALUES(1,'navo','Navo');
 UPDATE seo_settings SET automation=jsonb_set(jsonb_set(automation,'{actions,article,mode}','"live"'),'{dailyEntities,article}','1')`).Error)
	return st, &Service{Store: st, Config: config.Config{WebBaseURL: "https://site.example.test"}}, &seotasks.Service{DB: st.DB}
}

// An agent article states its facts in its own words, cites them, and uses one
// daily article slot from creation through publication.
func TestAgentArticlePublicationWithoutBusinessQuota(t *testing.T) {
	ctx := context.Background()
	st, s, tasks := articleFixture(t)
	slug := "batch-export-mac"
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "topic", EntityKey: "article:new:" + slug, DedupeKey: "demand:fixture", RuleVersion: "fixture", Evidence: map[string]any{"targetSlug": slug}})
	require.NoError(t, err)
	leases, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"topic"})
	require.NoError(t, err)
	require.Len(t, leases, 1)
	excerpt := "Version 2.0 adds batch export. Export Kit costs $20 per year and requires macOS 14 or later."
	e := listingEvidence(t, st, "article:new:"+slug, "Release notes. "+excerpt+" Contact us.", "https://vendor.example.test/notes", "reference")
	// The agent captured it, so creation rebinds it to the new article.
	require.NoError(t, st.DB.Exec("UPDATE seo_evidence_snapshots SET created_by=? WHERE id=?", seotasks.Owner(1, 2), e.ID).Error)
	summary := "Export Kit 2.0 adds batch export for $20 a year."
	requirement := "It needs macOS 14 or later."
	claims := []articles.Claim{
		{Text: summary, Kind: "factual", EvidenceID: e.ID, Excerpt: excerpt, Region: "ZZ"},
		{Text: requirement, Kind: "factual", EvidenceID: e.ID, Excerpt: excerpt, Region: "ZZ"},
		{Text: "$20", Kind: "factual", EvidenceID: e.ID, Excerpt: excerpt, Region: "ZZ"},
	}
	body := summary + "\n\nOpen the app, choose a folder and pick the files you want. The steps below walk through the export.\n\n## Requirements\n\n" + requirement + "\n\n| Plan | Price |\n|---|---|\n| Annual | $20 |"
	agent := articleAgent{t: t, s: s, st: st, lease: leases[0], actor: 1}
	created, err := agent.write("CreateArticle", 0, map[string]any{"slug": slug, "sourceLocale": "en-US", "type": "tutorial", "authorId": 1, "title": "How to export files in batches on a Mac", "summary": summary, "bodyMarkdown": body, "seoTitle": "Batch export on a Mac", "seoDescription": summary, "sources": []articles.Source{{Title: "Release notes", URL: e.FinalURL}}, "claims": claims, "target": articles.Target{EntityKey: "topic:" + slug, Intent: "topic"}}, e.ID)
	require.NoError(t, err)
	id := created.(map[string]any)["id"].(int64)
	var slots []string
	require.NoError(t, st.DB.Raw("SELECT entity_key FROM seo_daily_entities WHERE action='article'").Scan(&slots).Error)
	require.Empty(t, slots, "direct publication does not reserve business quota")
	_, err = agent.write("SubmitArticleReview", id, map[string]any{}, e.ID)
	require.NoError(t, err, "the second write to the new article must not need another daily slot")

	a, _, err := st.LoadArticle(ctx, id, false)
	require.NoError(t, err)
	draft, err := st.LoadArticleDraft(ctx, a.WorkingRevisionID)
	require.NoError(t, err)
	wrong := append([]articles.Claim{}, draft.Claims...)
	wrong[1].Text = "It needs macOS 15 or later."
	draft.Claims = wrong
	changed := summary + "\n\n" + wrong[1].Text
	draft.BodyMarkdown = &changed
	// Claims are the editor's source annotations. Publication checks the cited
	// evidence itself, not whether the claim's wording or numbers match the excerpt.
	require.NoError(t, s.validateArticlePublication(ctx, st, a, a.WorkingRevisionID, draft))
	foreign := listingEvidence(t, st, "pkg:777", "Unrelated vendor notes. "+excerpt, "https://other.example.test/notes", "official")
	wrong[1].EvidenceID = foreign.ID
	require.NoError(t, s.validateArticlePublication(ctx, st, a, a.WorkingRevisionID, draft), "annotations are not a publication gate")

	_, err = agent.write("PublishArticle", id, map[string]any{}, e.ID)
	require.NoError(t, err)
	a, _, err = st.LoadArticle(ctx, id, false)
	require.NoError(t, err)
	require.Equal(t, "published", a.Status)
	var receipts string
	require.NoError(t, st.DB.Raw("SELECT claim_evidence::text FROM article_revisions WHERE id=?", *a.PublishedRevisionID).Scan(&receipts).Error)
	require.JSONEq(t, "[]", receipts, "direct publication does not fabricate verified receipts")
	require.NoError(t, st.DB.Raw("SELECT entity_key FROM seo_daily_entities WHERE action='article'").Scan(&slots).Error)
	require.Empty(t, slots)
	var priorities []float64
	require.NoError(t, st.DB.Raw("SELECT DISTINCT priority FROM seo_tasks WHERE kind='translate' AND entity_key=?", fmt.Sprintf("article:%d", *a.PublishedRevisionID)).Scan(&priorities).Error)
	require.Equal(t, []float64{150}, priorities, "a published article's other languages are translated first")
}

// Articles published before the claim gate get a refresh task. Republishing one
// with the Navo byline and claims keeps translations whose source is unchanged.
func TestLegacyArticleRefreshKeepsUnchangedTranslations(t *testing.T) {
	ctx := context.Background()
	st, s, tasks := articleFixture(t)
	actor := int64(1)
	admin := domain.WithOperation(ctx, &domain.Operation{ActorID: &actor, Permissions: []string{"*"}, RequestID: uuid.NewString()})
	slug := "legacy-guide"
	title := "A short guide to Export Kit"
	fact := "Export Kit 2.0 adds batch export."
	old := listingEvidence(t, st, "article:new:"+slug, fact, "https://vendor.example.test/notes", "reference")
	claim := []articles.Claim{{Text: fact, Kind: "factual", EvidenceID: old.ID, Excerpt: fact, Region: "ZZ"}}
	created, err := s.Execute(admin, "CreateArticle", Input{Body: listingBody(t, map[string]any{"slug": slug, "sourceLocale": "en-US", "type": "tutorial", "title": title, "summary": fact, "bodyMarkdown": fact, "sources": []articles.Source{{Title: "Notes", URL: old.FinalURL}}, "claims": claim, "target": articles.Target{EntityKey: "topic:" + slug, Intent: "topic"}})})
	require.NoError(t, err)
	id := created.(map[string]any)["id"].(int64)
	revision := func() int64 {
		a, _, err := st.LoadArticle(ctx, id, false)
		require.NoError(t, err)
		return a.Revision
	}
	for _, op := range []string{"SubmitArticleReview", "PublishArticle"} {
		_, err = s.Execute(admin, op, Input{ID: id, Body: map[string]any{"expectedRevision": revision()}})
		require.NoError(t, err)
	}
	a, _, err := st.LoadArticle(ctx, id, false)
	require.NoError(t, err)
	legacy := *a.PublishedRevisionID
	// Make it look like an article from before the gate, with a human translation.
	var hash string
	require.NoError(t, st.DB.Raw("SELECT source_hash FROM content_translation_sources WHERE entity='article' AND object_key=?", fmt.Sprint(legacy)).Scan(&hash).Error)
	require.NoError(t, st.DB.Exec("UPDATE article_revisions SET claim_evidence='[]'::jsonb,author_id=NULL,editorial_reviewed_at=NULL WHERE id=?", legacy).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO article_revision_i18n(revision_id,locale,title,summary,body_markdown,status,source_locale,source_hash) VALUES(?,'ja-JP','Export Kit ガイド','Export Kit 2.0 で一括書き出しに対応しました。','Export Kit 2.0 で一括書き出しに対応しました。','manual','en-US',?)`, legacy, hash).Error)

	opportunities := &seoopportunities.Service{DB: st.DB}
	_, err = opportunities.Run(ctx)
	require.NoError(t, err)
	var refresh []seotasks.Task
	require.NoError(t, st.DB.Raw("SELECT * FROM seo_tasks WHERE kind='article_refresh'").Scan(&refresh).Error)
	require.Len(t, refresh, 1)
	require.Equal(t, fmt.Sprintf("article:%d", id), refresh[0].EntityKey)
	require.Equal(t, fmt.Sprintf("article-refresh:%d:%d", id, legacy), refresh[0].DedupeKey)

	leases, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"article_refresh"})
	require.NoError(t, err)
	require.Len(t, leases, 1)
	e := listingEvidence(t, st, fmt.Sprintf("article:%d", id), "Changelog. "+fact, "https://vendor.example.test/changelog", "reference")
	agent := articleAgent{t: t, s: s, st: st, lease: leases[0], actor: 1}
	claims := []articles.Claim{{Text: fact, Kind: "factual", EvidenceID: e.ID, Excerpt: fact, Region: "ZZ"}}
	_, err = agent.write("UpdateArticle", id, map[string]any{"authorId": 1, "claims": claims}, e.ID)
	require.NoError(t, err)
	_, err = agent.write("SubmitArticleReview", id, map[string]any{}, e.ID)
	require.NoError(t, err)
	_, err = agent.write("PublishArticle", id, map[string]any{}, e.ID)
	require.NoError(t, err)
	_, err = tasks.Complete(ctx, leases[0].ID, leases[0].Fencing, seotasks.Owner(1, 2), "done", "")
	require.NoError(t, err)

	a, _, err = st.LoadArticle(ctx, id, false)
	require.NoError(t, err)
	refreshed := *a.PublishedRevisionID
	require.NotEqual(t, legacy, refreshed)
	var author int64
	require.NoError(t, st.DB.Raw("SELECT author_id FROM article_revisions WHERE id=?", refreshed).Scan(&author).Error)
	require.EqualValues(t, 1, author)
	var status string
	require.NoError(t, st.DB.Raw("SELECT status FROM article_revision_i18n WHERE revision_id=? AND locale='ja-JP'", refreshed).Scan(&status).Error)
	require.Equal(t, "manual", status, "an unchanged source keeps its human translation")
	var queued int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM seo_tasks WHERE kind='translate' AND entity_key=? AND locale='ja-JP' AND state='open'", fmt.Sprintf("article:%d", refreshed)).Scan(&queued).Error)
	require.Zero(t, queued)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM seo_tasks WHERE kind='translate' AND entity_key=? AND state='open'", fmt.Sprintf("article:%d", refreshed)).Scan(&queued).Error)
	require.Positive(t, queued, "languages without a translation are still queued")

	_, err = opportunities.Run(ctx)
	require.NoError(t, err)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM seo_tasks WHERE kind='article_refresh'").Scan(&queued).Error)
	require.EqualValues(t, 1, queued, "a republished article with receipts needs no refresh")

	// A changed source does not inherit the old translation.
	_, err = s.Execute(admin, "UpdateArticle", Input{ID: id, Body: map[string]any{"expectedRevision": revision(), "title": "A shorter guide to Export Kit"}})
	require.NoError(t, err)
	for _, op := range []string{"SubmitArticleReview", "PublishArticle"} {
		_, err = s.Execute(admin, op, Input{ID: id, Body: map[string]any{"expectedRevision": revision()}})
		require.NoError(t, err)
	}
	a, _, err = st.LoadArticle(ctx, id, false)
	require.NoError(t, err)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM article_revision_i18n WHERE revision_id=? AND locale='ja-JP'", *a.PublishedRevisionID).Scan(&queued).Error)
	require.Zero(t, queued)
}
