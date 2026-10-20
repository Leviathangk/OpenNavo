package agentgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"golang.org/x/net/html"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const RuleVersion = "agent-v1"
const PlaybookVersion = "seo-routine-v1"

type Violation struct{ Reason string }

func (e *Violation) Error() string { return "Agent gate: " + e.Reason }
func reject(reason string) error   { return &Violation{Reason: reason} }

type Evidence struct {
	ID          int64      `json:"id"`
	EntityKey   string     `json:"entityKey"`
	SourceURL   string     `json:"sourceUrl"`
	FinalURL    string     `json:"finalUrl"`
	BodyText    string     `json:"-"`
	Excerpt     string     `json:"excerpt"`
	BodyHash    string     `json:"bodyHash"`
	FetchedAt   time.Time  `json:"fetchedAt"`
	ValidUntil  time.Time  `json:"validUntil"`
	Authority   string     `json:"authority"`
	FetchStatus int        `json:"fetchStatus"`
	CreatedBy   string     `json:"-"`
	CaptureKey  string     `json:"-"`
	RequestHash string     `json:"-"`
	RevokedAt   *time.Time `json:"revokedAt"`
}

func (Evidence) TableName() string { return "seo_evidence_snapshots" }

type EvidenceService struct {
	DB        *gorm.DB
	HTTP      *http.Client
	UserAgent string
}
type Capture struct {
	DryRun                                         bool
	TaskID, Fencing                                int64
	Owner, IdempotencyKey, EntityKey, URL, Excerpt string
}

func NewEvidenceService(db *gorm.DB, userAgent string) *EvidenceService {
	return &EvidenceService{DB: db, HTTP: fetch.NewClient(15 * time.Second), UserAgent: userAgent}
}

func normalizeText(value string) string { return strings.Join(strings.Fields(value), " ") }

// Capture keeps the Agent path fenced by its task lease.
func (e *EvidenceService) Capture(ctx context.Context, in Capture) (Evidence, error) {
	return e.capture(ctx, in, func(tx *gorm.DB) error {
		task, err := seotasks.Verify(ctx, tx, in.TaskID, in.Fencing, in.Owner)
		if err != nil {
			return err
		}
		key, err := seotasks.CanonicalEntity(ctx, tx, task.EntityKey)
		if err != nil {
			return err
		}
		wanted, err := seotasks.CanonicalEntity(ctx, tx, in.EntityKey)
		if err != nil {
			return err
		}
		if key != wanted {
			return reject("evidence_entity_mismatch")
		}
		return nil
	})
}

// CaptureArticle is the human editor path. It uses article edit permission and
// current package membership instead of requiring an unrelated Agent task.
func (e *EvidenceService) CaptureArticle(ctx context.Context, in Capture, articleID int64) (Evidence, error) {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.ActorID == nil || op.AgentClientID > 0 || op.AgentTokenID > 0 || !op.Allowed("content:article:edit") {
		return Evidence{}, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: 403}
	}
	in.Owner = fmt.Sprintf("admin:%d", *op.ActorID)
	in.TaskID, in.Fencing = 0, 0
	return e.capture(ctx, in, func(tx *gorm.DB) error {
		var revision int64
		r := tx.Raw("SELECT working_revision_id FROM articles WHERE id=? FOR SHARE", articleID).Scan(&revision)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return reject("unknown_article")
		}
		if in.EntityKey == fmt.Sprintf("article:%d", articleID) {
			return nil
		}
		var linked bool
		if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM article_revision_packages WHERE revision_id=? AND 'pkg:' || package_id = ?)", revision, in.EntityKey).Scan(&linked).Error; err != nil {
			return err
		}
		if !linked {
			return reject("evidence_entity_mismatch")
		}
		return nil
	})
}

func (e *EvidenceService) capture(ctx context.Context, in Capture, verify func(*gorm.DB) error) (Evidence, error) {
	out := Evidence{}
	normalized, err := seotasks.CanonicalEntity(ctx, e.DB, in.EntityKey)
	if err != nil {
		return out, err
	}
	in.EntityKey = normalized
	if in.Owner == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 200 || len(in.URL) > 2048 || len(in.EntityKey) > 300 || len(in.Excerpt) > 20000 {
		return out, reject("invalid_evidence_request")
	}
	target, err := url.Parse(in.URL)
	if err != nil || target.Scheme != "https" || target.Fragment != "" || fetch.ValidateURL(target) != nil {
		return out, reject("invalid_source_url")
	}
	excerpt := normalizeText(in.Excerpt)
	if excerpt == "" {
		return out, reject("empty_excerpt")
	}
	raw, _ := json.Marshal([]string{strconv.FormatInt(in.TaskID, 10), canonicalEntity(in.EntityKey), in.URL, excerpt})
	requestHash := seotasks.Hash(raw)
	captureKey := in.Owner + ":" + in.IdempotencyKey
	existing := e.DB.WithContext(ctx).Where("capture_key=?", captureKey).Limit(1).Find(&out)
	if existing.Error != nil {
		return out, existing.Error
	}
	if existing.RowsAffected > 0 {
		if out.RequestHash != requestHash {
			return Evidence{}, seotasks.ErrConflict
		}
		if err := e.DB.WithContext(ctx).Transaction(verify); err != nil {
			return Evidence{}, err
		}
		return out, nil
	}
	// Recheck authorization before network I/O and immediately before persistence.
	err = e.DB.WithContext(ctx).Transaction(verify)
	if err != nil {
		return out, err
	}
	authority, err := e.authority(ctx, in.EntityKey, target)
	if err != nil {
		return out, err
	}
	if e.HTTP == nil {
		return out, reject("evidence_fetch_unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return out, reject("invalid_source_url")
	}
	req.Header.Set("User-Agent", e.UserAgent)
	req.Header.Set("Accept", "text/html, text/plain, application/json")
	response, err := e.HTTP.Do(req)
	if err != nil {
		return out, reject("evidence_fetch_failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return out, reject("evidence_fetch_failed")
	}
	final := response.Request.URL
	finalAuthority, err := e.authority(ctx, in.EntityKey, final)
	if err != nil {
		return out, err
	}
	if authority == "official" && finalAuthority != "official" {
		return out, reject("unofficial_redirect")
	}
	mime := strings.ToLower(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if mime != "text/html" && mime != "text/plain" && mime != "application/json" {
		return out, reject("unsupported_evidence_type")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if err != nil || len(body) > 1048576 {
		return out, reject("evidence_too_large")
	}
	text := string(body)
	if mime == "text/html" {
		text, err = visibleText(text)
		if err != nil {
			return out, reject("invalid_evidence_html")
		}
	}
	text = normalizeText(text)
	if !strings.Contains(text, excerpt) {
		return out, reject("excerpt_not_found")
	}
	sum := sha256.Sum256([]byte(text))
	out = Evidence{EntityKey: canonicalEntity(in.EntityKey), SourceURL: target.String(), FinalURL: final.String(), BodyText: text, Excerpt: excerpt, BodyHash: hex.EncodeToString(sum[:]), Authority: authority, FetchStatus: 200, CreatedBy: in.Owner, CaptureKey: captureKey, RequestHash: requestHash}
	err = e.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := verify(tx); err != nil {
			return err
		}
		if err := tx.Raw("SELECT clock_timestamp()").Scan(&out.FetchedAt).Error; err != nil {
			return err
		}
		out.ValidUntil = out.FetchedAt.Add(30 * 24 * time.Hour)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo:evidence:"+captureKey).Error; err != nil {
			return err
		}
		var prior Evidence
		result := tx.Where("capture_key=?", captureKey).Limit(1).Find(&prior)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if prior.RequestHash != requestHash {
				return seotasks.ErrConflict
			}
			out = prior
			return nil
		}
		if in.DryRun {
			return nil
		}
		return tx.Create(&out).Error
	})
	return out, err
}

func canonicalEntity(key string) string {
	if strings.HasPrefix(key, "package:") {
		return "pkg:" + strings.TrimPrefix(key, "package:")
	}
	return key
}

func (e *EvidenceService) authority(ctx context.Context, entity string, target *url.URL) (string, error) {
	if target == nil || target.Scheme != "https" || fetch.ValidateURL(target) != nil {
		return "", reject("invalid_source_url")
	}
	entity = canonicalEntity(entity)
	if !strings.HasPrefix(entity, "pkg:") {
		return "reference", nil
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(entity, "pkg:"), 10, 64)
	if err != nil || id < 1 {
		return "", reject("invalid_entity")
	}
	var row struct{ Homepage, RepoURL, DownloadURL string }
	r := e.DB.WithContext(ctx).Raw(`SELECT coalesce(p.homepage,'') homepage,coalesce(m.repo_url,l.github_repo,'') repo_url,coalesce(p.download_url,'') download_url FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN link_apps l ON l.package_id=p.id WHERE p.id=? AND p.removed_at IS NULL`, id).Scan(&row)
	if r.Error != nil {
		return "", r.Error
	}
	if r.RowsAffected != 1 {
		return "", reject("unknown_entity")
	}
	if officialURL(target, row.Homepage, row.RepoURL, row.DownloadURL) {
		return "official", nil
	}
	return "", reject("source_not_official")
}

func officialURL(target *url.URL, homepage, repo, download string) bool {
	host := strings.ToLower(target.Hostname())
	if repo != "" && !strings.Contains(repo, "://") {
		repo = "https://github.com/" + strings.Trim(repo, "/")
	}
	for _, raw := range []string{homepage, repo, download} {
		base, err := url.Parse(raw)
		if err != nil || base.Hostname() == "" {
			continue
		}
		registered := strings.ToLower(base.Hostname())
		if registered == "github.com" {
			parts := strings.Split(strings.Trim(base.Path, "/"), "/")
			if len(parts) < 2 {
				continue
			}
			path := strings.ToLower(strings.Trim(target.Path, "/"))
			prefix := strings.ToLower(strings.Join(parts[:2], "/"))
			if (host == "github.com" || host == "raw.githubusercontent.com") && (path == prefix || strings.HasPrefix(path, prefix+"/")) {
				return true
			}
			continue
		}
		if registered == "apps.apple.com" {
			if host == registered && target.Path == base.Path {
				return true
			}
			continue
		}
		// A registered download file does not establish ownership of an entire CDN.
		if raw == download && raw != homepage {
			continue
		}
		registered = strings.TrimPrefix(registered, "www.")
		if host == registered || strings.HasSuffix(host, "."+registered) {
			return true
		}
	}
	return false
}

func visibleText(source string) (string, error) {
	node, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", err
	}
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "template") {
			return
		}
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "hidden" || a.Key == "aria-hidden" && a.Val == "true" {
					return
				}
			}
		}
		if n.Type == html.TextNode {
			out.WriteString(n.Data)
			out.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return out.String(), nil
}

func LoadEvidence(ctx context.Context, tx *gorm.DB, ids []int64, entity string, official bool) ([]Evidence, error) {
	if len(ids) == 0 || len(ids) > 40 {
		return nil, reject("evidence_required")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, reject("invalid_evidence_ids")
		}
		seen[id] = true
	}
	entity, err := seotasks.CanonicalEntity(ctx, tx, entity)
	if err != nil {
		return nil, err
	}
	var rows []Evidence
	err = tx.WithContext(ctx).Where("id IN ? AND entity_key=? AND revoked_at IS NULL AND valid_until>clock_timestamp()", ids, canonicalEntity(entity)).Order("id").Clauses(clause.Locking{Strength: "SHARE"}).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		return nil, reject("evidence_expired_or_wrong_entity")
	}
	for _, row := range rows {
		if official && row.Authority != "official" {
			return nil, reject("source_not_official")
		}
	}
	return rows, nil
}

func IsViolation(err error) bool { var v *Violation; return errors.As(err, &v) }
