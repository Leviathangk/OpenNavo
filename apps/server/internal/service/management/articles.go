package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/searchtext"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// Article writes (WP14). Drafts are edited in place until submitted; editing a published article
// forks a new working revision, so nothing reaches the site until someone with publish permission
// publishes it. A supplied expectedRevision checks the article's change counter.

func articleFields(body map[string]any) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, domain.Validation()
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(data, &out) != nil {
		return nil, domain.Validation()
	}
	return out, nil
}

// patchArticleDraft applies present fields to the draft; null clears optional fields.
func patchArticleDraft(d store.ArticleDraft, fields map[string]json.RawMessage, allowed ...string) (store.ArticleDraft, error) {
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return d, domain.Validation()
		}
	}
	text := func(key string, limit int, target **string) error {
		raw, ok := fields[key]
		if !ok {
			return nil
		}
		var value *string
		if json.Unmarshal(raw, &value) != nil {
			return domain.Validation()
		}
		out, err := articles.Text(value, limit)
		if err != nil {
			return domain.Validation()
		}
		*target = out
		return nil
	}
	id := func(key string, target **int64) error {
		raw, ok := fields[key]
		if !ok {
			return nil
		}
		var value *int64
		if json.Unmarshal(raw, &value) != nil || value != nil && *value < 1 {
			return domain.Validation()
		}
		*target = value
		return nil
	}
	if raw, ok := fields["claims"]; ok {
		if json.Unmarshal(raw, &d.Claims) != nil || !articles.ValidClaims(d.Claims) {
			return d, domain.Validation()
		}
	}
	if raw, ok := fields["target"]; ok {
		if json.Unmarshal(raw, &d.Target) != nil || d.Target != nil && !d.Target.Valid() {
			return d, domain.Validation()
		}
	}
	if raw, ok := fields["sourceLocale"]; ok {
		var locale string
		if json.Unmarshal(raw, &locale) != nil || !i18n.Valid(locale) {
			return d, domain.Validation()
		}
		d.SourceLocale = locale
	}
	if raw, ok := fields["type"]; ok {
		var kind string
		if json.Unmarshal(raw, &kind) != nil || !articles.ValidType(kind) {
			return d, domain.Validation()
		}
		d.Type = kind
	}
	if raw, ok := fields["title"]; ok {
		var title *string
		if json.Unmarshal(raw, &title) != nil {
			return d, domain.Validation()
		}
		out, err := articles.Text(title, 200)
		if err != nil || out == nil {
			return d, domain.Validation()
		}
		d.Title = *out
	}
	for _, step := range []error{text("summary", 400, &d.Summary), text("testNote", 1000, &d.TestNote), text("seoTitle", 120, &d.SeoTitle),
		text("seoDescription", 300, &d.SeoDescription), text("coverAlt", 300, &d.CoverAlt), id("authorId", &d.AuthorID), id("coverAssetId", &d.CoverAssetID)} {
		if step != nil {
			return d, step
		}
	}
	if raw, ok := fields["bodyMarkdown"]; ok {
		var body *string
		if json.Unmarshal(raw, &body) != nil {
			return d, domain.Validation()
		}
		out, err := articles.Body(body)
		if err != nil {
			return d, domain.Validation()
		}
		d.BodyMarkdown = out
	}
	if raw, ok := fields["sources"]; ok {
		var sources []articles.Source
		if json.Unmarshal(raw, &sources) != nil {
			return d, domain.Validation()
		}
		out, err := articles.Sources(sources)
		if err != nil {
			return d, domain.Validation()
		}
		d.Sources = out
	}
	if raw, ok := fields["packageIds"]; ok {
		var ids []int64
		if json.Unmarshal(raw, &ids) != nil {
			return d, domain.Validation()
		}
		out, err := articles.Packages(ids)
		if err != nil {
			return d, domain.Validation()
		}
		d.Packages = out
	}
	if d.CoverAssetID == nil {
		d.CoverAlt = nil
	}
	return d, nil
}

// checkArticleReferences verifies that the author, cover and related apps exist.
func checkArticleReferences(ctx context.Context, st *store.Store, d store.ArticleDraft) error {
	if d.AuthorID != nil {
		var found bool
		if err := st.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM article_authors WHERE id=?)", *d.AuthorID).Scan(&found).Error; err != nil {
			return err
		}
		if !found {
			return domain.Validation()
		}
	}
	if d.CoverAssetID != nil {
		var found bool
		if err := st.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM assets WHERE id=? AND kind IN ('cover','screenshot','og'))", *d.CoverAssetID).Scan(&found).Error; err != nil {
			return err
		}
		if !found {
			return domain.Validation()
		}
	}
	if len(d.Packages) > 0 {
		var count int64
		if err := st.DB.WithContext(ctx).Table("packages").Where("id IN ? AND kind IN ('cask','link') AND removed_at IS NULL", d.Packages).Count(&count).Error; err != nil {
			return err
		}
		if int(count) != len(d.Packages) {
			return domain.Validation()
		}
	}
	return nil
}

// articleReadiness lists what blocks review or publication, including body images that are not
// OpenNavo assets and app cards for apps that are not in the catalog.
func articleReadiness(ctx context.Context, st *store.Store, d store.ArticleDraft) ([]string, articles.Scan, error) {
	scan := articles.ScanBody(ptrText(d.BodyMarkdown))
	problems := articles.Readiness(d.Content(), scan)
	urls := []string{}
	for _, image := range scan.Images {
		urls = append(urls, image.URL)
	}
	assets, err := st.ImageAssets(ctx, urls)
	if err != nil {
		return nil, scan, err
	}
	add := func(problem string) {
		if !slices.Contains(problems, problem) {
			problems = append(problems, problem)
		}
	}
	for _, image := range scan.Images {
		if _, ok := assets[image.URL]; !ok {
			add(articles.ProblemImageHost)
		}
	}
	casks, err := st.LiveCaskIDs(ctx, scan.AppCards)
	if err != nil {
		return nil, scan, err
	}
	for _, token := range scan.AppCards {
		if _, ok := casks[token]; !ok {
			add(articles.ProblemUnknownApp)
		}
	}
	return problems, scan, nil
}

func ptrText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func expectedRevision(fields map[string]json.RawMessage) (int64, error) {
	raw, ok := fields["expectedRevision"]
	var value int64
	if !ok {
		return 0, nil
	}
	if json.Unmarshal(raw, &value) != nil || value < 1 {
		return 0, domain.Validation()
	}
	delete(fields, "expectedRevision")
	return value, nil
}

// articleWrite runs one article change in a transaction. Dry runs return the view before and after
// and roll back; real writes are audited. Draft edits change nothing public; the operations in
// publicArticleOps queue the pages they change (before and after, so a former author or app page
// is refreshed too) and invalidate only the API caches behind them.
func (s *Service) articleWrite(ctx context.Context, in Input, op string, fn func(st *store.Store) (int64, string, error)) (any, error) {
	ctx = operationContext(ctx, op, in)
	var result any
	var pages []string
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		var before any
		var beforePages []string
		if in.ID > 0 {
			view, err := s.articleView(ctx, st, in.ID)
			if err != nil {
				return err
			}
			before = view
			if publicArticleOps[op] {
				if beforePages, err = st.ArticlePaths(ctx, in.ID); err != nil {
					return err
				}
			}
		}
		id, action, err := fn(st)
		if err != nil {
			return err
		}
		after, err := s.articleView(ctx, st, id)
		if err != nil {
			return err
		}
		// Articles never published have no public pages yet.
		if published, _ := after["firstPublishedAt"].(*time.Time); publicArticleOps[op] && published != nil {
			if pages, err = st.ArticlePaths(ctx, id, beforePages...); err != nil {
				return err
			}
			if err := st.EnqueueArticlePages(ctx, pages); err != nil {
				return err
			}
		}
		if domain.CurrentOperation(ctx).DryRun {
			urls := []string{}
			if link, ok := after["webUrl"].(*string); ok && link != nil {
				urls = append(urls, *link)
			}
			result = map[string]any{"dryRun": true, "requestId": domain.CurrentOperation(ctx).RequestID, "affectedUrls": urls,
				"changes": []any{map[string]any{"object": map[string]any{"entity": "article", "objectKey": strconv.FormatInt(id, 10)}, "action": action, "before": before, "after": after}}}
			return errPreviewRollback
		}
		result = after
		return store.Audit(ctx, tx, in.Actor, op, "article", jsonID(id), articleSummary(before), articleSummary(after))
	})
	if errors.Is(err, errPreviewRollback) {
		return result, nil
	}
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	if s.Store.Redis != nil && len(pages) > 0 && !agentTransaction(ctx) {
		if err := cache.PublishInvalidation(ctx, s.Store.Redis, store.ArticleCachePatterns(pages)...); err != nil {
			return nil, err
		}
	}
	return result, nil
}

var publicArticleOps = map[string]bool{"PublishArticle": true, "WithdrawArticle": true, "RestoreArticle": true, "SetArticleSlug": true, "SetArticleLocale": true, "RevertArticle": true}

func jsonID(id int64) string {
	data, _ := json.Marshal(id)
	return string(data)
}

// articleSummary keeps the audit log small: identity, state and the working revision.
func articleSummary(view any) any {
	m, ok := view.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"id", "slug", "status", "revision"} {
		out[key] = m[key]
	}
	if working, ok := m["working"].(map[string]any); ok {
		out["working"] = map[string]any{"number": working["number"], "title": working["title"], "submittedAt": working["submittedAt"]}
	}
	return out
}

func (s *Service) lockArticle(ctx context.Context, st *store.Store, id int64, fields map[string]json.RawMessage) (store.Article, error) {
	expected, err := expectedRevision(fields)
	if err != nil {
		return store.Article{}, err
	}
	article, found, err := st.LoadArticle(ctx, id, true)
	if err != nil {
		return article, err
	}
	if !found {
		return article, fail(domain.CodeNotFound)
	}
	if expected > 0 && article.Revision != expected {
		return article, fail(domain.CodeConflict)
	}
	return article, nil
}

// saveWorkingDraft stores an edited draft: in place for an unpublished working revision (which
// withdraws its submission), as a new revision when the working revision is already published.
func (s *Service) saveWorkingDraft(ctx context.Context, st *store.Store, article store.Article, next store.ArticleDraft, action string, detail map[string]any) error {
	working, err := st.ArticleRevisionMeta(ctx, article.WorkingRevisionID)
	if err != nil {
		return err
	}
	current, err := st.LoadArticleDraft(ctx, working.ID)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(normalizeDraft(current), normalizeDraft(next)) {
		return nil
	}
	if err := checkArticleReferences(ctx, st, next); err != nil {
		return err
	}
	number := working.Number
	status := article.Status
	if working.PublishedAt != nil {
		if _, number, err = st.InsertArticleRevision(ctx, article.ID, next); err != nil {
			return err
		}
	} else {
		if err := st.ReplaceArticleDraft(ctx, working.ID, next); err != nil {
			return err
		}
		if working.SubmittedAt != nil {
			if err := st.ArticleEvent(ctx, article.ID, number, "review_invalidated", nil); err != nil {
				return err
			}
			if status == "review" {
				status = "draft"
			}
		}
	}
	if err := st.TouchArticle(ctx, article.ID, map[string]any{"status": status}); err != nil {
		return err
	}
	if detail == nil {
		detail = map[string]any{}
	}
	return st.ArticleEvent(ctx, article.ID, number, action, detail)
}

func normalizeDraft(d store.ArticleDraft) store.ArticleDraft {
	if d.Claims == nil {
		d.Claims = []articles.Claim{}
	}
	if d.Sources == nil {
		d.Sources = []articles.Source{}
	}
	if d.Packages == nil {
		d.Packages = []int64{}
	}
	return d
}

func (s *Service) createArticle(ctx context.Context, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	var slug string
	if raw, ok := fields["slug"]; !ok || json.Unmarshal(raw, &slug) != nil || !articles.ValidSlug(slug, 100) {
		return nil, domain.Validation()
	}
	var key *string
	if raw, ok := fields["idempotencyKey"]; ok {
		if json.Unmarshal(raw, &key) != nil || key != nil && (len(*key) < 1 || len(*key) > 128) {
			return nil, domain.Validation()
		}
	}
	delete(fields, "slug")
	delete(fields, "idempotencyKey")
	for _, required := range []string{"sourceLocale", "type", "title"} {
		if _, ok := fields[required]; !ok {
			return nil, domain.Validation()
		}
	}
	draft, err := patchArticleDraft(store.ArticleDraft{Sources: []articles.Source{}, Packages: []int64{}}, fields,
		"sourceLocale", "type", "title", "summary", "bodyMarkdown", "testNote", "seoTitle", "seoDescription", "authorId", "coverAssetId", "coverAlt", "sources", "packageIds", "claims", "target")
	if err != nil {
		return nil, err
	}
	return s.articleWrite(ctx, in, "CreateArticle", func(st *store.Store) (int64, string, error) {
		if key != nil {
			existing, err := st.ArticleByClientKey(ctx, *key)
			if err != nil || existing > 0 {
				return existing, "create", err
			}
		}
		if taken, err := st.SlugTaken(ctx, slug, 0); err != nil || taken {
			if err == nil {
				err = fail(domain.CodeConflict)
			}
			return 0, "", err
		}
		if err := checkArticleReferences(ctx, st, draft); err != nil {
			return 0, "", err
		}
		id, err := st.CreateArticleRecord(ctx, slug, key)
		if err != nil {
			return 0, "", err
		}
		_, number, err := st.InsertArticleRevision(ctx, id, draft)
		if err != nil {
			return 0, "", err
		}
		return id, "create", st.ArticleEvent(ctx, id, number, "create", nil)
	})
}

func (s *Service) updateArticle(ctx context.Context, op string, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	allowed := []string{"packageIds", "sourceLocale", "type", "title", "summary", "bodyMarkdown", "testNote", "seoTitle", "seoDescription", "authorId", "coverAssetId", "coverAlt", "sources", "claims", "target"}
	action := "update"
	if op == "SetArticlePackages" {
		allowed = []string{"packageIds"}
		if _, ok := fields["packageIds"]; !ok {
			return nil, domain.Validation()
		}
	}
	return s.articleWrite(ctx, in, op, func(st *store.Store) (int64, string, error) {
		article, err := s.lockArticle(ctx, st, in.ID, fields)
		if err != nil {
			return 0, "", err
		}
		current, err := st.LoadArticleDraft(ctx, article.WorkingRevisionID)
		if err != nil {
			return 0, "", err
		}
		next, err := patchArticleDraft(current, fields, allowed...)
		if err != nil {
			return 0, "", err
		}
		err = s.saveWorkingDraft(ctx, st, article, next, action, nil)
		return article.ID, "update", err
	})
}

func (s *Service) revertArticle(ctx context.Context, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	var number int
	if raw, ok := fields["revisionNumber"]; !ok || json.Unmarshal(raw, &number) != nil || number < 1 {
		return nil, domain.Validation()
	}
	delete(fields, "revisionNumber")
	return s.articleWrite(ctx, in, "RevertArticle", func(st *store.Store) (int64, string, error) {
		article, err := s.lockArticle(ctx, st, in.ID, fields)
		if err != nil {
			return 0, "", err
		}
		if len(fields) > 0 {
			return 0, "", domain.Validation()
		}
		var revisionID int64
		if err := st.DB.WithContext(ctx).Raw("SELECT id FROM article_revisions WHERE article_id=? AND number=?", article.ID, number).Scan(&revisionID).Error; err != nil {
			return 0, "", err
		}
		if revisionID == 0 {
			return 0, "", fail(domain.CodeNotFound)
		}
		old, err := st.LoadArticleDraft(ctx, revisionID)
		if err != nil {
			return 0, "", err
		}
		meta, err := st.ArticleRevisionMeta(ctx, revisionID)
		if err != nil {
			return 0, "", err
		}
		if meta.PublishedAt != nil {
			if !domain.CurrentOperation(ctx).Allowed("content:article:publish") {
				return 0, "", fail(domain.CodeForbidden)
			}
			if err := s.validateArticlePublication(ctx, st, article, revisionID, old); err != nil {
				return 0, "", err
			}
			// Restore the actual live revision and language state, preserving its original
			// publication date. The discarded draft never reaches the public pointer.
			update := map[string]any{"working_revision_id": revisionID, "published_revision_id": revisionID, "status": "published", "archived_at": nil}
			if article.PublishedRevisionID != nil {
				current, err := st.ArticleRevisionMeta(ctx, *article.PublishedRevisionID)
				if err != nil {
					return 0, "", err
				}
				if current.ContentHash != meta.ContentHash {
					update["content_updated_at"] = time.Now().UTC()
				}
			}
			if err := st.TouchArticle(ctx, article.ID, update); err != nil {
				return 0, "", err
			}
			if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "article", ID: revisionID}); err != nil {
				return 0, "", err
			}
			return article.ID, "update", st.ArticleEvent(ctx, article.ID, number, "revert", map[string]any{"from": number, "published": true})
		}
		// Apps removed from the catalog since then cannot come back.
		live := []int64{}
		for _, id := range old.Packages {
			var ok bool
			if err := st.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL)", id).Scan(&ok).Error; err != nil {
				return 0, "", err
			}
			if ok {
				live = append(live, id)
			}
		}
		old.Packages = live
		err = s.saveWorkingDraft(ctx, st, article, old, "revert", map[string]any{"from": number})
		return article.ID, "update", err
	})
}

func (s *Service) submitArticle(ctx context.Context, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	return s.articleWrite(ctx, in, "SubmitArticleReview", func(st *store.Store) (int64, string, error) {
		article, err := s.lockArticle(ctx, st, in.ID, fields)
		if err != nil {
			return 0, "", err
		}
		if len(fields) > 0 {
			return 0, "", domain.Validation()
		}
		working, err := st.ArticleRevisionMeta(ctx, article.WorkingRevisionID)
		if err != nil {
			return 0, "", err
		}
		if working.PublishedAt != nil {
			return 0, "", fail(domain.CodeInvalidState)
		}
		if working.SubmittedAt != nil {
			return article.ID, "update", nil
		}
		_, actorID, actorName := articleActorInfo(ctx)
		if err := st.DB.WithContext(ctx).Exec("UPDATE article_revisions SET submitted_at=now(),submitted_by=?,submitted_by_name=? WHERE id=?", actorID, actorName, working.ID).Error; err != nil {
			return 0, "", err
		}
		status := article.Status
		if status == "draft" {
			status = "review"
		}
		if err := st.TouchArticle(ctx, article.ID, map[string]any{"status": status}); err != nil {
			return 0, "", err
		}
		return article.ID, "update", st.ArticleEvent(ctx, article.ID, working.Number, "submit", nil)
	})
}

func articleActorInfo(ctx context.Context) (bool, *int64, string) {
	op := domain.CurrentOperation(ctx)
	if op == nil {
		return false, nil, ""
	}
	return op.AgentClientID > 0, op.ActorID, op.ActorName
}

// publishArticle makes the working revision live. The first publication sets the
// publication date; later ones move dateModified only when what readers see changed.
func (s *Service) publishArticle(ctx context.Context, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	return s.articleWrite(ctx, in, "PublishArticle", func(st *store.Store) (int64, string, error) {
		article, err := s.lockArticle(ctx, st, in.ID, fields)
		if err != nil {
			return 0, "", err
		}
		if len(fields) > 0 {
			return 0, "", domain.Validation()
		}
		working, err := st.ArticleRevisionMeta(ctx, article.WorkingRevisionID)
		if err != nil {
			return 0, "", err
		}
		if working.PublishedAt != nil {
			return 0, "", fail(domain.CodeInvalidState)
		}
		draft, err := st.LoadArticleDraft(ctx, working.ID)
		if err != nil {
			return 0, "", err
		}
		if err := s.validateArticlePublication(ctx, st, article, working.ID, draft); err != nil {
			return 0, "", err
		}
		substantive := true
		if article.PublishedRevisionID != nil {
			previous, err := st.ArticleRevisionMeta(ctx, *article.PublishedRevisionID)
			if err != nil {
				return 0, "", err
			}
			substantive = previous.ContentHash != working.ContentHash
		}
		if err := st.DB.WithContext(ctx).Exec("UPDATE article_revisions SET published_at=now() WHERE id=?", working.ID).Error; err != nil {
			return 0, "", err
		}
		now := time.Now().UTC()
		update := map[string]any{"status": "published", "published_revision_id": working.ID, "archived_at": nil,
			"first_published_at": gorm.Expr("COALESCE(first_published_at,?)", now)}
		if substantive {
			update["content_updated_at"] = now
		}
		if err := st.TouchArticle(ctx, article.ID, update); err != nil {
			return 0, "", err
		}
		if article.PublishedRevisionID != nil {
			if err := st.CarryArticleTranslations(ctx, *article.PublishedRevisionID, working.ID); err != nil {
				return 0, "", err
			}
		}
		if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "article", ID: working.ID}); err != nil {
			return 0, "", err
		}
		return article.ID, "update", st.ArticleEvent(ctx, article.ID, working.Number, "publish", map[string]any{"substantive": substantive})
	})
}

func (s *Service) withdrawArticle(ctx context.Context, op string, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	var reason *string
	if raw, ok := fields["reason"]; ok {
		if json.Unmarshal(raw, &reason) != nil {
			return nil, domain.Validation()
		}
		if reason, err = articles.Text(reason, 500); err != nil {
			return nil, domain.Validation()
		}
		delete(fields, "reason")
	}
	return s.articleWrite(ctx, in, op, func(st *store.Store) (int64, string, error) {
		article, err := s.lockArticle(ctx, st, in.ID, fields)
		if err != nil {
			return 0, "", err
		}
		if len(fields) > 0 {
			return 0, "", domain.Validation()
		}
		action, from, to, archivedAt := "withdraw", "published", "archived", any(time.Now().UTC())
		if op == "RestoreArticle" {
			action, from, to, archivedAt = "restore", "archived", "published", nil
		}
		if op == "RestoreArticle" {
			if article.PublishedRevisionID == nil {
				return 0, "", fail(domain.CodeInvalidState)
			}
			d, err := st.LoadArticleDraft(ctx, *article.PublishedRevisionID)
			if err != nil {
				return 0, "", err
			}
			if err := s.validateArticlePublication(ctx, st, article, *article.PublishedRevisionID, d); err != nil {
				return 0, "", err
			}
		}
		if article.Status != from {
			return 0, "", fail(domain.CodeInvalidState)
		}
		if err := st.TouchArticle(ctx, article.ID, map[string]any{"status": to, "archived_at": archivedAt}); err != nil {
			return 0, "", err
		}
		// Archived revisions are excluded from backfill. Restore their translation
		// dependencies only after the article is visible again, in this transaction.
		if op == "RestoreArticle" {
			if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "article", ID: *article.PublishedRevisionID}); err != nil {
				return 0, "", err
			}
		}
		detail := map[string]any{}
		if reason != nil {
			detail["reason"] = *reason
		}
		number := 0
		if article.PublishedRevisionID != nil {
			meta, err := st.ArticleRevisionMeta(ctx, *article.PublishedRevisionID)
			if err != nil {
				return 0, "", err
			}
			number = meta.Number
		}
		return article.ID, "update", st.ArticleEvent(ctx, article.ID, number, action, detail)
	})
}

// setArticleSlug renames an article; published slugs stay as redirects.
func (s *Service) setArticleSlug(ctx context.Context, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	var slug string
	if raw, ok := fields["slug"]; !ok || json.Unmarshal(raw, &slug) != nil || !articles.ValidSlug(slug, 100) {
		return nil, domain.Validation()
	}
	delete(fields, "slug")
	return s.articleWrite(ctx, in, "SetArticleSlug", func(st *store.Store) (int64, string, error) {
		article, err := s.lockArticle(ctx, st, in.ID, fields)
		if err != nil {
			return 0, "", err
		}
		if len(fields) > 0 {
			return 0, "", domain.Validation()
		}
		if slug == article.Slug {
			return article.ID, "update", nil
		}
		if taken, err := st.SlugTaken(ctx, slug, article.ID); err != nil || taken {
			if err == nil {
				err = fail(domain.CodeConflict)
			}
			return 0, "", err
		}
		if article.FirstPublishedAt != nil {
			if err := st.DB.WithContext(ctx).Exec("INSERT INTO article_slug_aliases(slug,article_id) VALUES(?,?) ON CONFLICT(slug) DO NOTHING", article.Slug, article.ID).Error; err != nil {
				return 0, "", err
			}
		}
		if err := st.DB.WithContext(ctx).Exec("DELETE FROM article_slug_aliases WHERE slug=? AND article_id=?", slug, article.ID).Error; err != nil {
			return 0, "", err
		}
		for _, rid := range []int64{article.WorkingRevisionID, func() int64 {
			if article.PublishedRevisionID != nil {
				return *article.PublishedRevisionID
			}
			return 0
		}()} {
			if rid == 0 {
				continue
			}
			d, err := st.LoadArticleDraft(ctx, rid)
			if err != nil {
				return 0, "", err
			}
			if d.Target == nil {
				continue
			}
			if err := st.DB.WithContext(ctx).Exec("UPDATE seo_targets SET target_url=? WHERE entity_key=? AND intent=? AND locale=? AND target_url=?", articleTargetURL(s.Config.WebBaseURL, d.SourceLocale, slug), d.Target.EntityKey, d.Target.Intent, d.SourceLocale, articleTargetURL(s.Config.WebBaseURL, d.SourceLocale, article.Slug)).Error; err != nil {
				return 0, "", err
			}
		}
		if err := st.TouchArticle(ctx, article.ID, map[string]any{"slug": slug}); err != nil {
			return 0, "", err
		}
		return article.ID, "update", st.ArticleEvent(ctx, article.ID, 0, "slug", map[string]any{"from": article.Slug, "to": slug})
	})
}

// setArticleLocale holds back or releases one language of the published revision.
func (s *Service) setArticleLocale(ctx context.Context, in Input) (any, error) {
	fields, err := articleFields(in.Body)
	if err != nil {
		return nil, err
	}
	var withheld bool
	if raw, ok := fields["withheld"]; !ok || json.Unmarshal(raw, &withheld) != nil || !i18n.Valid(in.Locale) {
		return nil, domain.Validation()
	}
	delete(fields, "withheld")
	return s.articleWrite(ctx, in, "SetArticleLocale", func(st *store.Store) (int64, string, error) {
		article, err := s.lockArticle(ctx, st, in.ID, fields)
		if err != nil {
			return 0, "", err
		}
		if len(fields) > 0 {
			return 0, "", domain.Validation()
		}
		if article.PublishedRevisionID == nil {
			return 0, "", fail(domain.CodeInvalidState)
		}
		result := st.DB.WithContext(ctx).Exec("UPDATE article_revision_i18n SET withheld=? WHERE revision_id=? AND locale=? AND locale<>source_locale", withheld, *article.PublishedRevisionID, in.Locale)
		if result.Error != nil {
			return 0, "", result.Error
		}
		if result.RowsAffected == 0 {
			return 0, "", fail(domain.CodeNotFound)
		}
		if in.Locale == "zh-CN" {
			if _, err := st.DeriveTraditional(ctx, content.Ref{Entity: "article", ID: *article.PublishedRevisionID}); err != nil {
				return 0, "", err
			}
		}
		if err := st.TouchArticle(ctx, article.ID, nil); err != nil {
			return 0, "", err
		}
		action := "release"
		if withheld {
			action = "withhold"
		}
		meta, err := st.ArticleRevisionMeta(ctx, *article.PublishedRevisionID)
		if err != nil {
			return 0, "", err
		}
		return article.ID, "update", st.ArticleEvent(ctx, article.ID, meta.Number, action, map[string]any{"locale": in.Locale})
	})
}

func (s *Service) articleWebURL(slug string) *string {
	base := strings.TrimRight(s.Config.WebBaseURL, "/")
	if base == "" {
		return nil
	}
	link := base + "/articles/" + url.PathEscape(slug)
	return &link
}

// articleView is the admin and Agent view of one article.
func (s *Service) articleView(ctx context.Context, st *store.Store, id int64) (map[string]any, error) {
	article, found, err := st.LoadArticle(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(domain.CodeNotFound)
	}
	working, err := s.articleRevisionView(ctx, st, article.WorkingRevisionID)
	if err != nil {
		return nil, err
	}
	draft, err := st.LoadArticleDraft(ctx, article.WorkingRevisionID)
	if err != nil {
		return nil, err
	}
	readiness, scan, err := articleReadiness(ctx, st, draft)
	if err != nil {
		return nil, err
	}
	images, err := articleImages(ctx, st, scan)
	if err != nil {
		return nil, err
	}
	var published any
	locales := []any{}
	if article.PublishedRevisionID != nil {
		meta, err := st.ArticleRevisionMeta(ctx, *article.PublishedRevisionID)
		if err != nil {
			return nil, err
		}
		published = map[string]any{"number": meta.Number, "publishedAt": meta.PublishedAt}
		siteLanguages, _ := json.Marshal(i18n.SiteLocales)
		rows, err := st.JSONRows(ctx, `SELECT jsonb_build_object('locale',l.code,'status',COALESCE(i.status,'missing'),'withheld',COALESCE(i.withheld,false),'source',l.code=r.source_locale,
 'failReason',i.fail_reason,'translatedAt',i.translated_at) AS data
 FROM article_revisions r CROSS JOIN jsonb_array_elements_text(?::jsonb) WITH ORDINALITY AS l(code,position)
 LEFT JOIN article_revision_i18n i ON i.revision_id=r.id AND i.locale=l.code WHERE r.id=? ORDER BY l.position`, string(siteLanguages), *article.PublishedRevisionID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			locales = append(locales, row)
		}
	}
	revisions, err := st.JSONRows(ctx, `SELECT jsonb_build_object('number',r.number,'title',COALESCE(i.title,''),'createdAt',r.created_at,'createdByName',r.created_by_name,
 'submittedAt',r.submitted_at,'publishedAt',r.published_at) AS data FROM article_revisions r LEFT JOIN article_revision_i18n i ON i.revision_id=r.id AND i.locale=r.source_locale
 WHERE r.article_id=? ORDER BY r.number DESC LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	events, err := st.JSONRows(ctx, `SELECT jsonb_build_object('action',e.action,'revisionNumber',e.revision_number,'actorType',e.actor_type,'actorName',e.actor_name,
 'detail',e.detail,'createdAt',e.created_at) AS data FROM article_events e WHERE e.article_id=? ORDER BY e.id DESC LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	var aliases []string
	if err := st.DB.WithContext(ctx).Raw("SELECT slug FROM article_slug_aliases WHERE article_id=? ORDER BY created_at", id).Scan(&aliases).Error; err != nil {
		return nil, err
	}
	var webURL *string
	if article.Status == "published" {
		webURL = s.articleWebURL(article.Slug)
	}
	return map[string]any{
		"id": article.ID, "slug": article.Slug, "status": article.Status, "revision": article.Revision,
		"firstPublishedAt": article.FirstPublishedAt, "contentUpdatedAt": article.ContentUpdatedAt, "archivedAt": article.ArchivedAt,
		"createdAt": article.CreatedAt, "updatedAt": article.UpdatedAt, "webUrl": webURL, "aliases": nonnilStrings(aliases),
		"working": working, "published": published, "readiness": readiness, "images": images, "locales": locales, "revisions": revisions, "events": events,
		"hasPendingChanges": article.PublishedRevisionID == nil || *article.PublishedRevisionID != article.WorkingRevisionID,
	}, nil
}

func nonnilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func articleImages(ctx context.Context, st *store.Store, scan articles.Scan) ([]any, error) {
	urls := []string{}
	for _, image := range scan.Images {
		urls = append(urls, image.URL)
	}
	assets, err := st.ImageAssets(ctx, urls)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, u := range urls {
		if asset, ok := assets[u]; ok {
			out = append(out, map[string]any{"url": asset.URL, "width": asset.Width, "height": asset.Height})
			delete(assets, u)
		}
	}
	return out, nil
}

func (s *Service) articleRevisionView(ctx context.Context, st *store.Store, revisionID int64) (map[string]any, error) {
	rows, err := st.JSONRows(ctx, `SELECT jsonb_build_object('number',r.number,'sourceLocale',r.source_locale,'type',r.type,'authorId',r.author_id,
 'author',(SELECT jsonb_build_object('id',a.id,'slug',a.slug,'name',a.name) FROM article_authors a WHERE a.id=r.author_id),
 'claims',r.claims,'claimEvidence',r.claim_evidence,'target',r.seo_target,'coverAssetId',r.cover_asset_id,'coverUrl',ca.url,'coverAlt',r.cover_alt,'sources',r.sources,
 'packages',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',p.id,'token',p.token,'name',p.name,'displayName',COALESCE(NULLIF(si.display_name,''),p.name),'iconUrl',ia.url) ORDER BY rp.sort)
   FROM article_revision_packages rp JOIN packages p ON p.id=rp.package_id LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN assets ia ON ia.id=m.icon_asset_id
   LEFT JOIN package_i18n si ON si.package_id=p.id AND si.locale=p.source_locale WHERE rp.revision_id=r.id),'[]'::jsonb),
 'title',COALESCE(i.title,''),'summary',i.summary,'bodyMarkdown',i.body_markdown,'testNote',i.test_note,'seoTitle',i.seo_title,'seoDescription',i.seo_description,
 'submittedAt',r.submitted_at,'submittedByName',r.submitted_by_name,'publishedAt',r.published_at,'createdByName',r.created_by_name,'createdAt',r.created_at,'updatedAt',r.updated_at) AS data
 FROM article_revisions r LEFT JOIN article_revision_i18n i ON i.revision_id=r.id AND i.locale=r.source_locale LEFT JOIN assets ca ON ca.id=r.cover_asset_id WHERE r.id=?`, revisionID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fail(domain.CodeNotFound)
	}
	var out map[string]any
	return out, json.Unmarshal(rows[0], &out)
}

func (s *Service) articleRead(ctx context.Context, op string, in Input) (any, error) {
	if op == "GetAdminArticle" {
		return s.articleView(ctx, s.Store, in.ID)
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	where, args := "TRUE", []any{}
	if v := text(in.Params, "status"); v != "" {
		if !slices.Contains(articles.Status, v) {
			return nil, domain.Validation()
		}
		where += " AND a.status=?"
		args = append(args, v)
	}
	if v := text(in.Params, "type"); v != "" {
		if !articles.ValidType(v) {
			return nil, domain.Validation()
		}
		where += " AND w.type=?"
		args = append(args, v)
	}
	if q := text(in.Params, "q"); q != "" {
		like := "%" + searchtext.EscapeLike(q) + "%"
		where += ` AND (a.slug ILIKE ? ESCAPE '\' OR wi.title ILIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	expr := `jsonb_build_object('id',a.id,'slug',a.slug,'status',a.status,'revision',a.revision,'type',w.type,'title',COALESCE(wi.title,''),'sourceLocale',w.source_locale,
 'workingNumber',w.number,'publishedNumber',pr.number,'submittedAt',w.submitted_at,'hasPendingChanges',a.published_revision_id IS DISTINCT FROM a.working_revision_id,
 'authorName',au.name,'firstPublishedAt',a.first_published_at,'contentUpdatedAt',a.content_updated_at,'updatedAt',a.updated_at)`
	from := ` FROM articles a JOIN article_revisions w ON w.id=a.working_revision_id LEFT JOIN article_revision_i18n wi ON wi.revision_id=w.id AND wi.locale=w.source_locale
 LEFT JOIN article_revisions pr ON pr.id=a.published_revision_id LEFT JOIN article_authors au ON au.id=w.author_id`
	return s.Store.AdminPage(ctx, expr, from, where, "a.updated_at DESC,a.id DESC", args, current, size)
}

// Authors (publish permission): public identity used for bylines and author pages.
func (s *Service) articleAuthors(ctx context.Context, op string, in Input) (any, error) {
	if op == "ListArticleAuthors" {
		rows, err := s.Store.JSONRows(ctx, `SELECT jsonb_build_object('id',a.id,'slug',a.slug,'name',a.name,'links',a.links,'avatarAssetId',a.avatar_asset_id,'avatarUrl',av.url,
 'sourceLocale',a.source_locale,'role',i.role,'bio',i.bio,'articleCount',(SELECT count(*) FROM articles ar JOIN article_revisions r ON r.id=ar.published_revision_id WHERE r.author_id=a.id AND ar.status='published'),
 'updatedAt',a.updated_at) AS data FROM article_authors a LEFT JOIN assets av ON av.id=a.avatar_asset_id LEFT JOIN article_author_i18n i ON i.author_id=a.id AND i.locale=a.source_locale ORDER BY a.name,a.id`)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, row := range rows {
			out = append(out, row)
		}
		return out, nil
	}
	var body struct {
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		SourceLocale  string `json:"sourceLocale"`
		Role          *string
		Bio           *string
		Links         []articles.Source `json:"links"`
		AvatarAssetID *int64            `json:"avatarAssetId"`
	}
	data, _ := json.Marshal(in.Body)
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || !articles.ValidSlug(body.Slug, 80) || !i18n.Valid(body.SourceLocale) {
		return nil, domain.Validation()
	}
	name, err := articles.Text(&body.Name, 80)
	if err != nil || name == nil {
		return nil, domain.Validation()
	}
	role, err := articles.Text(body.Role, 80)
	if err != nil {
		return nil, domain.Validation()
	}
	bio, err := articles.Text(body.Bio, 1000)
	if err != nil {
		return nil, domain.Validation()
	}
	links, err := articles.Sources(body.Links)
	if err != nil || len(links) > 8 {
		return nil, domain.Validation()
	}
	linksJSON, _ := json.Marshal(links)
	return s.write(ctx, in, op, "article_author", func(st *store.Store) (any, error) {
		if body.AvatarAssetID != nil {
			var ok bool
			if err := st.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM assets WHERE id=? AND kind IN ('icon','cover','screenshot'))", *body.AvatarAssetID).Scan(&ok).Error; err != nil {
				return nil, err
			}
			if !ok {
				return nil, domain.Validation()
			}
		}
		id := in.ID
		var oldSlug string
		if op == "CreateArticleAuthor" {
			if err := st.DB.WithContext(ctx).Raw("INSERT INTO article_authors(slug,name,links,avatar_asset_id,source_locale) VALUES(?,?,?::jsonb,?,?) RETURNING id",
				body.Slug, *name, string(linksJSON), body.AvatarAssetID, body.SourceLocale).Scan(&id).Error; err != nil {
				return nil, store.MapAdminError(err)
			}
		} else {
			if err := st.DB.WithContext(ctx).Raw("SELECT slug FROM article_authors WHERE id=? FOR UPDATE", id).Scan(&oldSlug).Error; err != nil {
				return nil, err
			}
			if oldSlug == "" {
				return nil, fail(domain.CodeNotFound)
			}
			if err := st.DB.WithContext(ctx).Exec("UPDATE article_authors SET slug=?,name=?,links=?::jsonb,avatar_asset_id=?,source_locale=?,updated_at=now() WHERE id=?",
				body.Slug, *name, string(linksJSON), body.AvatarAssetID, body.SourceLocale, id).Error; err != nil {
				return nil, store.MapAdminError(err)
			}
		}
		if err := st.DB.WithContext(ctx).Exec("DELETE FROM article_author_i18n WHERE author_id=? AND locale=?", id, body.SourceLocale).Error; err != nil {
			return nil, err
		}
		if err := st.DB.WithContext(ctx).Exec("INSERT INTO article_author_i18n(author_id,locale,role,bio,status,source_locale) VALUES(?,?,?,?,'source',?)", id, body.SourceLocale, role, bio, body.SourceLocale).Error; err != nil {
			return nil, err
		}
		if err := st.DB.WithContext(ctx).Exec("UPDATE article_author_i18n SET source_locale=? WHERE author_id=?", body.SourceLocale, id).Error; err != nil {
			return nil, err
		}
		if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "article_author", ID: id}); err != nil {
			return nil, err
		}
		paths := []string{"/authors/" + body.Slug}
		if oldSlug != "" && oldSlug != body.Slug {
			paths = append(paths, "/authors/"+oldSlug)
		}
		// Bylines on the author's published articles show the new name too.
		var bylines []string
		if err := st.DB.WithContext(ctx).Raw("SELECT '/articles/'||a.slug FROM articles a JOIN article_revisions r ON r.id=a.published_revision_id WHERE a.status='published' AND r.author_id=? ORDER BY a.id", id).Scan(&bylines).Error; err != nil {
			return nil, err
		}
		if err := st.EnqueueArticlePages(ctx, append(paths, bylines...)); err != nil {
			return nil, err
		}
		return map[string]any{"id": id}, nil
	})
}
