package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/seo"
)

// IndexRow is one cask or link with what the index rule (WP11) and the sitemap need. Text counts as
// published in a language when its summary is non-empty and its status is source, machine or manual.
// Disabled and Deprecated are Homebrew's flags; Stale is OpenNavo's 90-day rule (stale_disabled),
// which the catalog shows as disabled but which does not stop Homebrew from installing the app.
type IndexRow struct {
	TroubleshootingIssues                       map[string]int
	IntentLocales                               map[seo.PageKind][]string
	ID                                          int64
	Kind, Token, Name, DisplayName, Version     string
	SourceLocale                                string
	Summary                                     string
	Description                                 string
	HasSubstantiveContent, HasDependencyContent bool
	Hidden, Disabled, HasRequirements, IsFont   bool
	Deprecated, Stale                           bool
	Installs30d, NotesCount                     int
	FirstSeenAt, UpdatedAt                      time.Time
	Override                                    string
	OverrideReason                              *string
	OverrideAt                                  *time.Time
	CommittedAt, ScreenshotsAt                  *time.Time
	PublishedLocales                            []string
	ReadyLocales                                []string
	NotesPages                                  map[int]map[string]seo.NotesFacts
	// TextUpdatedAt holds the update time of each published language's text.
	TextUpdatedAt map[string]time.Time
}

const indexOverviewColumns = `p.id,p.kind,p.token,p.name,p.version,p.source_locale,p.installs_30d,p.first_seen_at,p.updated_at,p.is_font,
 COALESCE(NULLIF(btrim(si.summary),''),p.desc_en,'') AS summary,
 COALESCE(si.description,'') AS description,
 EXISTS(SELECT 1 FROM package_screenshots sc WHERE sc.package_id=p.id) AS has_substantive_content,
 COALESCE(NULLIF(si.display_name,''),p.name) AS display_name,
 COALESCE(m.hidden,false) AS hidden,p.disabled,p.deprecated,p.stale_disabled AS stale,
 (p.min_macos IS NOT NULL OR p.raw->'depends_on'->'macos' IS NOT NULL) AS has_requirements,
 COALESCE(m.index_override,'auto') AS override,m.index_override_reason AS override_reason,m.index_override_at AS override_at,
 COALESCE((SELECT json_object_agg(t.locale,t.updated_at) FROM package_i18n t WHERE t.package_id=p.id AND NULLIF(btrim(t.summary),'') IS NOT NULL AND t.status IN ('source','machine','manual')),'{}')::text AS text_times,
 COALESCE((SELECT json_agg(t.locale) FROM package_i18n t WHERE t.package_id=p.id
 AND NULLIF(btrim(t.summary),'') IS NOT NULL AND NULLIF(btrim(t.description),'') IS NOT NULL
 AND t.status IN ('source','machine','manual') AND t.source_hash ~ '^[0-9a-f]{64}$'),'[]')::text AS ready_languages
`
const indexJoins = ` FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN package_i18n si ON si.package_id=p.id AND si.locale=p.source_locale`
const indexOverviewSelect = `SELECT ` + indexOverviewColumns + indexJoins

// Detail/sitemap readers also need dependency and timestamp facts. Overview-only
// directory/report queries avoid release history and reverse dependency work.
const indexRowSelect = `SELECT
 (COALESCE(jsonb_array_length(p.dependencies->'dependsOn'->'cask'),0)>0
 OR COALESCE(jsonb_array_length(p.dependencies->'dependsOn'->'formula'),0)>0
 OR COALESCE(jsonb_array_length(p.conflicts_with->'cask'),0)>0
 OR COALESCE(jsonb_array_length(p.conflicts_with->'formula'),0)>0
 OR EXISTS(SELECT 1 FROM packages dependent WHERE dependent.removed_at IS NULL
 AND dependent.dependencies->'dependsOn'->'cask' @> jsonb_build_array(p.token))) AS has_dependency_content,
 (SELECT v.brew_committed_at FROM package_versions v WHERE v.package_id=p.id AND v.version=p.version) AS committed_at,
 (SELECT max(sc.created_at) FROM package_screenshots sc WHERE sc.package_id=p.id) AS screenshots_at,
 (SELECT count(*) FROM releases r WHERE r.package_id=p.id AND NOT r.hidden AND NULLIF(btrim(r.body_markdown),'') IS NOT NULL) AS notes_count,
` + indexOverviewColumns + indexJoins

type indexRowScan struct {
	ID                                          int64
	Kind, Token, Name, DisplayName, Version     string
	SourceLocale                                string
	Summary                                     string
	Description                                 string
	HasSubstantiveContent, HasDependencyContent bool
	Hidden, Disabled, HasRequirements, IsFont   bool
	Deprecated, Stale                           bool
	Installs30d, NotesCount                     int
	FirstSeenAt, UpdatedAt                      time.Time
	Override                                    string
	OverrideReason                              *string
	OverrideAt                                  *time.Time
	CommittedAt, ScreenshotsAt                  *time.Time
	TextTimes                                   string
	ReadyLanguages                              string
}

func (s *Store) indexRows(ctx context.Context, condition, tail string, args ...any) ([]IndexRow, error) {
	rows, err := s.indexRowsQuery(ctx, indexRowSelect, condition, tail, args...)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateTroubleshooting(ctx, rows, time.Now()); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) indexRowsQuery(ctx context.Context, query, condition, tail string, args ...any) ([]IndexRow, error) {
	var scanned []indexRowScan
	if err := s.DB.WithContext(ctx).Raw(query+" WHERE p.kind IN ('cask','link') AND p.removed_at IS NULL AND "+condition+" "+tail, args...).Scan(&scanned).Error; err != nil {
		return nil, err
	}
	out := make([]IndexRow, 0, len(scanned))
	for _, row := range scanned {
		r := IndexRow{
			ID: row.ID, Kind: row.Kind, Token: row.Token, Name: row.Name, DisplayName: row.DisplayName, Version: row.Version,
			Description: row.Description, HasSubstantiveContent: row.HasSubstantiveContent, HasDependencyContent: row.HasDependencyContent,
			SourceLocale: row.SourceLocale, Summary: row.Summary, Hidden: row.Hidden, Disabled: row.Disabled,
			Deprecated: row.Deprecated, Stale: row.Stale,
			HasRequirements: row.HasRequirements, IsFont: row.IsFont, Installs30d: row.Installs30d, NotesCount: row.NotesCount,
			FirstSeenAt: row.FirstSeenAt, UpdatedAt: row.UpdatedAt, Override: row.Override, OverrideReason: row.OverrideReason, OverrideAt: row.OverrideAt,
			CommittedAt: row.CommittedAt, ScreenshotsAt: row.ScreenshotsAt, TextUpdatedAt: map[string]time.Time{},
		}
		if err := json.Unmarshal([]byte(row.TextTimes), &r.TextUpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(row.ReadyLanguages), &r.ReadyLocales); err != nil {
			return nil, err
		}
		for _, locale := range seo.Locales {
			if _, ok := r.TextUpdatedAt[locale]; ok {
				r.PublishedLocales = append(r.PublishedLocales, locale)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Facts converts a row for seo.Decide.
func (r IndexRow) Facts() seo.Facts {
	return seo.Facts{TroubleshootingIssues: r.TroubleshootingIssues, IntentLocales: r.IntentLocales,
		Kind: r.Kind, Description: r.Description, HasSubstantiveContent: r.HasSubstantiveContent || strings.TrimSpace(r.Description) != "",
		ReadyLocales: r.ReadyLocales, NotesPages: r.NotesPages, HasDependencyContent: r.HasDependencyContent,
		Hidden: r.Hidden, Disabled: r.Disabled, Deprecated: r.Deprecated, Stale: r.Stale,
		Name: r.Name, Token: r.Token, Summary: r.Summary, Version: r.Version,
		HasRequirements: r.HasRequirements, Installs30d: r.Installs30d, Override: r.Override, FirstSeenAt: r.FirstSeenAt,
		SourceLocale: r.SourceLocale, PublishedLocales: r.PublishedLocales,
	}
}

// Lastmod is the latest reliable content change for a language: the Homebrew commit of the current
// version, screenshots, and that language's published text (the source text for languages that fall
// back to it). version_changed_at and updated_at are sync times, so they never count.
func (r IndexRow) Lastmod(locale string) *time.Time {
	var latest *time.Time
	consider := func(value *time.Time) {
		if value != nil && (latest == nil || value.After(*latest)) {
			copied := value.UTC()
			latest = &copied
		}
	}
	consider(r.CommittedAt)
	consider(r.ScreenshotsAt)
	text, ok := r.TextUpdatedAt[locale]
	if !ok {
		text, ok = r.TextUpdatedAt[r.SourceLocale]
	}
	if ok {
		consider(&text)
	}
	return latest
}

// IndexRowByToken loads one listed or hidden cask; found is false for unknown or removed tokens.
func (s *Store) IndexRowByToken(ctx context.Context, token string) (IndexRow, bool, error) {
	rows, err := s.indexRows(ctx, "p.token=?", "", token)
	if err != nil || len(rows) == 0 {
		return IndexRow{}, false, err
	}
	return rows[0], true, nil
}

// IndexRowByID is IndexRowByToken for admin pages, which address apps by id.
func (s *Store) IndexRowByID(ctx context.Context, id int64) (IndexRow, bool, error) {
	rows, err := s.indexRows(ctx, "p.id=?", "", id)
	if err != nil || len(rows) == 0 {
		return IndexRow{}, false, err
	}
	return rows[0], true, nil
}

// SitemapIndexRows pages the casks the sitemap may list, in stable id order. group is apps, fonts
// or empty for both. Callers apply the policy; total counts candidates, not listed rows.
func (s *Store) SitemapIndexRows(ctx context.Context, group string, current, size int) ([]IndexRow, int, error) {
	condition := "NOT COALESCE(m.hidden,false)"
	switch group {
	case "apps":
		condition += " AND NOT p.is_font"
	case "fonts":
		condition += " AND p.is_font"
	}
	var total int
	if err := s.DB.WithContext(ctx).Raw("SELECT count(*)" + publicJoins + " WHERE p.kind IN ('cask','link') AND p.removed_at IS NULL AND " + condition).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	rows, err := s.indexRows(ctx, condition, "ORDER BY p.id LIMIT ? OFFSET ?", size, (current-1)*size)
	return rows, total, err
}

// IndexReportRows loads only decision facts for report counts and pagination. Unlike sitemap
// rows, these do not need per-package version, screenshot, release or translation aggregates.
func (s *Store) IndexReportRows(ctx context.Context) ([]IndexRow, error) {
	return s.indexRowsQuery(ctx, indexOverviewSelect, "true", "ORDER BY p.first_seen_at DESC,p.id DESC")
}

func (s *Store) IndexPolicy(ctx context.Context) (seo.Policy, error) {
	row, err := s.SEOSettings(ctx)
	if err != nil {
		return seo.Policy{IntentPagesEnabled: row.Settings.Pages.IntentPagesEnabled}, err
	}
	policy := row.Settings.Pages.IndexPolicy
	since, _ := time.Parse(time.RFC3339, policy.Since)
	return seo.Policy{IntentPagesEnabled: row.Settings.Pages.IntentPagesEnabled, Mode: policy.Mode, Scope: policy.Scope, Since: since, VersionsSitemap: row.Settings.Pages.VersionsSitemap, TranslationMode: row.Settings.Pages.TranslationMode, TranslationLocales: row.Settings.Pages.TranslationLocales, VersionsMinNotesChars: row.Settings.Pages.VersionsMinNotesChars}, nil
}
