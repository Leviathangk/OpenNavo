package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
)

// TimelineVersion is one recorded build; the timeline groups builds by base version.
type TimelineVersion struct {
	PackageID                    int64
	ID                           int64
	Version                      string
	VersionBase                  string `gorm:"column:version_base"`
	BrewCommittedAt, FirstSeenAt *time.Time
	CreatedAt                    time.Time
}

// Seen orders builds: Homebrew commit time, then first observation, then recording time. Old
// fetches may have stored a zero commit date, which says nothing about order.
func (v TimelineVersion) Seen() time.Time {
	switch {
	case v.BrewCommittedAt != nil && v.BrewCommittedAt.Year() > 1:
		return *v.BrewCommittedAt
	case v.FirstSeenAt != nil:
		return *v.FirstSeenAt
	}
	return v.CreatedAt
}

type TimelineRelease struct {
	PackageID                             int64
	SourceHash                            *string
	CurrentSourceHash                     string          `gorm:"-" json:"-"`
	SourceFields                          json.RawMessage `json:"-"`
	EditorialUnavailable                  bool            `gorm:"-" json:"-"`
	MachineTranslated                     bool
	ID                                    int64
	PackageVersionID                      *int64
	Version, Source, SourceLocale, Locale string
	TranslatedTitle                       *string
	Title, SourceURL, BodyMarkdown        *string
	PublishedAt                           *time.Time
	IsPrerelease                          bool
	Priority                              int
	TranslationStatus                     *string
	Summary, TranslatedBody               *string
	Sections                              json.RawMessage
}

func (s *Store) TimelineVersions(ctx context.Context, id int64) ([]TimelineVersion, error) {
	return s.timelineVersionsFor(ctx, []int64{id})
}

func (s *Store) timelineVersionsFor(ctx context.Context, ids []int64) ([]TimelineVersion, error) {
	rows := []TimelineVersion{}
	err := s.DB.WithContext(ctx).Raw("SELECT package_id,id,version,version_base,brew_committed_at,first_seen_at,created_at FROM package_versions WHERE package_id IN ?", ids).Scan(&rows).Error
	return rows, err
}
func (s *Store) TimelineReleases(ctx context.Context, id int64, locale string) ([]TimelineRelease, error) {
	return s.timelineReleasesFor(ctx, []int64{id}, locale)
}

func (s *Store) timelineReleasesFor(ctx context.Context, ids []int64, locale string) ([]TimelineRelease, error) {
	rows := []TimelineRelease{}
	err := s.DB.WithContext(ctx).Raw(`SELECT r.package_id,i.source_hash,to_jsonb(src) source_fields,r.id,r.package_version_id,r.version,r.source,r.source_locale,i.locale,i.title translated_title,r.title,r.source_url,r.body_markdown,r.published_at,r.is_prerelease,CASE WHEN r.source='editorial' THEN 0 ELSE COALESCE(s.priority,CASE r.source WHEN 'manual' THEN 10 WHEN 'github_release' THEN 20 WHEN 'sparkle' THEN 30 ELSE 40 END) END priority,i.machine_translated,i.status translation_status,i.summary,i.body_markdown translated_body,i.sections FROM releases r LEFT JOIN release_i18n src ON src.release_id=r.id AND src.locale=r.source_locale AND r.source='editorial' LEFT JOIN changelog_sources s ON s.package_id=r.package_id AND s.type=CASE r.source WHEN 'github_release' THEN 'github_releases' ELSE r.source END LEFT JOIN LATERAL (SELECT t.* FROM release_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE (r.source='editorial' OR t.status='skipped' OR COALESCE(NULLIF(t.title,''),NULLIF(t.summary,''),NULLIF(t.body_markdown,'')) IS NOT NULL OR t.sections<>'[]'::jsonb) AND t.release_id=r.id AND ((r.source='editorial' AND t.locale=lang.requested) OR (r.source<>'editorial' AND t.locale IN (lang.requested,r.source_locale,'en-US'))) ORDER BY `+publicLocaleOrder+` LIMIT 1) i ON TRUE WHERE r.package_id IN ? AND NOT r.hidden ORDER BY priority,r.updated_at DESC,r.id DESC`, locale, ids).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		r := &rows[i]
		if r.Source != "editorial" {
			continue
		}
		if len(r.SourceFields) > 0 && string(r.SourceFields) != "null" {
			var source map[string]any
			if err := json.Unmarshal(r.SourceFields, &source); err != nil {
				return nil, err
			}
			fields := map[string]any{}
			for field := range content.Registry["release"].Fields {
				fields[field] = source[field]
			}
			r.CurrentSourceHash = content.Hash(r.SourceLocale, fields)
		}
		r.SourceFields = nil
		r.Locale = locale
		r.localizeEditorial()
	}
	return rows, nil
}

// editorialTranslationReady uses the same source fingerprint as Hermes writes.
// Hash shape alone cannot prove that a translation still describes this source.
func (r TimelineRelease) editorialTranslationReady() bool {
	if r.Locale == r.SourceLocale {
		return true
	}
	return r.TranslationStatus != nil && (*r.TranslationStatus == "manual" || *r.TranslationStatus == "machine") &&
		r.SourceHash != nil && r.CurrentSourceHash != "" && *r.SourceHash == r.CurrentSourceHash
}

func (r *TimelineRelease) localizeEditorial() {
	if r.Locale == r.SourceLocale {
		if r.TranslationStatus == nil || *r.TranslationStatus != "skipped" {
			status := "source"
			r.TranslationStatus = &status
			r.MachineTranslated = false
		}
		return
	}
	// Even a valid summary-only translation must not fall back to a foreign title/body.
	r.Title, r.BodyMarkdown = nil, nil
	if r.editorialTranslationReady() {
		return
	}
	r.EditorialUnavailable = true
	r.TranslatedTitle, r.TranslatedBody, r.Summary = nil, nil, nil
	r.Sections = nil
	r.MachineTranslated = false
	status := "pending"
	if r.TranslationStatus != nil && (*r.TranslationStatus == "failed" || *r.TranslationStatus == "skipped") {
		status = *r.TranslationStatus
	}
	r.TranslationStatus = &status
}
