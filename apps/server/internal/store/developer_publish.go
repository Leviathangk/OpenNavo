package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"gorm.io/gorm"

	"github.com/opennavo/opennavo/server/internal/domain"
)

// DeveloperPayload is the editable metadata a developer submits with a new app or a
// claim/update. Fields are optional: publishing applies only what is present.
type DeveloperPayload struct {
	Name        string  `json:"name"`
	Summary     string  `json:"summary"`
	Description string  `json:"description"`
	Developer   string  `json:"developer"`
	Category    string  `json:"category"`
	Homepage    string  `json:"homepage"`
	RepoURL     string  `json:"repoUrl"`
	DownloadURL string  `json:"downloadUrl"`
	IconURL     string  `json:"iconUrl"`
	Version     string  `json:"version"`
	MinMacos    string  `json:"minMacos"`
	License     string  `json:"license"`
	CountryCode string  `json:"countryCode"`
	City        string  `json:"city"`
	CityKey     string  `json:"cityKey"`
	Region      string  `json:"region"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	Remote      bool    `json:"remote"`
	// Claim forms send these; a missing flag means "update", so new submissions
	// keep writing every field they collected.
	UpdateLocation bool `json:"updateLocation"`
	UpdateSummary  bool `json:"updateSummary"`
	UpdateIcon     bool `json:"updateIcon"`
	// SourceURL is the link the developer submitted; it is used as the homepage when
	// the payload does not carry one.
	SourceURL string `json:"-"`
}

// ParseDeveloperPayload reads a submission payload, trimming every string field.
func ParseDeveloperPayload(raw json.RawMessage) DeveloperPayload {
	payload := DeveloperPayload{UpdateLocation: true, UpdateSummary: true, UpdateIcon: true}
	if len(raw) == 0 || string(raw) == "null" {
		return payload
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.normalized()
}

func (p DeveloperPayload) normalized() DeveloperPayload {
	trim := func(value *string) { *value = strings.TrimSpace(*value) }
	trim(&p.Name)
	trim(&p.Summary)
	trim(&p.Description)
	trim(&p.Developer)
	trim(&p.Category)
	trim(&p.Homepage)
	trim(&p.RepoURL)
	trim(&p.DownloadURL)
	trim(&p.IconURL)
	trim(&p.Version)
	trim(&p.MinMacos)
	trim(&p.License)
	trim(&p.CountryCode)
	trim(&p.City)
	trim(&p.CityKey)
	trim(&p.Region)
	trim(&p.SourceURL)
	if p.CountryCode != "" {
		p.CountryCode = strings.ToUpper(p.CountryCode)
	}
	return p
}

// HasLocation reports whether the payload carries a usable country for the map.
func (p DeveloperPayload) HasLocation() bool {
	return len(p.CountryCode) == 2
}

// GithubRepo extracts owner/repo from a github.com URL, if the payload has one.
func (p DeveloperPayload) GithubRepo() *string {
	candidates := []string{p.RepoURL, p.SourceURL, p.Homepage}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "github.com" && host != "www.github.com" {
			continue
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		repo := parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
		if repoPattern(repo) {
			return &repo
		}
	}
	return nil
}

func repoPattern(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-' {
				return false
			}
		}
	}
	return true
}

// tokenBase derives a stable link token from the app name.
func (p DeveloperPayload) tokenBase() string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(p.Name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '@', r == '.', r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	token := strings.Trim(b.String(), "-._")
	if token == "" {
		token = "app"
	}
	if len(token) > 96 {
		token = strings.Trim(token[:96], "-._")
	}
	if token == "" {
		token = "app"
	}
	return token
}

func isConflict(err error) bool {
	var appErr *domain.AppError
	return errors.As(err, &appErr) && appErr.Code == domain.CodeConflict
}

func isNotFound(err error) bool {
	var appErr *domain.AppError
	return errors.As(err, &appErr) && appErr.Code == domain.CodeNotFound
}

// CreateLinkPackageFromPayload publishes a `new` submission as a link package.
// The catalog write path (WriteLinkApp) keeps catalog changes, search and sync
// consistent; ApplyDeveloperPayload then stores the editable metadata.
func (s *Store) CreateLinkPackageFromPayload(ctx context.Context, payload DeveloperPayload) (int64, error) {
	payload = payload.normalized()
	if payload.Name == "" {
		return 0, domain.Rejected("name_required")
	}
	homepage := payload.Homepage
	if homepage == "" && payload.SourceURL != "" && domain.LinkHTTPS(payload.SourceURL) {
		homepage = payload.SourceURL
	}
	download := payload.DownloadURL
	repo := payload.GithubRepo()
	if homepage == "" && download == "" && repo == nil {
		return 0, domain.Rejected("link_target_required")
	}

	input := domain.LinkInput{Name: payload.Name, SourceLocale: "en-US", Pricing: "unknown"}
	if homepage != "" {
		input.Homepage = &homepage
	}
	if download != "" {
		input.DownloadURL = &download
	}
	if repo != nil {
		input.GithubRepo = repo
	}
	if payload.Version != "" && domain.ValidLinkVersion(payload.Version) && repo == nil {
		input.Version = &payload.Version
	}

	base := payload.tokenBase()
	for attempt := 0; attempt < 100 && input.Token == ""; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		err := s.LinkTokenAvailable(ctx, candidate, 0)
		if err == nil {
			input.Token = candidate
			break
		}
		if !isConflict(err) {
			return 0, err
		}
	}
	if input.Token == "" {
		return 0, &domain.AppError{Code: domain.CodeConflict, HTTPStatus: 409}
	}

	id, err := s.WriteLinkApp(ctx, 0, input)
	if err != nil {
		return 0, err
	}
	if err := s.ApplyDeveloperPayload(ctx, id, payload); err != nil {
		return 0, err
	}
	return id, nil
}

// ApplyDeveloperPayload writes the editor-approved metadata onto an existing package.
// Link packages accept homepage/download/repo/version edits; casks keep Homebrew's
// authoritative facts and only take the curated display fields.
func (s *Store) ApplyDeveloperPayload(ctx context.Context, packageID int64, payload DeveloperPayload) error {
	payload = payload.normalized()
	if packageID == 0 {
		return domain.Validation()
	}
	var kind string
	if err := s.DB.WithContext(ctx).Raw(`SELECT kind FROM packages WHERE id = ?`, packageID).Scan(&kind).Error; err != nil {
		return err
	}
	if kind == "" {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	icon := ""
	if payload.UpdateIcon && payload.IconURL != "" {
		if !domain.LinkHTTPS(payload.IconURL) {
			return domain.Rejected("icon_url_not_https")
		}
		icon = payload.IconURL
	}
	if payload.Homepage != "" && !domain.LinkHTTPS(payload.Homepage) {
		return domain.Rejected("homepage_not_https")
	}
	if payload.DownloadURL != "" && !domain.LinkHTTPS(payload.DownloadURL) {
		return domain.Rejected("download_url_not_https")
	}
	summary := ""
	if payload.UpdateSummary {
		summary = payload.Summary
	}
	manualVersion := payload.Version
	if kind == "link" {
		var syncOwned bool
		if err := s.DB.WithContext(ctx).Raw(`SELECT COALESCE(github_repo IS NOT NULL OR sparkle_feed IS NOT NULL, FALSE) FROM link_apps WHERE package_id = ?`, packageID).Scan(&syncOwned).Error; err != nil {
			return err
		}
		// A release source owns the version; manual writes would be overwritten by the next sync.
		if syncOwned {
			manualVersion = ""
		}
	}
	if payload.RepoURL != "" && payload.GithubRepo() == nil {
		return domain.Rejected("repo_url_invalid")
	}
	repo := payload.GithubRepo()
	repoValue := ""
	if repo != nil {
		repoValue = *repo
	}

	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if kind == "link" {
			version := manualVersion
			versionBase := version
			if index := strings.Index(version, ","); index > 0 {
				versionBase = version[:index]
			}
			if err := tx.Exec(`
				UPDATE packages
				SET homepage = COALESCE(NULLIF(?, ''), homepage),
				    download_url = COALESCE(NULLIF(?, ''), download_url),
				    desc_en = COALESCE(NULLIF(?, ''), desc_en),
				    version = COALESCE(NULLIF(?, ''), version),
				    version_base = COALESCE(NULLIF(?, ''), version_base),
				    updated_at = now()
				WHERE id = ?
			`, payload.Homepage, payload.DownloadURL, summary, version, versionBase, packageID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`
				UPDATE link_apps
				SET homepage = COALESCE(NULLIF(?, ''), homepage),
				    download_url = COALESCE(NULLIF(?, ''), download_url),
				    github_repo = COALESCE(NULLIF(?, ''), github_repo)
				WHERE package_id = ?
			`, payload.Homepage, payload.DownloadURL, repoValue, packageID).Error; err != nil {
				return err
			}
		} else if summary != "" {
			if err := tx.Exec(`UPDATE packages SET desc_en = ?, updated_at = now() WHERE id = ?`, payload.Summary, packageID).Error; err != nil {
				return err
			}
		}
		if payload.UpdateSummary && (payload.Name != "" || payload.Summary != "" || payload.Description != "") {
			if err := tx.Exec(`
				INSERT INTO package_i18n (package_id, locale, display_name, summary, description, status, source, reviewed_at)
				VALUES (?, 'en-US', NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), 'manual', 'human', now())
				ON CONFLICT (package_id, locale) DO UPDATE SET
				  display_name = COALESCE(NULLIF(EXCLUDED.display_name, ''), package_i18n.display_name),
				  summary = COALESCE(NULLIF(EXCLUDED.summary, ''), package_i18n.summary),
				  description = COALESCE(NULLIF(EXCLUDED.description, ''), package_i18n.description),
				  status = 'manual',
				  source = 'human',
				  reviewed_at = now(),
				  updated_at = now()
			`, packageID, payload.Name, payload.Summary, payload.Description).Error; err != nil {
				return err
			}
		}
		if payload.Developer != "" || icon != "" || repoValue != "" {
			if err := tx.Exec(`
				INSERT INTO package_meta (package_id, developer, icon_url, repo_url)
				VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))
				ON CONFLICT (package_id) DO UPDATE SET
				  developer = COALESCE(NULLIF(EXCLUDED.developer, ''), package_meta.developer),
				  icon_url = COALESCE(NULLIF(EXCLUDED.icon_url, ''), package_meta.icon_url),
				  repo_url = COALESCE(NULLIF(EXCLUDED.repo_url, ''), package_meta.repo_url),
				  updated_at = now()
			`, packageID, payload.Developer, icon, repoValue).Error; err != nil {
				return err
			}
		}
		if payload.Category != "" {
			var categoryID int64
			if err := tx.Raw(`SELECT id FROM categories WHERE slug = ?`, payload.Category).Scan(&categoryID).Error; err != nil {
				return err
			}
			if categoryID == 0 {
				return domain.RejectedDetail("category_not_found", payload.Category)
			}
			if err := tx.Exec(`
				UPDATE package_categories SET is_primary = FALSE
				WHERE package_id = ? AND is_primary
			`, packageID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`
				INSERT INTO package_categories (package_id, category_id, is_primary, source)
				VALUES (?, ?, TRUE, 'human')
				ON CONFLICT (package_id, category_id) DO UPDATE SET is_primary = TRUE, source = 'human'
			`, packageID, categoryID).Error; err != nil {
				return err
			}
		}
		if err := UpdateSearchIndex(ctx, tx, []int64{packageID}); err != nil {
			return err
		}
		return QueueCacheInvalidation(ctx, tx, fmt.Sprintf("developer:package:%d", packageID),
			"c:pkg:*", "c:developer-map:*", "c:developer-picks:*")
	})
}

// DeveloperPackageRef returns the published package identity for the status page.
func (s *Store) DeveloperPackageRef(ctx context.Context, packageID int64) (kind, token, name string, ok bool, err error) {
	var row struct {
		Kind  string
		Token string
		Name  string
	}
	err = s.DB.WithContext(ctx).Raw(`SELECT kind, token, name FROM packages WHERE id = ?`, packageID).Scan(&row).Error
	if err != nil {
		return "", "", "", false, err
	}
	if row.Token == "" {
		return "", "", "", false, nil
	}
	return row.Kind, row.Token, row.Name, true, nil
}

// DeveloperPublishInput is one approved submission moving to published.
type DeveloperPublishInput struct {
	SubmissionID int64
	Status       string
	Note         *string
	Payload      json.RawMessage
	PackageID    *int64
	ReviewerID   *int64
	// Feature is the editor's pick switch: nil leaves the current pick alone, true
	// features the package with Quote and Sort, false retires this submission's pick.
	Feature *bool
	Quote   string
	Sort    int
}

// PublishDeveloperSubmission creates or updates the package, location, pick and
// submission row in one transaction: a failure leaves no half-published app.
func (s *Store) PublishDeveloperSubmission(ctx context.Context, in DeveloperPublishInput) (DeveloperSubmission, error) {
	var out DeveloperSubmission
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		txStore := &Store{DB: tx}
		var row DeveloperSubmission
		if err := tx.Raw(`SELECT `+developerSubmissionColumns+` FROM developer_submissions WHERE id = ? FOR UPDATE`, in.SubmissionID).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == 0 {
			return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
		}
		payload := in.Payload
		if len(payload) == 0 {
			payload = row.Payload
		}
		parsed := ParseDeveloperPayload(payload)
		parsed.SourceURL = row.SourceURL

		packageID := in.PackageID
		if packageID == nil {
			packageID = row.PackageID
		}
		if packageID == nil {
			if row.Kind != "new" {
				return domain.Rejected("package_required_for_claim")
			}
			created, err := txStore.CreateLinkPackageFromPayload(ctx, parsed)
			if err != nil {
				return err
			}
			packageID = &created
		} else {
			if row.Kind == "new" {
				// Withdrawing a new submission takes its link package down; publishing it
				// again brings the same package (token, curated data, history) back.
				if err := txStore.restoreDeveloperPackage(ctx, *packageID); err != nil {
					return err
				}
			}
			if err := txStore.ApplyDeveloperPayload(ctx, *packageID, parsed); err != nil {
				return err
			}
		}

		if parsed.HasLocation() && (row.Kind == "new" || parsed.UpdateLocation) {
			cityKey, region, lat, lng := parsed.CityKey, parsed.Region, parsed.Lat, parsed.Lng
			if parsed.City != "" {
				location, ok := domain.ResolveCity(parsed.CountryCode, parsed.City)
				if !ok {
					return domain.RejectedDetail("city_not_found", parsed.CountryCode)
				}
				cityKey, region, lat, lng = location.Key, location.Region, location.Lat, location.Lng
			}
			if region == "" {
				region = domain.RegionForCountry(parsed.CountryCode)
			}
			if err := txStore.UpsertPackageLocation(ctx, *packageID, parsed.CountryCode, parsed.City, cityKey, region, lat, lng, parsed.Remote, "self", "approved", "self_reported", "", in.ReviewerID); err != nil {
				return err
			}
		}

		if in.Feature != nil {
			submissionID := row.ID
			if *in.Feature {
				// A pick curated on the picks page, or owned by another submission, stays put.
				if err := txStore.FeatureDeveloperPick(ctx, *packageID, submissionID, in.Quote, in.Sort); err != nil {
					return err
				}
			} else if err := txStore.DeactivateDeveloperPick(ctx, *packageID, &submissionID); err != nil {
				return err
			}
		}

		updated, err := txStore.UpdateDeveloperSubmission(ctx, row.ID, in.Status, in.Note, payload, packageID, in.ReviewerID)
		if err != nil {
			return err
		}
		if in.Status != row.Status {
			if err := txStore.AppendDeveloperSubmissionEvent(ctx, row.ID, in.Status, eventNote(in.Note), "admin"); err != nil {
				return err
			}
		}
		out = updated
		return nil
	})
	return out, err
}

// DeveloperReviewInput is one admin decision that is not a publish.
type DeveloperReviewInput struct {
	SubmissionID int64
	Status       string
	Note         *string
	Payload      json.RawMessage
	PackageID    *int64
	ReviewerID   *int64
}

// ReviewDeveloperSubmission stores a decision and its timeline row in one
// transaction. Leaving `published` also takes the public surface down, so a
// rejected or reopened submission cannot stay live in the catalog, on the map
// or in Discover.
func (s *Store) ReviewDeveloperSubmission(ctx context.Context, in DeveloperReviewInput) (DeveloperSubmission, error) {
	var out DeveloperSubmission
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		txStore := &Store{DB: tx}
		var row DeveloperSubmission
		if err := tx.Raw(`SELECT `+developerSubmissionColumns+` FROM developer_submissions WHERE id = ? FOR UPDATE`, in.SubmissionID).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == 0 {
			return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
		}
		if row.Status == "published" && in.Status != "published" {
			if err := txStore.withdrawDeveloperSurface(ctx, row); err != nil {
				return err
			}
		}
		updated, err := txStore.UpdateDeveloperSubmission(ctx, row.ID, in.Status, in.Note, in.Payload, in.PackageID, in.ReviewerID)
		if err != nil {
			return err
		}
		if in.Status != row.Status {
			if err := txStore.AppendDeveloperSubmissionEvent(ctx, row.ID, in.Status, eventNote(in.Note), "admin"); err != nil {
				return err
			}
		}
		out = updated
		return nil
	})
	return out, err
}

// eventNote keeps empty notes out of the timeline instead of storing "".
func eventNote(note *string) *string {
	if note == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*note)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// withdrawDeveloperSurface undoes what publishing made public: this submission's
// Discover pick, the map point it wrote, and a link package the submission itself
// created. A claim leaves the existing package, and a point it never touched, in place.
func (s *Store) withdrawDeveloperSurface(ctx context.Context, row DeveloperSubmission) error {
	if row.PackageID == nil {
		return nil
	}
	packageID := *row.PackageID
	submissionID := row.ID
	if err := s.DeactivateDeveloperPick(ctx, packageID, &submissionID); err != nil {
		return err
	}
	// Publishing writes the point only for a `new` submission or a claim that turned
	// updateLocation on; anything else never touched the editor's location.
	published := ParseDeveloperPayload(row.Payload)
	if published.HasLocation() && (row.Kind == "new" || published.UpdateLocation) {
		if err := s.DB.WithContext(ctx).Exec(`
			UPDATE package_locations
			SET status = 'rejected', verified_by = NULL, verified_at = NULL, updated_at = now()
			WHERE package_id = ? AND status = 'approved'
		`, packageID).Error; err != nil {
			return err
		}
		if err := QueueCacheInvalidation(ctx, s.DB, fmt.Sprintf("developer:location:%d", packageID), "c:developer-map:*"); err != nil {
			return err
		}
	}
	if row.Kind != "new" {
		return nil
	}
	var live struct {
		Kind    string
		Removed bool
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT kind, removed_at IS NOT NULL AS removed FROM packages WHERE id = ?`, packageID).Scan(&live).Error; err != nil {
		return err
	}
	if live.Kind != "link" || live.Removed {
		return nil
	}
	if err := s.RemoveLinkApp(ctx, packageID); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// restoreDeveloperPackage brings back a link package that withdrawing its `new`
// submission took down, so republishing keeps the token and curated data.
func (s *Store) restoreDeveloperPackage(ctx context.Context, packageID int64) error {
	var removed bool
	if err := s.DB.WithContext(ctx).Raw(`SELECT kind = 'link' AND removed_at IS NOT NULL FROM packages WHERE id = ?`, packageID).Scan(&removed).Error; err != nil {
		return err
	}
	if !removed {
		return nil
	}
	return s.RestoreLinkApp(ctx, packageID)
}
