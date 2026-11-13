package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// DeveloperSubmission is one anonymous new/claim/location lead. Editors review it before publishing.
type DeveloperSubmission struct {
	ID           int64           `json:"id" gorm:"column:id"`
	PublicToken  string          `json:"publicToken" gorm:"column:public_token"`
	Kind         string          `json:"kind" gorm:"column:kind"`
	Status       string          `json:"status" gorm:"column:status"`
	SourceKind   string          `json:"sourceKind" gorm:"column:source_kind"`
	SourceURL    string          `json:"sourceUrl" gorm:"column:source_url"`
	PackageID    *int64          `json:"packageId" gorm:"column:package_id"`
	Payload      json.RawMessage `json:"payload" gorm:"column:payload"`
	ContactEmail string          `json:"contactEmail" gorm:"column:contact_email"`
	Proof        string          `json:"proof" gorm:"column:proof"`
	ReviewerID   *int64          `json:"reviewerId" gorm:"column:reviewer_id"`
	DecisionNote *string         `json:"decisionNote" gorm:"column:decision_note"`
	CreatedAt    time.Time       `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt    time.Time       `json:"updatedAt" gorm:"column:updated_at"`
	DecidedAt    *time.Time      `json:"decidedAt" gorm:"column:decided_at"`
}

// DeveloperSubmissionEvent is one status change, newest last.
type DeveloperSubmissionEvent struct {
	Status    string    `json:"status" gorm:"column:status"`
	Note      *string   `json:"note" gorm:"column:note"`
	Actor     string    `json:"actor" gorm:"column:actor"`
	CreatedAt time.Time `json:"createdAt" gorm:"column:created_at"`
}

func newDeveloperToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type DeveloperSubmissionInput struct {
	Kind         string
	SourceKind   string
	SourceURL    string
	PackageID    *int64
	Payload      json.RawMessage
	ContactEmail string
	Proof        string
}

const developerSubmissionColumns = `id, public_token, kind, status, source_kind, source_url, package_id, payload, contact_email, proof, reviewer_id, decision_note, created_at, updated_at, decided_at`

func (s *Store) CreateDeveloperSubmission(ctx context.Context, in DeveloperSubmissionInput) (DeveloperSubmission, error) {
	payload := in.Payload
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage(`{}`)
	}
	token, err := newDeveloperToken()
	if err != nil {
		return DeveloperSubmission{}, err
	}
	var row DeveloperSubmission
	err = s.DB.WithContext(ctx).Raw(`
		INSERT INTO developer_submissions (public_token, kind, source_kind, source_url, package_id, payload, contact_email, proof)
		VALUES (?, ?, ?, ?, ?, ?::jsonb, ?, ?)
		RETURNING `+developerSubmissionColumns,
		token, in.Kind, in.SourceKind, in.SourceURL, in.PackageID, string(payload), in.ContactEmail, in.Proof,
	).Scan(&row).Error
	return row, err
}

// CreateDeveloperSubmissionWithEvent stores the lead and its first timeline row atomically,
// so a retry after a failed event cannot duplicate the submission.
func (s *Store) CreateDeveloperSubmissionWithEvent(ctx context.Context, in DeveloperSubmissionInput) (DeveloperSubmission, error) {
	var row DeveloperSubmission
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		txStore := &Store{DB: tx}
		created, err := txStore.CreateDeveloperSubmission(ctx, in)
		if err != nil {
			return err
		}
		if err := txStore.AppendDeveloperSubmissionEvent(ctx, created.ID, created.Status, nil, "developer"); err != nil {
			return err
		}
		row = created
		return nil
	})
	return row, err
}

// DeveloperSubmissionByToken returns the submission behind a public status link.
func (s *Store) DeveloperSubmissionByToken(ctx context.Context, token string) (DeveloperSubmission, error) {
	var row DeveloperSubmission
	err := s.DB.WithContext(ctx).Raw(`
		SELECT `+developerSubmissionColumns+` FROM developer_submissions WHERE public_token = ?
	`, token).Scan(&row).Error
	return row, err
}

func (s *Store) AppendDeveloperSubmissionEvent(ctx context.Context, submissionID int64, status string, note *string, actor string) error {
	return s.DB.WithContext(ctx).Exec(`
		INSERT INTO developer_submission_events (submission_id, status, note, actor)
		VALUES (?, ?, ?, ?)
	`, submissionID, status, note, actor).Error
}

func (s *Store) DeveloperSubmissionEvents(ctx context.Context, submissionID int64) ([]DeveloperSubmissionEvent, error) {
	var rows []DeveloperSubmissionEvent
	err := s.DB.WithContext(ctx).Raw(`
		SELECT status, note, actor, created_at
		FROM developer_submission_events
		WHERE submission_id = ?
		ORDER BY created_at, id
	`, submissionID).Scan(&rows).Error
	if rows == nil {
		rows = []DeveloperSubmissionEvent{}
	}
	return rows, err
}

func (s *Store) DeveloperSubmissions(ctx context.Context, status, query string, limit int) ([]DeveloperSubmission, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	query = strings.TrimSpace(query)
	var rows []DeveloperSubmission
	err := s.DB.WithContext(ctx).Raw(`
		SELECT `+developerSubmissionColumns+`
		FROM developer_submissions
		WHERE (? = '' OR status = ?)
		  AND (? = ''
		       OR contact_email ILIKE '%' || ? || '%'
		       OR payload->>'name' ILIKE '%' || ? || '%'
		       OR ('DS-' || lpad(id::text, 6, '0')) ILIKE '%' || ? || '%')
		ORDER BY created_at DESC
		LIMIT ?
	`, status, status, query, query, query, query, limit).Scan(&rows).Error
	if rows == nil {
		rows = []DeveloperSubmission{}
	}
	return rows, err
}

func (s *Store) DeveloperSubmission(ctx context.Context, id int64) (DeveloperSubmission, error) {
	var row DeveloperSubmission
	err := s.DB.WithContext(ctx).Raw(`
		SELECT `+developerSubmissionColumns+` FROM developer_submissions WHERE id = ?
	`, id).Scan(&row).Error
	return row, err
}

func (s *Store) UpdateDeveloperSubmission(ctx context.Context, id int64, status string, decisionNote *string, payload json.RawMessage, packageID *int64, reviewerID *int64) (DeveloperSubmission, error) {
	var payloadValue any
	if len(payload) > 0 && string(payload) != "null" {
		payloadValue = string(payload)
	}
	var row DeveloperSubmission
	err := s.DB.WithContext(ctx).Raw(`
		UPDATE developer_submissions
		SET status = ?,
		    decision_note = ?,
		    payload = COALESCE(?::jsonb, payload),
		    package_id = COALESCE(?, package_id),
		    reviewer_id = COALESCE(?, reviewer_id),
		    decided_at = CASE WHEN ? IN ('approved', 'rejected', 'published') THEN COALESCE(decided_at, now()) ELSE decided_at END,
		    updated_at = now()
		WHERE id = ?
		RETURNING `+developerSubmissionColumns,
		status, decisionNote, payloadValue, packageID, reviewerID, status, id,
	).Scan(&row).Error
	return row, err
}

// UpsertPackageLocation writes an approved (or pending) per-App location.
func (s *Store) UpsertPackageLocation(ctx context.Context, packageID int64, countryCode, city, cityKey, region string, lat, lng float64, remote bool, source, status, confidence, evidence string, reviewerID *int64) error {
	err := s.DB.WithContext(ctx).Exec(`
		INSERT INTO package_locations (package_id, country_code, city, city_key, region, lat, lng, remote, source, status, confidence, evidence_url, verified_by, verified_at)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, CASE WHEN ? = 'approved' THEN now() ELSE NULL END)
		ON CONFLICT (package_id) DO UPDATE SET
		  country_code = EXCLUDED.country_code,
		  city = EXCLUDED.city,
		  city_key = EXCLUDED.city_key,
		  region = EXCLUDED.region,
		  lat = EXCLUDED.lat,
		  lng = EXCLUDED.lng,
		  remote = EXCLUDED.remote,
		  source = EXCLUDED.source,
		  status = EXCLUDED.status,
		  confidence = EXCLUDED.confidence,
		  evidence_url = EXCLUDED.evidence_url,
		  verified_by = EXCLUDED.verified_by,
		  verified_at = EXCLUDED.verified_at,
		  updated_at = now()
	`, packageID, countryCode, city, cityKey, region, lat, lng, remote, source, status, confidence, evidence, reviewerID, status).Error
	if err != nil {
		return err
	}
	return QueueCacheInvalidation(ctx, s.DB, fmt.Sprintf("developer:location:%d", packageID), "c:developer-map:*")
}

// SetDeveloperPick features (or updates) a package in Discover. Passing active=false hides it.
// An unchanged pick is left alone, so re-saving a published submission does not reset
// published_at and jump ahead of picks with the same sort.
func (s *Store) SetDeveloperPick(ctx context.Context, packageID int64, submissionID *int64, quote string, sort int, active bool) error {
	result := s.DB.WithContext(ctx).Exec(`
		INSERT INTO developer_picks (package_id, submission_id, quote, sort, active, published_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, now())
		ON CONFLICT (package_id) DO UPDATE SET
		  submission_id = COALESCE(EXCLUDED.submission_id, developer_picks.submission_id),
		  quote = EXCLUDED.quote,
		  sort = EXCLUDED.sort,
		  active = EXCLUDED.active,
		  published_at = CASE WHEN EXCLUDED.active THEN now() ELSE developer_picks.published_at END,
		  updated_at = now()
		WHERE developer_picks.submission_id IS DISTINCT FROM COALESCE(EXCLUDED.submission_id, developer_picks.submission_id)
		   OR COALESCE(developer_picks.quote, '') IS DISTINCT FROM COALESCE(EXCLUDED.quote, '')
		   OR developer_picks.sort <> EXCLUDED.sort
		   OR developer_picks.active <> EXCLUDED.active
	`, packageID, submissionID, quote, sort, active)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	return QueueCacheInvalidation(ctx, s.DB, "developer:picks", "c:developer-picks:*")
}

// FeatureDeveloperPick creates the pick this submission owns, or updates it when the
// submission already owns it. A pick curated on the picks page (no submission) or owned
// by another submission stays as the editor left it, so a later rejection cannot switch
// that pick off.
func (s *Store) FeatureDeveloperPick(ctx context.Context, packageID, submissionID int64, quote string, sort int) error {
	var current struct {
		SubmissionID *int64
		Found        bool
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT submission_id, TRUE AS found FROM developer_picks WHERE package_id = ?`, packageID).Scan(&current).Error; err != nil {
		return err
	}
	if current.Found && (current.SubmissionID == nil || *current.SubmissionID != submissionID) {
		return nil
	}
	return s.SetDeveloperPick(ctx, packageID, &submissionID, quote, sort, true)
}

// DeactivateDeveloperPick retires the pick one submission owns and keeps its quote
// and order, so a later republish restores the same placement. Picks curated on the
// picks page (no submission) are left alone.
func (s *Store) DeactivateDeveloperPick(ctx context.Context, packageID int64, submissionID *int64) error {
	result := s.DB.WithContext(ctx).Exec(`
		UPDATE developer_picks
		SET active = FALSE, updated_at = now()
		WHERE package_id = ? AND submission_id IS NOT DISTINCT FROM ? AND active
	`, packageID, submissionID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	return QueueCacheInvalidation(ctx, s.DB, "developer:picks", "c:developer-picks:*")
}

// DeveloperRetention bounds rejected leads and status events; published submissions
// stay as the audit trail behind developer picks.
const DeveloperRetention = 365 * 24 * time.Hour

// PruneDeveloperSubmissions deletes rejected leads past the retention window.
func (s *Store) PruneDeveloperSubmissions(ctx context.Context, before time.Time) (int64, error) {
	result := s.DB.WithContext(ctx).Exec(`DELETE FROM developer_submissions WHERE status = 'rejected' AND updated_at < ?`, before)
	return result.RowsAffected, result.Error
}

// PruneDeveloperSubmissionEvents trims old timeline rows while keeping the submission itself.
func (s *Store) PruneDeveloperSubmissionEvents(ctx context.Context, before time.Time) (int64, error) {
	result := s.DB.WithContext(ctx).Exec(`DELETE FROM developer_submission_events WHERE created_at < ?`, before)
	return result.RowsAffected, result.Error
}

// DeveloperPickRow is one curated Discover pick with its package identity.
type DeveloperPickRow struct {
	PackageID    int64     `json:"packageId" gorm:"column:package_id"`
	Kind         string    `json:"kind" gorm:"column:kind"`
	Token        string    `json:"token" gorm:"column:token"`
	Name         string    `json:"name" gorm:"column:name"`
	IconURL      *string   `json:"iconUrl" gorm:"column:icon_url"`
	Quote        string    `json:"quote" gorm:"column:quote"`
	Sort         int       `json:"sort" gorm:"column:sort"`
	Active       bool      `json:"active" gorm:"column:active"`
	PublishedAt  time.Time `json:"publishedAt" gorm:"column:published_at"`
	SubmissionID *int64    `json:"submissionId" gorm:"column:submission_id"`
}

// DeveloperPickRows lists every pick, active or not, for the admin management page.
func (s *Store) DeveloperPickRows(ctx context.Context) ([]DeveloperPickRow, error) {
	var rows []DeveloperPickRow
	err := s.DB.WithContext(ctx).Raw(`
		SELECT dp.package_id, p.kind, p.token, p.name, COALESCE(a.url, m.icon_url) AS icon_url,
		       COALESCE(dp.quote, '') AS quote, dp.sort, dp.active, dp.published_at, dp.submission_id
		FROM developer_picks dp
		JOIN packages p ON p.id = dp.package_id
		LEFT JOIN package_meta m ON m.package_id = p.id
		LEFT JOIN assets a ON a.id = m.icon_asset_id
		ORDER BY dp.sort, dp.published_at DESC, dp.package_id
	`).Scan(&rows).Error
	if rows == nil {
		rows = []DeveloperPickRow{}
	}
	return rows, err
}
