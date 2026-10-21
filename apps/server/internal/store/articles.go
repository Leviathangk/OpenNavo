package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

// Article is the identity row; content lives in revisions (WP14).
type Article struct {
	ID                  int64
	Slug, Status        string
	WorkingRevisionID   int64
	PublishedRevisionID *int64
	Revision            int64
	FirstPublishedAt    *time.Time
	ContentUpdatedAt    *time.Time
	ArchivedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ArticleDraft is one revision's content in its source language.
type ArticleDraft struct {
	Claims         []articles.Claim
	Target         *articles.Target
	SourceLocale   string
	Type           string
	AuthorID       *int64
	CoverAssetID   *int64
	CoverAlt       *string
	Sources        []articles.Source
	Packages       []int64
	Title          string
	Summary        *string
	BodyMarkdown   *string
	TestNote       *string
	SeoTitle       *string
	SeoDescription *string
}

// Content is what readers see of the draft.
func (d ArticleDraft) Content() articles.Content {
	return articles.Content{Type: d.Type, AuthorID: d.AuthorID, CoverAssetID: d.CoverAssetID, CoverAlt: d.CoverAlt, Title: d.Title,
		Summary: d.Summary, Body: d.BodyMarkdown, TestNote: d.TestNote, Packages: d.Packages, Sources: d.Sources}
}

// ArticleRevision is a revision's state.
type ArticleRevision struct {
	ID, ArticleID int64
	Number        int
	ContentHash   string
	SubmittedAt   *time.Time
	PublishedAt   *time.Time
}

// AssetSize is the stored size of an image asset.
type AssetSize struct {
	URL           string
	Width, Height *int
}

func articleActor(ctx context.Context) (string, *int64, string) {
	op := domain.CurrentOperation(ctx)
	switch {
	case op == nil:
		return "system", nil, ""
	case op.AgentClientID > 0:
		return "agent", op.ActorID, op.ActorName
	case op.ActorID == nil:
		return "system", nil, op.ActorName
	}
	return "admin", op.ActorID, op.ActorName
}

const articleColumns = `id,slug,status,working_revision_id,published_revision_id,revision,first_published_at,content_updated_at,archived_at,created_at,updated_at`

// LoadArticle reads an article; lock takes a row lock for the rest of the transaction.
func (s *Store) LoadArticle(ctx context.Context, id int64, lock bool) (Article, bool, error) {
	var row Article
	query := "SELECT " + articleColumns + " FROM articles WHERE id=?"
	if lock {
		query += " FOR UPDATE"
	}
	if err := s.DB.WithContext(ctx).Raw(query, id).Scan(&row).Error; err != nil || row.ID == 0 {
		return row, false, err
	}
	return row, true, nil
}

// ArticleByClientKey finds the article created with an idempotency key.
func (s *Store) ArticleByClientKey(ctx context.Context, key string) (int64, error) {
	var id int64
	err := s.DB.WithContext(ctx).Raw("SELECT id FROM articles WHERE client_key=?", key).Scan(&id).Error
	return id, err
}

// SlugTaken reports a slug used by another article, now or formerly.
func (s *Store) SlugTaken(ctx context.Context, slug string, except int64) (bool, error) {
	var taken bool
	err := s.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM articles WHERE slug=? AND id<>?) OR EXISTS(SELECT 1 FROM article_slug_aliases WHERE slug=? AND article_id<>?)`,
		slug, except, slug, except).Scan(&taken).Error
	return taken, err
}

// ArticleRevisionMeta reads a revision's state.
func (s *Store) ArticleRevisionMeta(ctx context.Context, revisionID int64) (ArticleRevision, error) {
	var row ArticleRevision
	if err := s.DB.WithContext(ctx).Raw("SELECT id,article_id,number,content_hash,submitted_at,published_at FROM article_revisions WHERE id=?", revisionID).Scan(&row).Error; err != nil {
		return row, err
	}
	if row.ID == 0 {
		return row, gorm.ErrRecordNotFound
	}
	return row, nil
}

// LoadArticleDraft reads a revision's content in its source language.
func (s *Store) LoadArticleDraft(ctx context.Context, revisionID int64) (ArticleDraft, error) {
	var row struct {
		Claims, Target                                    string
		SourceLocale, Type                                string
		AuthorID, CoverAssetID                            *int64
		CoverAlt                                          *string
		Sources, Packages                                 string
		Title                                             string
		Summary, BodyMarkdown, TestNote, SeoTitle, SeoDes *string
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT r.claims::text AS claims,COALESCE(r.seo_target,'null'::jsonb)::text AS target,r.source_locale,r.type,r.author_id,r.cover_asset_id,r.cover_alt,r.sources::text AS sources,
 COALESCE((SELECT json_agg(p.package_id ORDER BY p.sort) FROM article_revision_packages p WHERE p.revision_id=r.id),'[]')::text AS packages,
 COALESCE(i.title,'') AS title,i.summary,i.body_markdown,i.test_note,i.seo_title,i.seo_description AS seo_des
 FROM article_revisions r LEFT JOIN article_revision_i18n i ON i.revision_id=r.id AND i.locale=r.source_locale WHERE r.id=?`, revisionID).Scan(&row).Error
	if err != nil {
		return ArticleDraft{}, err
	}
	if row.Type == "" {
		return ArticleDraft{}, gorm.ErrRecordNotFound
	}
	out := ArticleDraft{SourceLocale: row.SourceLocale, Type: row.Type, AuthorID: row.AuthorID, CoverAssetID: row.CoverAssetID, CoverAlt: row.CoverAlt,
		Title: row.Title, Summary: row.Summary, BodyMarkdown: row.BodyMarkdown, TestNote: row.TestNote, SeoTitle: row.SeoTitle, SeoDescription: row.SeoDes}
	if err := json.Unmarshal([]byte(row.Claims), &out.Claims); err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(row.Target), &out.Target); err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(row.Sources), &out.Sources); err != nil {
		return out, fmt.Errorf("decode article sources: %w", err)
	}
	if err := json.Unmarshal([]byte(row.Packages), &out.Packages); err != nil {
		return out, fmt.Errorf("decode article packages: %w", err)
	}
	return out, nil
}

func (s *Store) writeArticleText(ctx context.Context, revisionID int64, d ArticleDraft) error {
	claims := d.Claims
	if claims == nil {
		claims = []articles.Claim{}
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	target, err := json.Marshal(d.Target)
	if err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Exec("UPDATE article_revisions SET claims=?::jsonb,claim_evidence='[]'::jsonb,seo_target=NULLIF(?::jsonb,'null'::jsonb) WHERE id=? AND published_at IS NULL", string(raw), string(target), revisionID).Error; err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Exec("DELETE FROM article_revision_i18n WHERE revision_id=?", revisionID).Error; err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO article_revision_i18n(revision_id,locale,title,summary,body_markdown,test_note,seo_title,seo_description,cover_alt,status,source_locale)
 VALUES(?,?,?,?,?,?,?,?,?,'source',?)`, revisionID, d.SourceLocale, d.Title, d.Summary, d.BodyMarkdown, d.TestNote, d.SeoTitle, d.SeoDescription, d.CoverAlt, d.SourceLocale).Error; err != nil {
		return MapAdminError(err)
	}
	if err := s.DB.WithContext(ctx).Exec("DELETE FROM article_revision_packages WHERE revision_id=?", revisionID).Error; err != nil {
		return err
	}
	for index, id := range d.Packages {
		if err := s.DB.WithContext(ctx).Exec("INSERT INTO article_revision_packages(revision_id,package_id,sort) VALUES(?,?,?)", revisionID, id, index).Error; err != nil {
			return MapAdminError(err)
		}
	}
	return nil
}

func sourcesJSON(sources []articles.Source) string {
	if sources == nil {
		sources = []articles.Source{}
	}
	data, _ := json.Marshal(sources)
	return string(data)
}

// InsertArticleRevision adds a revision after the article's latest one and makes it the working revision.
func (s *Store) InsertArticleRevision(ctx context.Context, articleID int64, d ArticleDraft) (int64, int, error) {
	_, actorID, actorName := articleActor(ctx)
	var row struct {
		ID     int64
		Number int
	}
	err := s.DB.WithContext(ctx).Raw(`INSERT INTO article_revisions(article_id,number,source_locale,type,author_id,cover_asset_id,cover_alt,sources,content_hash,created_by,created_by_name)
 VALUES(?,COALESCE((SELECT max(number) FROM article_revisions WHERE article_id=?),0)+1,?,?,?,?,?,?::jsonb,?,?,?) RETURNING id,number`,
		articleID, articleID, d.SourceLocale, d.Type, d.AuthorID, d.CoverAssetID, d.CoverAlt, sourcesJSON(d.Sources), d.Content().Hash(), actorID, actorName).Scan(&row).Error
	if err != nil {
		return 0, 0, MapAdminError(err)
	}
	if err := s.writeArticleText(ctx, row.ID, d); err != nil {
		return 0, 0, err
	}
	return row.ID, row.Number, s.DB.WithContext(ctx).Exec("UPDATE articles SET working_revision_id=?,updated_at=now() WHERE id=?", row.ID, articleID).Error
}

// ReplaceArticleDraft rewrites an unpublished working revision in place and clears its submission.
func (s *Store) ReplaceArticleDraft(ctx context.Context, revisionID int64, d ArticleDraft) error {
	if err := s.DB.WithContext(ctx).Exec(`UPDATE article_revisions SET source_locale=?,type=?,author_id=?,cover_asset_id=?,cover_alt=?,sources=?::jsonb,content_hash=?,
 submitted_at=NULL,submitted_by=NULL,submitted_by_name='',updated_at=now() WHERE id=? AND published_at IS NULL`,
		d.SourceLocale, d.Type, d.AuthorID, d.CoverAssetID, d.CoverAlt, sourcesJSON(d.Sources), d.Content().Hash(), revisionID).Error; err != nil {
		return MapAdminError(err)
	}
	return s.writeArticleText(ctx, revisionID, d)
}

// CarryArticleTranslations copies the previous revision's ready translations into
// a newly published revision whose source text is unchanged, so a change to the
// byline, claims or sources keeps every language live. Languages the new
// revision already has are left alone.
func (s *Store) CarryArticleTranslations(ctx context.Context, fromRevision, toRevision int64) error {
	return s.DB.WithContext(ctx).Exec(`INSERT INTO article_revision_i18n(revision_id,locale,title,summary,body_markdown,test_note,seo_title,seo_description,cover_alt,status,source_locale,source_hash,model,translated_at,fail_reason,machine_translated,withheld)
 SELECT tr.id,o.locale,o.title,o.summary,o.body_markdown,o.test_note,o.seo_title,o.seo_description,o.cover_alt,o.status,o.source_locale,o.source_hash,o.model,o.translated_at,o.fail_reason,o.machine_translated,o.withheld
 FROM article_revisions fr JOIN article_revisions tr ON tr.id=? AND tr.source_locale=fr.source_locale
 JOIN article_revision_i18n fs ON fs.revision_id=fr.id AND fs.locale=fr.source_locale
 JOIN article_revision_i18n ts ON ts.revision_id=tr.id AND ts.locale=tr.source_locale
 JOIN article_revision_i18n o ON o.revision_id=fr.id AND o.locale<>fr.source_locale AND o.status IN ('machine','manual')
 WHERE fr.id=? AND (fs.title,fs.summary,fs.body_markdown,fs.test_note,fs.seo_title,fs.seo_description,fs.cover_alt) IS NOT DISTINCT FROM (ts.title,ts.summary,ts.body_markdown,ts.test_note,ts.seo_title,ts.seo_description,ts.cover_alt)
 ON CONFLICT(revision_id,locale) DO NOTHING`, toRevision, fromRevision).Error
}

// CreateArticleRecord inserts the identity row; the caller adds the first revision in the same transaction.
func (s *Store) CreateArticleRecord(ctx context.Context, slug string, clientKey *string) (int64, error) {
	_, actorID, _ := articleActor(ctx)
	var id int64
	err := s.DB.WithContext(ctx).Raw("INSERT INTO articles(slug,status,working_revision_id,client_key,created_by) VALUES(?,'draft',0,?,?) RETURNING id", slug, clientKey, actorID).Scan(&id).Error
	return id, MapAdminError(err)
}

// TouchArticle bumps the change counter that writes check as expectedRevision.
func (s *Store) TouchArticle(ctx context.Context, id int64, fields map[string]any) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["revision"] = gorm.Expr("revision+1")
	fields["updated_at"] = time.Now().UTC()
	return MapAdminError(s.DB.WithContext(ctx).Table("articles").Where("id=?", id).Updates(fields).Error)
}

// ArticleEvent records a review step.
func (s *Store) ArticleEvent(ctx context.Context, articleID int64, number int, action string, detail map[string]any) error {
	actorType, actorID, actorName := articleActor(ctx)
	if detail == nil {
		detail = map[string]any{}
	}
	data, _ := json.Marshal(detail)
	var revision any
	if number > 0 {
		revision = number
	}
	return s.DB.WithContext(ctx).Exec("INSERT INTO article_events(article_id,revision_number,action,actor_type,actor_id,actor_name,detail) VALUES(?,?,?,?,?,?,?::jsonb)",
		articleID, revision, action, actorType, actorID, actorName, string(data)).Error
}

// ImageAssets returns the stored sizes of image assets with these URLs; others are not allowed in articles.
func (s *Store) ImageAssets(ctx context.Context, urls []string) (map[string]AssetSize, error) {
	out := map[string]AssetSize{}
	if len(urls) == 0 {
		return out, nil
	}
	var rows []AssetSize
	if err := s.DB.WithContext(ctx).Raw("SELECT DISTINCT ON (url) url,width,height FROM assets WHERE url IN ? AND kind IN ('cover','screenshot','og') ORDER BY url,id", urls).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.URL] = row
	}
	return out, nil
}

// LiveCaskIDs is the legacy name for article card lookup; it accepts cask and link apps.
func (s *Store) LiveCaskIDs(ctx context.Context, tokens []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(tokens) == 0 {
		return out, nil
	}
	var rows []struct {
		ID    int64
		Token string
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT id,token FROM packages WHERE kind IN ('cask','link') AND removed_at IS NULL AND token IN ?", tokens).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Token] = row.ID
	}
	return out, nil
}

// EnqueueContentURLs records page changes for the URL change feed (paths without language prefix).
func (s *Store) EnqueueContentURLs(ctx context.Context, paths []string, announce bool) error {
	if len(paths) == 0 {
		return nil
	}
	// Each row permits at most 100 paths. One INSERT keeps all batches atomic,
	// both on its own and within the caller's business transaction.
	values := make([]string, 0, (len(paths)+99)/100)
	args := make([]any, 0, 2*cap(values))
	for batch := range slices.Chunk(paths, 100) {
		data, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		values = append(values, "(ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?)")
		args = append(args, string(data), announce)
	}
	return s.DB.WithContext(ctx).Exec("INSERT INTO content_url_changes(paths,announce) VALUES"+strings.Join(values, ","), args...).Error
}

// EnqueueArticlePages queues changed article pages to be refreshed and announced; the feeds are
// refreshed too but never announced.
func (s *Store) EnqueueArticlePages(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := s.EnqueueContentURLs(ctx, paths, true); err != nil {
		return err
	}
	return s.EnqueueContentURLs(ctx, []string{articles.FeedPath}, false)
}

// ArticleCachePatterns lists the API caches behind changed article pages: every article read, and
// the detail of each app whose page lists related articles.
func ArticleCachePatterns(paths []string) []string {
	out := []string{"c:art:*"}
	for _, path := range paths {
		if token, ok := strings.CutPrefix(path, "/apps/"); ok {
			out = append(out, cache.PackageDetailPrefix+"cask:"+token+":*")
		}
	}
	return out
}

// ArticlePaths lists the pages an article's state affects: the article, the list, the author page
// and the pages of the apps it covers.
func (s *Store) ArticlePaths(ctx context.Context, articleID int64, extra ...string) ([]string, error) {
	var row struct {
		Slug       string
		AuthorSlug *string
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT a.slug,au.slug AS author_slug FROM articles a LEFT JOIN article_revisions r ON r.id=COALESCE(a.published_revision_id,a.working_revision_id)
 LEFT JOIN article_authors au ON au.id=r.author_id WHERE a.id=?`, articleID).Scan(&row).Error; err != nil {
		return nil, err
	}
	if row.Slug == "" {
		return nil, errors.New("article not found")
	}
	out := []string{"/articles/" + row.Slug, "/articles"}
	if row.AuthorSlug != nil {
		out = append(out, "/authors/"+*row.AuthorSlug)
	}
	// App pages list the articles that cover them.
	var apps []string
	if err := s.DB.WithContext(ctx).Raw(`SELECT '/apps/'||p.token FROM articles a JOIN article_revision_packages rp ON rp.revision_id=COALESCE(a.published_revision_id,a.working_revision_id)
 JOIN packages p ON p.id=rp.package_id WHERE a.id=? ORDER BY rp.sort`, articleID).Scan(&apps).Error; err != nil {
		return nil, err
	}
	for _, path := range append(apps, extra...) {
		if !slices.Contains(out, path) {
			out = append(out, path)
		}
	}
	return out, nil
}
