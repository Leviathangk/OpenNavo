//go:build integration

package management

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

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
	// A valid-looking body cannot approve the unrelated persisted revision.
	_, err = mutate("UpdateArticle", map[string]any{"summary": "It is safer than every other editor."})
	require.NoError(t, err)
	_, err = mutate("SubmitArticleReview", map[string]any{})
	require.NoError(t, err)
	_, err = mutate("PublishArticle", map[string]any{})
	require.Error(t, err)
	require.Nil(t, state().PublishedRevisionID)
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
	// A source citing a testing claim is still not a real test artifact.
	testText := "We tested the export task in 10 seconds."
	testEvidence := listingEvidence(t, st, "pkg:81", testText+" Version 1.0 is available in US.", "https://vendor.example.test/release", "official")
	testClaim := articles.Claim{Text: testText, Kind: "factual", EvidenceID: testEvidence.ID, Excerpt: testEvidence.Excerpt, PackageID: &pkg, AppVersion: "1.0", Region: "US"}
	nextClaims := append(append([]articles.Claim{}, claims...), testClaim)
	_, err = mutate("UpdateArticle", map[string]any{"type": "review", "bodyMarkdown": body + " " + testText, "testNote": testText, "claims": nextClaims})
	require.NoError(t, err)
	_, err = mutate("SubmitArticleReview", map[string]any{})
	require.NoError(t, err)
	_, err = mutate("PublishArticle", map[string]any{})
	require.Error(t, err)
	require.Equal(t, first, *state().PublishedRevisionID)
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
	_, err = mutate("RevertArticle", map[string]any{"revisionNumber": 1})
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
}
