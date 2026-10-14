package store

import (
	"context"
	"encoding/json"
	"time"
)

// TimelineVersion is one recorded build; the timeline groups builds by base version.
type TimelineVersion struct {
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
	rows := []TimelineVersion{}
	err := s.DB.WithContext(ctx).Raw("SELECT id,version,version_base,brew_committed_at,first_seen_at,created_at FROM package_versions WHERE package_id=?", id).Scan(&rows).Error
	return rows, err
}
func (s *Store) TimelineReleases(ctx context.Context, id int64, locale string) ([]TimelineRelease, error) {
	rows := []TimelineRelease{}
	err := s.DB.WithContext(ctx).Raw(`SELECT r.id,r.package_version_id,r.version,r.source,r.source_locale,i.locale,i.title translated_title,r.title,r.source_url,r.body_markdown,r.published_at,r.is_prerelease,CASE WHEN r.source='editorial' THEN 0 ELSE COALESCE(s.priority,CASE r.source WHEN 'manual' THEN 10 WHEN 'github_release' THEN 20 WHEN 'sparkle' THEN 30 ELSE 40 END) END priority,i.machine_translated,i.status translation_status,i.summary,i.body_markdown translated_body,i.sections FROM releases r LEFT JOIN changelog_sources s ON s.package_id=r.package_id AND s.type=CASE r.source WHEN 'github_release' THEN 'github_releases' ELSE r.source END LEFT JOIN LATERAL (SELECT t.* FROM release_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE (t.status='skipped' OR COALESCE(NULLIF(t.title,''),NULLIF(t.summary,''),NULLIF(t.body_markdown,'')) IS NOT NULL OR t.sections<>'[]'::jsonb) AND t.release_id=r.id AND t.locale IN (lang.requested,r.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1) i ON TRUE WHERE r.package_id=? AND NOT r.hidden ORDER BY priority,r.updated_at DESC,r.id DESC`, locale, id).Scan(&rows).Error
	return rows, err
}
