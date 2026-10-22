//go:build integration

package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/agentgate"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestArticlePublicationEvidenceArtifactsAndLiveRollback(t *testing.T) {
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st, Config: config.Config{WebBaseURL: "https://site.example.test"}}
	actor := int64(1)
	ctx := domain.WithOperation(context.Background(), &domain.Operation{ActorID: &actor, Permissions: []string{"*"}, RequestID: uuid.NewString()})
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'article-admin','isolated-test');
 INSERT INTO packages(id,kind,token,full_token,tap,name,homepage,version,version_base,raw,raw_hash) VALUES(81,'cask','article-app','article-app','','Article App','https://vendor.example.test','1.0','1.0','{}','')`).Error)
	title := "Article App supports macOS."
	body := title + " It exports local documents without requiring a network connection. The source documentation explains how to select a document and export the selected pages."
	e := listingEvidence(t, st, "pkg:81", body+" Version 1.0 is available in US.", "https://vendor.example.test/guide", "official")
	pkg := int64(81)
	claims := []articles.Claim{{Text: title, Kind: "factual", EvidenceID: e.ID, Excerpt: body, PackageID: &pkg, Region: "ZZ"}, {Text: body, Kind: "factual", EvidenceID: e.ID, Excerpt: body, PackageID: &pkg, Region: "ZZ"}}
	create := map[string]any{"slug": "article-gate", "sourceLocale": "en-US", "type": "news", "title": title, "summary": title, "bodyMarkdown": body, "sources": []articles.Source{{Title: "Vendor guide", URL: e.FinalURL}}, "packageIds": []int64{pkg}, "claims": claims, "target": articles.Target{EntityKey: "pkg:81", Intent: "topic"}}
	result, err := s.Execute(ctx, "CreateArticle", Input{Body: listingBody(t, create)})
	require.NoError(t, err)
	id := result.(map[string]any)["id"].(int64)
	// Another slug cannot compete for the same entity/intent/language.
	create["slug"] = "duplicate-gate"
	_, err = s.Execute(ctx, "CreateArticle", Input{Body: listingBody(t, create)})
	require.Error(t, err)
	state := func() store.Article {
		a, found, err := st.LoadArticle(ctx, id, false)
		require.NoError(t, err)
		require.True(t, found)
		return a
	}
	mutate := func(op string, fields map[string]any) (any, error) {
		fields["expectedRevision"] = state().Revision
		return s.Execute(ctx, op, Input{ID: id, Body: listingBody(t, fields)})
	}
	_, err = mutate("SubmitArticleReview", map[string]any{})
	require.NoError(t, err)
	// Editorial paraphrases do not require a matching claim for each sentence.
	_, err = mutate("UpdateArticle", map[string]any{"summary": "The guide explains the export workflow in different words."})
	require.NoError(t, err)
	_, err = mutate("SubmitArticleReview", map[string]any{})
	require.NoError(t, err)
	_, err = mutate("PublishArticle", map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, state().PublishedRevisionID)
	_, err = mutate("UpdateArticle", map[string]any{"summary": title})
	require.NoError(t, err)
	_, err = mutate("SubmitArticleReview", map[string]any{})
	require.NoError(t, err)
	_, err = mutate("PublishArticle", map[string]any{})
	require.NoError(t, err)
	first := *state().PublishedRevisionID
	var bound string
	require.NoError(t, st.DB.Raw("SELECT claim_evidence::text FROM article_revisions WHERE id=?", first).Scan(&bound).Error)
	require.Contains(t, bound, e.BodyHash)
	// Testing-related prose does not imply an administrator-attested test.
	testText := "This is not a benchmark; the vendor describes export performance."
	testEvidence := listingEvidence(t, st, "pkg:81", testText+" Version 1.0 is available in US.", "https://vendor.example.test/release", "official")
	testClaim := articles.Claim{Text: testText, Kind: "factual", EvidenceID: testEvidence.ID, Excerpt: testEvidence.Excerpt, PackageID: &pkg, AppVersion: "1.0", Region: "US"}
	nextClaims := append(append([]articles.Claim{}, claims...), testClaim)
	_, err = mutate("UpdateArticle", map[string]any{"type": "review", "bodyMarkdown": body + " " + testText, "testNote": testText, "claims": nextClaims})
	require.NoError(t, err)
	_, err = mutate("SubmitArticleReview", map[string]any{})
	require.NoError(t, err)
	_, err = mutate("PublishArticle", map[string]any{})
	require.NoError(t, err)
	require.NotEqual(t, first, *state().PublishedRevisionID)
	// Only the trusted administrator/runner channel can register actual output.
	report := articles.TestArtifactInput{PackageID: pkg, AppVersion: "1.0", MacOS: "15.0", Architecture: "arm64", Region: "US", PerformedAt: time.Now().Add(-time.Minute).UTC().Truncate(time.Second), Runner: "isolated-fixture-runner", Command: "fixture-export --count 10", Environment: map[string]string{"machine": "fixture"}, Output: body + " " + testText, Conclusions: []string{title, body, testText}, Attested: true}
	artifactInput := Input{ID: id, Body: listingBody(t, report)}
	agentCtx := domain.WithOperation(ctx, &domain.Operation{ActorID: &actor, AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"*"}})
	_, err = s.articleTestArtifacts(agentCtx, "CreateArticleTestArtifact", artifactInput)
	require.Error(t, err)
	result, err = s.Execute(ctx, "CreateArticleTestArtifact", artifactInput)
	require.NoError(t, err)
	artifact := result.(store.ArticleTestArtifact)
	again, err := s.Execute(ctx, "CreateArticleTestArtifact", artifactInput)
	require.NoError(t, err)
	require.Equal(t, artifact.ID, again.(store.ArticleTestArtifact).ID)
	require.Error(t, st.DB.Exec("UPDATE article_test_artifacts SET payload=payload || '{\"output\":\"forged\"}' WHERE id=?", artifact.ID).Error)
	testClaim.TestArtifactID = &artifact.ID
	testClaim.Kind = "test"
	nextClaims[len(nextClaims)-1] = testClaim
	for i := range nextClaims {
		nextClaims[i].TestArtifactID = &artifact.ID
		nextClaims[i].AppVersion = "1.0"
		nextClaims[i].Region = "US"
		if nextClaims[i].EvidenceID == e.ID {
			nextClaims[i].Excerpt = e.Excerpt
		}
	}

	_, err = mutate("UpdateArticle", map[string]any{"claims": nextClaims})
	require.NoError(t, err)
	_, err = mutate("SubmitArticleReview", map[string]any{})
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET automation=jsonb_set(automation,'{actions,article,mode}','"live"')`).Error)
	tasks := &seotasks.Service{DB: st.DB}
	_, err = tasks.Enqueue(ctx, seotasks.NewTask{Kind: "article", EntityKey: fmt.Sprintf("article:%d", id), DedupeKey: uuid.NewString(), RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	leases, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"article"})
	require.NoError(t, err)
	require.Len(t, leases, 1)
	taskEvidence := listingEvidence(t, st, fmt.Sprintf("article:%d", id), body+" "+testText, e.FinalURL, "reference")
	agentPublish := &domain.Operation{Name: "publishArticle", RequestID: uuid.NewString(), ActorID: &actor, AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"content:article:publish"}, RequiredPermissions: []string{"content:article:publish"}, Execution: &domain.AgentExecution{TaskID: leases[0].ID, Fencing: leases[0].Fencing, IdempotencyKey: uuid.NewString(), ExpectedRevision: state().Revision, EvidenceIDs: []int64{taskEvidence.ID}}}
	_, err = s.Execute(domain.WithOperation(ctx, agentPublish), "PublishArticle", Input{ID: id, Body: map[string]any{"expectedRevision": state().Revision}})
	require.NoError(t, err)
	_, err = tasks.Complete(ctx, leases[0].ID, leases[0].Fencing, seotasks.Owner(1, 2), "done", "")
	require.NoError(t, err)
	second := *state().PublishedRevisionID
	require.NotEqual(t, first, second)
	require.NoError(t, st.DB.Raw("SELECT claim_evidence::text FROM article_revisions WHERE id=?", second).Scan(&bound).Error)
	require.Contains(t, bound, artifact.SHA256)
	// Revert moves the actual online pointer and emits page invalidations.
	var before int64
	require.NoError(t, st.DB.Table("content_url_changes").Count(&before).Error)
	_, err = mutate("RevertArticle", map[string]any{"revisionNumber": 2})
	require.NoError(t, err)
	require.Equal(t, first, *state().PublishedRevisionID)
	require.Equal(t, "published", state().Status)
	var after int64
	require.NoError(t, st.DB.Table("content_url_changes").Count(&after).Error)
	require.Greater(t, after, before)
	// Restoring a withdrawn page revalidates revocation; failure preserves the withdrawal.
	_, err = mutate("WithdrawArticle", map[string]any{})
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE seo_evidence_snapshots SET revoked_at=now() WHERE id=?", e.ID).Error)
	_, err = mutate("RestoreArticle", map[string]any{})
	require.Error(t, err)
	require.Equal(t, "archived", state().Status)
	require.NoError(t, st.DB.Exec("UPDATE seo_evidence_snapshots SET revoked_at=NULL WHERE id=?", e.ID).Error)
	_, err = mutate("RestoreArticle", map[string]any{})
	require.NoError(t, err)
	// Tampering and cross-article artifact reuse cannot be blessed by arbitrary claims.
	draft, err := st.LoadArticleDraft(ctx, second)
	require.NoError(t, err)
	original := draft.Claims[len(draft.Claims)-1].AppVersion
	draft.Claims[len(draft.Claims)-1].AppVersion = "2.0"
	require.Error(t, s.validateArticlePublication(ctx, st, state(), second, draft))
	draft.Claims[len(draft.Claims)-1].AppVersion = original
	other := state()
	other.ID++
	require.Error(t, s.validateArticlePublication(ctx, st, other, second, draft))
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	// The editor can publish without a mechanical per-sentence claim inventory.
	draft.Claims = nil
	disclaimer := "这不是实测，依据官方文档整理。快捷键为 Ctrl - Shift - C。"
	draft.BodyMarkdown = &disclaimer
	require.NoError(t, s.validateArticlePublication(ctx, st, state(), second, draft))

}

// A human can start with no evidence/report IDs and complete the entire review
// workflow through management operations, without an Agent task or seeded evidence.
func TestHumanArticleCaptureRegisterAndPublish(t *testing.T) {
	st := testutil.NewSEOStore(t)
	actor := int64(1)
	ctx := domain.WithOperation(context.Background(), &domain.Operation{ActorID: &actor, Permissions: []string{"content:article:edit", "content:article:publish"}, RequestID: uuid.NewString()})
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'human-editor','test');
 INSERT INTO packages(id,kind,token,full_token,tap,name,homepage,version,version_base,raw,raw_hash) VALUES(81,'cask','human-app','human-app','','Article App','https://vendor.example.test','1.0','1.0','{}','')`).Error)
	title := "We tested Article App export."
	body := title + " Version 1.0 exports the selected local document. The test used a local fixture and verified the exported file against its original contents without requiring a network connection."
	s := &Service{Store: st, Config: config.Config{WebBaseURL: "https://site.example.test"}}
	s.Evidence = agentgate.NewEvidenceService(st.DB, "test")
	s.Evidence.HTTP = &http.Client{Transport: articleEvidenceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	created, err := s.Execute(ctx, "CreateArticle", Input{Body: listingBody(t, map[string]any{"slug": "human-publish", "sourceLocale": "en-US", "type": "review", "title": title, "summary": title, "bodyMarkdown": body, "testNote": title, "packageIds": []int64{81}})})
	require.NoError(t, err)
	id := created.(map[string]any)["id"].(int64)
	capture := Input{ID: id, Body: map[string]any{"url": "https://vendor.example.test/test", "excerpt": body, "packageId": float64(81), "idempotencyKey": uuid.NewString()}}
	captured, err := s.Execute(ctx, "CaptureArticleEvidence", capture)
	require.NoError(t, err)
	evidence := captured.(agentgate.Evidence)
	require.Positive(t, evidence.ID)
	again, err := s.Execute(ctx, "CaptureArticleEvidence", capture)
	require.NoError(t, err)
	require.Equal(t, evidence.ID, again.(agentgate.Evidence).ID)
	denied := domain.WithOperation(context.Background(), &domain.Operation{ActorID: &actor, Permissions: []string{"content:article:publish"}})
	_, err = s.Execute(denied, "CaptureArticleEvidence", capture)
	require.Error(t, err)
	agent := domain.WithOperation(context.Background(), &domain.Operation{ActorID: &actor, AgentClientID: 1, AgentTokenID: 1, Permissions: []string{"*"}})
	_, err = s.Execute(agent, "CaptureArticleEvidence", capture)
	require.Error(t, err)
	for _, bad := range []map[string]any{
		{"url": "https://127.0.0.1/private", "excerpt": body, "idempotencyKey": uuid.NewString()},
		{"url": "https://vendor.example.test/test", "excerpt": body, "packageId": float64(999), "idempotencyKey": uuid.NewString()},
		{"url": "https://vendor.example.test/test", "excerpt": "Invented assertion", "idempotencyKey": uuid.NewString()},
	} {
		_, err = s.Execute(ctx, "CaptureArticleEvidence", Input{ID: id, Body: bad})
		require.Error(t, err)
	}
	report := articles.TestArtifactInput{PackageID: 81, AppVersion: "1.0", MacOS: "15.0", Architecture: "arm64", Region: "ZZ", PerformedAt: time.Now().Add(-time.Minute).UTC(), Runner: "Human tester", Command: "Export fixture and compare output", Environment: map[string]string{"machine": "test Mac"}, Output: body, Conclusions: []string{title, body}, Attested: true}
	registered, err := s.Execute(ctx, "CreateArticleTestArtifact", Input{ID: id, Body: listingBody(t, report)})
	require.NoError(t, err)
	artifact := registered.(store.ArticleTestArtifact)
	pkg := int64(81)
	claims := []articles.Claim{}
	for _, statement := range []string{title, body} {
		claims = append(claims, articles.Claim{Text: statement, Kind: "test", EvidenceID: evidence.ID, Excerpt: body, PackageID: &pkg, AppVersion: "1.0", Region: "ZZ", TestArtifactID: &artifact.ID})
	}
	mutate := func(op string, fields map[string]any) {
		a, found, err := st.LoadArticle(ctx, id, false)
		require.NoError(t, err)
		require.True(t, found)
		fields["expectedRevision"] = a.Revision
		_, err = s.Execute(ctx, op, Input{ID: id, Body: listingBody(t, fields)})
		require.NoError(t, err)
	}
	mutate("UpdateArticle", map[string]any{"claims": claims, "target": articles.Target{EntityKey: "pkg:81", Intent: "topic"}})
	mutate("SubmitArticleReview", map[string]any{})
	mutate("PublishArticle", map[string]any{})
	a, found, err := st.LoadArticle(ctx, id, false)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "published", a.Status)
	require.NotNil(t, a.PublishedRevisionID)
}

type articleEvidenceTransport func(*http.Request) (*http.Response, error)

func (f articleEvidenceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestArticlePackageReplacementIsAtomicAtLimit(t *testing.T) {
	st := testutil.NewSEOStore(t)
	actor := int64(1)
	ctx := domain.WithOperation(context.Background(), &domain.Operation{ActorID: &actor, Permissions: []string{"*"}, RequestID: uuid.NewString()})
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'replace-editor','test');
 INSERT INTO packages(id,kind,token,full_token,tap,name,homepage,version,version_base,raw,raw_hash)
 SELECT n,'cask','replace-'||n,'replace-'||n,'','App '||n,'https://vendor.example.test','1','1','{}','' FROM generate_series(1,51) n`).Error)
	s := &Service{Store: st, Config: config.Config{WebBaseURL: "https://site.example.test"}}
	oldIDs, newIDs := []int64{}, []int64{}
	for id := int64(1); id <= 50; id++ {
		oldIDs = append(oldIDs, id)
		newIDs = append(newIDs, id+1)
	}
	created, err := s.Execute(ctx, "CreateArticle", Input{Body: listingBody(t, map[string]any{
		"slug": "replace-at-limit", "sourceLocale": "en-US", "type": "roundup", "title": "Apps", "packageIds": oldIDs,
		"target": map[string]any{"entityKey": "pkg:1", "intent": "download"},
	})})
	require.NoError(t, err)
	id := created.(map[string]any)["id"].(int64)
	read := func() (store.Article, store.ArticleDraft) {
		a, found, err := st.LoadArticle(ctx, id, false)
		require.NoError(t, err)
		require.True(t, found)
		d, err := st.LoadArticleDraft(ctx, a.WorkingRevisionID)
		require.NoError(t, err)
		return a, d
	}
	a, _ := read()
	update := func(revision int64, ids []int64, target any) error {
		_, err := s.Execute(ctx, "UpdateArticle", Input{ID: id, Body: listingBody(t, map[string]any{
			"expectedRevision": revision, "packageIds": ids, "target": target,
		})})
		return err
	}
	// A failed target check must not partially unlink any apps.
	require.Error(t, update(a.Revision, newIDs, map[string]any{"entityKey": "pkg:1", "intent": "download"}))
	after, draft := read()
	require.Equal(t, a.Revision, after.Revision)
	require.Equal(t, oldIDs, draft.Packages)
	// The server still enforces the 50-app ceiling.
	require.Error(t, update(a.Revision, append(append([]int64{}, oldIDs...), 51), nil))
	require.NoError(t, update(a.Revision, newIDs, map[string]any{"entityKey": "pkg:51", "intent": "download"}))
	after, draft = read()
	require.Equal(t, newIDs, draft.Packages)
	require.Equal(t, "pkg:51", draft.Target.EntityKey)
	require.Equal(t, a.Revision+1, after.Revision)
	require.Error(t, update(a.Revision, oldIDs, nil)) // stale edits cannot overwrite the replacement
	// Evidence preparation can atomically unlink an old target without saving incomplete claims.
	require.NoError(t, update(after.Revision, oldIDs, nil))
	_, draft = read()
	require.Equal(t, oldIDs, draft.Packages)
	require.Nil(t, draft.Target)
}
