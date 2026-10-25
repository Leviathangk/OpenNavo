package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"gorm.io/gorm"
)

// Translate work order: published articles first, because their other languages
// stay noindex until translated; then shared site content; packages follow by
// popularity, and the opportunity job later raises package priority by installs.
var translationPriority = map[string]float64{"article": 150, "category": 100, "collection": 90, "feature": 80, "collection_item": 70, "package": 10}

var translationBackfill = []struct{ Entity, Query string }{
	{"category", "SELECT id,0 secondary_id FROM categories WHERE applies_to<>'formula' ORDER BY sort,id"},
	{"collection", "SELECT id,0 secondary_id FROM collections ORDER BY id"},
	{"feature", "SELECT id,0 secondary_id FROM features ORDER BY id"},
	{"collection_item", "SELECT ci.collection_id id,ci.package_id secondary_id FROM collection_items ci JOIN packages p ON p.id=ci.package_id WHERE p.removed_at IS NULL ORDER BY ci.collection_id,ci.sort,ci.package_id"},
	{"package", "SELECT p.id,0 secondary_id FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.kind IN ('cask','link') AND p.removed_at IS NULL AND NOT (p.disabled OR p.stale_disabled) AND NOT COALESCE(m.hidden,false) ORDER BY p.popularity DESC,p.id"},
}

// BackfillTranslationTasks queues translate tasks for public catalog content whose
// site languages are missing or older than the current source text. A source
// edit already queues every site language; content written before a language
// was added never got that work. Languages closed for indexing (pages.openLocales)
// get no new work. It writes tasks only, never content, and leaves existing tasks
// as they are.
func (s *Store) BackfillTranslationTasks(ctx context.Context) (map[string]int, error) {
	queued := map[string]int{}
	settings, err := s.SEOSettings(ctx)
	if err != nil {
		return queued, err
	}
	for _, group := range translationBackfill {
		var rows []struct{ ID, SecondaryID int64 }
		if err := s.DB.WithContext(ctx).Raw(group.Query).Scan(&rows).Error; err != nil {
			return queued, err
		}
		for _, row := range rows {
			ref := content.Ref{Entity: group.Entity, ID: row.ID, SecondaryID: row.SecondaryID}
			source, err := s.ReadContentSource(ctx, ref)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return queued, err
			}
			if len(content.Flatten(source.Fields)) == 0 {
				continue
			}
			locales := make([]string, 0, len(source.Targets))
			for locale := range source.Targets {
				if seo.TranslationOpen(group.Entity, settings.Settings.Pages.OpenLocales, locale) {
					locales = append(locales, locale)
				}
			}
			sort.Strings(locales)
			entity := ref.Entity + ":" + ref.Key()
			evidence, _ := json.Marshal(map[string]any{"ref": ref, "sourceLocale": source.SourceLocale, "sourceHash": source.Hash})
			for _, locale := range locales {
				r := s.DB.WithContext(ctx).Exec(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version) VALUES('translate',?,?,?,?,?::jsonb,?,'translation-v1') ON CONFLICT(dedupe_key) DO NOTHING`,
					entity, locale, "translate:"+entity+":"+locale+":"+source.Hash, translationPriority[group.Entity], string(evidence), source.Hash)
				if r.Error != nil {
					return queued, r.Error
				}
				queued[group.Entity] += int(r.RowsAffected)
			}
		}
	}
	return queued, nil
}

// BackfillTranslationTasksForLocales runs the backfill once per set of site
// languages open for indexing. Languages are added by a deploy and opened in the
// SEO settings, so the worker calls this on start and later starts skip the scan:
// a language opened later gets its work at the next start. A failed or
// interrupted run is retried on the next start.
func (s *Store) BackfillTranslationTasksForLocales(ctx context.Context) (map[string]int, bool, error) {
	settings, err := s.SEOSettings(ctx)
	if err != nil {
		return nil, false, err
	}
	codes := make([]string, 0, len(i18n.SiteLocales))
	for _, code := range i18n.SiteLocales {
		if seo.LocaleOpen(settings.Settings.Pages.OpenLocales, string(code)) {
			codes = append(codes, string(code))
		}
	}
	// v2 includes the Simplified Chinese dependency of Traditional Chinese.
	signature := "translation-backfill-v2:" + strings.Join(codes, ",")
	if err := s.DB.WithContext(ctx).Exec("INSERT INTO seo_opportunity_cursors(source) VALUES('translation_locales') ON CONFLICT DO NOTHING").Error; err != nil {
		return nil, false, err
	}
	var done string
	if err := s.DB.WithContext(ctx).Raw("SELECT signature FROM seo_opportunity_cursors WHERE source='translation_locales'").Scan(&done).Error; err != nil {
		return nil, false, err
	}
	if done == signature {
		return nil, false, nil
	}
	queued, err := s.BackfillTranslationTasks(ctx)
	if err != nil {
		return queued, true, err
	}
	return queued, true, s.DB.WithContext(ctx).Exec("UPDATE seo_opportunity_cursors SET signature=?,updated_at=clock_timestamp() WHERE source='translation_locales'", signature).Error
}
