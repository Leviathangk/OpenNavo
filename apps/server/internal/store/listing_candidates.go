package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
)

// ListingInput is private until publication. The locked baseline is independent
// of an Agent's assertions about a newly discovered application.
type ListingInput struct {
	ExpectedRevision  int64            `json:"expectedRevision"`
	Action            string           `json:"action"`
	Link              domain.LinkInput `json:"link"`
	Summary           string           `json:"summary"`
	Description       string           `json:"description"`
	CategoryID        int64            `json:"categoryId"`
	IconAssetID       int64            `json:"iconAssetId"`
	BaselinePackageID int64            `json:"baselinePackageId"`
	DownloadMode      string           `json:"downloadMode"`
	EvidenceID        int64            `json:"evidenceId"`
}
type ListingCandidate struct {
	ID                int64           `json:"id"`
	Token             string          `json:"token"`
	State             string          `json:"state"`
	Revision          int64           `json:"revision"`
	Payload           json.RawMessage `json:"payload"`
	BaselinePackageID *int64          `json:"baselinePackageId"`
	PackageID         *int64          `json:"packageId"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}
type ListingBaseline struct {
	PackageID    int64     `json:"packageId"`
	Domain       string    `json:"domain"`
	Developer    string    `json:"developer"`
	TeamID       string    `json:"teamId"`
	FactID       int64     `json:"factId"`
	EvidenceID   int64     `json:"evidenceId"`
	EvidenceHash string    `json:"evidenceHash"`
	VerifiedBy   int64     `json:"verifiedBy"`
	VerifiedAt   time.Time `json:"verifiedAt"`
	ValidUntil   time.Time `json:"validUntil"`
	Suspicious   bool      `json:"suspicious"`
}

func (s *Store) ListingCandidate(ctx context.Context, token string) (*ListingCandidate, error) {
	var c ListingCandidate
	err := s.DB.WithContext(ctx).Table("seo_listing_candidates").Where("token=?", token).Find(&c).Error
	if err != nil || c.ID == 0 {
		return nil, err
	}
	return &c, nil
}
func (s *Store) ListingBaseline(ctx context.Context, id int64) (*ListingBaseline, error) {
	var b ListingBaseline
	err := s.DB.WithContext(ctx).Table("seo_listing_baselines").Where("package_id=?", id).Find(&b).Error
	if err != nil || b.PackageID == 0 {
		return nil, err
	}
	return &b, nil
}

// A name/alias/bundle collision requires resolving the existing entity instead
// of publishing a second listing. Catalog writers share the same advisory lock.
func (s *Store) ListingDuplicate(ctx context.Context, in domain.LinkInput) (bool, error) {
	var duplicate bool
	err := s.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM packages p WHERE p.kind IN ('cask','link') AND
 (lower(p.token)=lower(?) OR lower(p.name)=lower(?) OR EXISTS(SELECT 1 FROM unnest(p.names) n WHERE lower(n)=lower(?))))
 OR EXISTS(SELECT 1 FROM package_aliases WHERE kind IN ('cask','link') AND lower(token)=lower(?))`, in.Token, in.Name, in.Name, in.Token).Scan(&duplicate).Error
	if err != nil || duplicate {
		return duplicate, err
	}
	for _, bundle := range in.BundleIDs {
		// Homebrew's raw uninstall/artifacts data has several shapes. An exact JSON
		// string match is intentionally conservative; partial bundle IDs never match.
		encoded, _ := json.Marshal(bundle)
		err = s.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM link_apps WHERE ?=ANY(bundle_ids)) OR EXISTS(SELECT 1 FROM packages WHERE kind='cask' AND strpos(raw::text,?)>0)`, bundle, string(encoded)).Scan(&duplicate).Error
		if err != nil || duplicate {
			return duplicate, err
		}
	}
	return false, nil
}
