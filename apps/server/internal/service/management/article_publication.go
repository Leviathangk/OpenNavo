package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
)

func articleTargetURL(base, locale, slug string) string {
	return strings.TrimRight(base, "/") + i18n.Metadata[i18n.LocaleCode(locale)].Prefix + "/articles/" + url.PathEscape(slug)
}
func (s *Service) reserveArticleTarget(ctx context.Context, st *store.Store, slug string, d store.ArticleDraft) error {
	if d.Target == nil || !d.Target.Valid() {
		return domain.Validation()
	}
	base, err := url.Parse(s.Config.WebBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return domain.Validation()
	}
	if strings.HasPrefix(d.Target.EntityKey, "pkg:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(d.Target.EntityKey, "pkg:"), 10, 64)
		if err != nil || !slices.Contains(d.Packages, id) {
			return domain.Validation()
		}
	}
	target := articleTargetURL(s.Config.WebBaseURL, d.SourceLocale, slug)
	var saved string
	err = st.DB.WithContext(ctx).Raw(`INSERT INTO seo_targets(entity_key,intent,locale,target_url) VALUES(?,?,?,?) ON CONFLICT(entity_key,intent,locale) DO UPDATE SET target_url=seo_targets.target_url RETURNING target_url`, d.Target.EntityKey, d.Target.Intent, d.SourceLocale, target).Scan(&saved).Error
	if err != nil {
		return err
	}
	if saved != target {
		return fail(domain.CodeConflict)
	}
	return nil
}
func evidenceContainsScope(body, value string) bool {
	if value == "" || value == "ZZ" {
		return true
	}
	boundary := `[^\pL\pN]`
	if regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`).MatchString(value) {
		boundary = `[^\pL\pN.]`
	}
	return regexp.MustCompile(`(?:^|` + boundary + `)` + regexp.QuoteMeta(value) + `(?:$|` + boundary + `)`).MatchString(body)
}
func loadClaimEvidence(ctx context.Context, st *store.Store, a store.Article, c articles.Claim) (agentgate.Evidence, error) {
	// Read the persisted scope, then use H2's actual freshness/revocation checks.
	// A caller cannot pick a different entity just by putting a URL in a claim.
	var scope string
	if err := st.DB.WithContext(ctx).Raw("SELECT entity_key FROM seo_evidence_snapshots WHERE id=? FOR SHARE", c.EvidenceID).Scan(&scope).Error; err != nil {
		return agentgate.Evidence{}, err
	}
	allowed := scope == fmt.Sprintf("article:%d", a.ID) || scope == "article:new:"+a.Slug
	if !allowed && strings.HasPrefix(scope, "article:new:") {
		if err := st.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM article_slug_aliases WHERE article_id=? AND slug=?)", a.ID, strings.TrimPrefix(scope, "article:new:")).Scan(&allowed).Error; err != nil {
			return agentgate.Evidence{}, err
		}
	}
	if c.PackageID != nil {
		allowed = scope == fmt.Sprintf("pkg:%d", *c.PackageID) || scope == fmt.Sprintf("package:%d", *c.PackageID)
	}
	if !allowed {
		return agentgate.Evidence{}, domain.Validation()
	}
	e, err := agentgate.LoadEvidence(ctx, st.DB, []int64{c.EvidenceID}, scope, false)
	if err != nil {
		return agentgate.Evidence{}, publicationEvidenceError(err)
	}
	return e[0], nil
}

// All publication paths validate the persisted revision, including rollback and
// restore. A request body with valid claims cannot approve different stored text.
func (s *Service) validateArticlePublication(ctx context.Context, st *store.Store, a store.Article, revisionID int64, d store.ArticleDraft) error {
	problems, _, err := articleReadiness(ctx, st, d)
	if err != nil {
		return err
	}
	if len(problems) > 0 || len(d.Claims) == 0 || !articles.ValidClaims(d.Claims) {
		return domain.Validation()
	}
	if err := checkArticleReferences(ctx, st, d); err != nil {
		return err
	}
	if err := s.reserveArticleTarget(ctx, st, a.Slug, d); err != nil {
		return err
	}
	for _, text := range []string{d.Title, ptrText(d.Summary), ptrText(d.BodyMarkdown), ptrText(d.TestNote), ptrText(d.SeoTitle), ptrText(d.SeoDescription)} {
		if !articles.ClaimsCover(text, d.Claims) {
			return domain.Validation()
		}
	}
	receipts := []articles.ClaimEvidence{}
	hasTest := false
	for _, c := range d.Claims {
		if c.PackageID != nil && !slices.Contains(d.Packages, *c.PackageID) {
			return domain.Validation()
		}
		e, err := loadClaimEvidence(ctx, st, a, c)
		if err != nil {
			return err
		}
		if !evidenceIntact(e) || !strings.Contains(e.BodyText, articles.NormalizeAssertion(c.Excerpt)) || !strings.Contains(e.Excerpt, articles.NormalizeAssertion(c.Excerpt)) || !evidenceContainsScope(c.Excerpt, c.AppVersion) || !evidenceContainsScope(c.Excerpt, c.Region) {
			return domain.Validation()
		}
		receipt := articles.ClaimEvidence{EvidenceID: e.ID, SourceURL: e.FinalURL, BodyHash: e.BodyHash, CheckedAt: e.FetchedAt, ValidUntil: e.ValidUntil}
		needsTest := d.Type == "review" || c.Kind == "test" || articles.TestingClaim(c.Text)
		if needsTest {
			if !strings.Contains(articles.NormalizeAssertion(ptrText(d.BodyMarkdown)), articles.NormalizeAssertion(c.Text)) {
				return domain.Validation()
			}
			if c.TestArtifactID == nil || c.PackageID == nil {
				return domain.Validation()
			}
			artifact, err := st.ArticleTestArtifact(ctx, *c.TestArtifactID)
			if err != nil {
				return err
			}
			var report articles.TestArtifactInput
			if artifact.ID == 0 || artifact.ArticleID != a.ID || artifact.PackageID != *c.PackageID || json.Unmarshal(artifact.Payload, &report) != nil || artifact.SHA256 != report.Hash() || !report.Valid(s.now()) || report.AppVersion != c.AppVersion || report.Region != c.Region {
				return domain.Validation()
			}
			conclusion := false
			for _, value := range report.Conclusions {
				conclusion = conclusion || articles.NormalizeAssertion(value) == articles.NormalizeAssertion(c.Text)
			}
			if !conclusion || !articles.CompleteAssertion(c.Text, report.Output) {
				return domain.Validation()
			}
			hasTest = true
			receipt.TestArtifactID = c.TestArtifactID
			receipt.TestArtifactHash = &artifact.SHA256
		} else if c.TestArtifactID != nil || !articles.CompleteAssertion(c.Text, e.BodyText) || !articles.CompleteAssertion(c.Text, c.Excerpt) {
			return domain.Validation()
		}
		receipts = append(receipts, receipt)
	}
	if d.Type == "review" && !hasTest {
		return domain.Validation()
	}
	raw, err := json.Marshal(receipts)
	if err != nil {
		return err
	}
	return st.DB.WithContext(ctx).Exec("UPDATE article_revisions SET claim_evidence=?::jsonb WHERE id=?", string(raw), revisionID).Error
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
	if actor == nil || actor.ActorID == nil || actor.AgentClientID > 0 || actor.AgentTokenID > 0 || !actor.Allowed("content:article:publish") {
		return nil, fail(domain.CodeForbidden)
	}
	var req articles.TestArtifactInput
	if decodeStrictBody(in.Body, &req) != nil || !req.Valid(s.now()) {
		return nil, domain.Validation()
	}
	return s.write(ctx, in, op, "article_artifact", func(st *store.Store) (any, error) {
		// A test can reference a previously released version, but must belong to an
		// app in this article and a real stored package version.
		draft, err := st.LoadArticleDraft(ctx, a.WorkingRevisionID)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(draft.Packages, req.PackageID) {
			return nil, domain.Validation()
		}
		var found bool
		if err := st.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL AND (version=? OR EXISTS(SELECT 1 FROM package_versions WHERE package_id=? AND version=?)))`, req.PackageID, req.AppVersion, req.PackageID, req.AppVersion).Scan(&found).Error; err != nil {
			return nil, err
		}
		if !found {
			return nil, domain.Validation()
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

// Human editors capture a source directly from an existing article, without an
// Agent lease. Scope and permission are checked again by the evidence service.
func (s *Service) captureArticleEvidence(ctx context.Context, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.ActorID == nil || op.AgentClientID > 0 || op.AgentTokenID > 0 || !op.Allowed("content:article:edit") {
		return nil, fail(domain.CodeForbidden)
	}
	var req struct {
		URL            string `json:"url"`
		Excerpt        string `json:"excerpt"`
		PackageID      *int64 `json:"packageId"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if decodeStrictBody(in.Body, &req) != nil || in.ID < 1 || req.PackageID != nil && *req.PackageID < 1 {
		return nil, domain.Validation()
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
	return result, mapTaskError(err)
}
