package management

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Publication checks actual stored references and records the editorial decision.
func (s *Service) validateArticlePublication(ctx context.Context, st *store.Store, _ store.Article, revisionID int64, d store.ArticleDraft) error {
	if err := checkArticleReferences(ctx, st, d); err != nil {
		return err
	}
	// Record the editor's publication decision so it is not repeatedly queued
	// for review. This does not synthesize factual evidence or test receipts.
	return st.DB.WithContext(ctx).Exec("UPDATE article_revisions SET editorial_reviewed_at=clock_timestamp() WHERE id=?", revisionID).Error
}

func (s *Service) articleTestArtifacts(ctx context.Context, op string, in Input) (any, error) {
	a, found, err := s.Store.LoadArticle(ctx, in.ID, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(domain.CodeNotFound)
	}
	if op == "ListArticleTestArtifacts" {
		rows, err := s.Store.ArticleTestArtifacts(ctx, in.ID)
		return map[string]any{"items": rows}, err
	}
	actor := domain.CurrentOperation(ctx)
	if err := humanEditor(actor, "content:article:publish"); err != nil {
		return nil, err
	}
	var req articles.TestArtifactInput
	if decodeStrictBody(in.Body, &req) != nil || !req.Valid(s.now()) {
		return nil, domain.Rejected("test_artifact_invalid")
	}
	return s.write(ctx, in, op, "article_artifact", func(st *store.Store) (any, error) {
		// A test can reference a previously released version, but must belong to an
		// app in this article and a real stored package version.
		draft, err := st.LoadArticleDraft(ctx, a.WorkingRevisionID)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(draft.Packages, req.PackageID) {
			return nil, domain.Rejected("package_not_in_article")
		}
		var found bool
		if err := st.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL AND (version=? OR EXISTS(SELECT 1 FROM package_versions WHERE package_id=? AND version=?)))`, req.PackageID, req.AppVersion, req.PackageID, req.AppVersion).Scan(&found).Error; err != nil {
			return nil, err
		}
		if !found {
			return nil, domain.Rejected("package_version_not_found")
		}
		result, err := st.InsertArticleTestArtifact(ctx, in.ID, *actor.ActorID, req)
		if err != nil {
			return nil, err
		}
		if err := store.Audit(ctx, st.DB, in.Actor, op, "article_artifact", fmt.Sprint(result.ID), nil, map[string]any{"articleId": in.ID, "sha256": result.SHA256, "registeredBy": result.RegisteredBy}); err != nil {
			return nil, err
		}
		return result, nil
	})
}

// humanEditor admits a signed-in editor with permission; Agent credentials never reach these operations.
func humanEditor(op *domain.Operation, permission string) error {
	if op == nil || op.ActorID == nil || op.AgentClientID > 0 || op.AgentTokenID > 0 {
		return domain.Forbidden(http.StatusConflict, "operation_not_exposed")
	}
	if !op.Allowed(permission) {
		return domain.Forbidden(http.StatusConflict, "permission_required:"+permission)
	}
	return nil
}

// Human editors capture a source directly from an existing article. Scope and
// permission are checked again by the evidence service.
func (s *Service) captureArticleEvidence(ctx context.Context, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	if err := humanEditor(op, "content:article:edit"); err != nil {
		return nil, err
	}
	var req struct {
		URL            string `json:"url"`
		Excerpt        string `json:"excerpt"`
		PackageID      *int64 `json:"packageId"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if decodeStrictBody(in.Body, &req) != nil {
		return nil, domain.Rejected("body_invalid")
	}
	if in.ID < 1 {
		return nil, domain.Rejected("parameter_invalid:id")
	}
	if req.PackageID != nil && *req.PackageID < 1 {
		return nil, domain.Rejected("field_invalid:packageId")
	}
	entity := fmt.Sprintf("article:%d", in.ID)
	if req.PackageID != nil {
		entity = fmt.Sprintf("pkg:%d", *req.PackageID)
	}
	evidence := s.Evidence
	if evidence == nil {
		evidence = agentgate.NewEvidenceService(s.Store.DB, s.Config.FetchUserAgent)
	}
	result, err := evidence.CaptureArticle(ctx, agentgate.Capture{EntityKey: entity, URL: req.URL, Excerpt: req.Excerpt, IdempotencyKey: req.IdempotencyKey, DryRun: op.DryRun}, in.ID)
	if rejected, _, ok := agentgate.Rejection(err, http.StatusBadRequest); ok {
		return nil, rejected
	}
	return result, mapWriteError(err)
}
