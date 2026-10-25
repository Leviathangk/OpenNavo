package catalog

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/seo"
	"gorm.io/gorm"
)

func (s *PublicService) BackgroundItem(ctx context.Context, label, locale string) (map[string]any, error) {
	if !facts.ValidBackgroundLabel(label) {
		return nil, domain.Validation()
	}
	settings, err := s.Store.SEOSettings(ctx)
	if err != nil {
		return nil, err
	}
	return s.backgroundItem(ctx, label, locale, settings.Settings.Pages.OpenLocales)
}

// backgroundItem is one label's page. It is indexable only in a language open for indexing (open, the
// pages.openLocales list), with a verified description in that language and a catalog app still declaring
// it; closed languages are left out of its locales too.
func (s *PublicService) backgroundItem(ctx context.Context, label, locale string, open []string) (map[string]any, error) {
	owners, err := s.Store.BackgroundOwners(ctx, label)
	if err != nil {
		return nil, err
	}
	if len(owners) == 0 {
		return nil, notFound(gorm.ErrRecordNotFound)
	}
	ids := []int64{}
	visibleIDs := []int64{}
	for _, row := range owners {
		if !slices.Contains(ids, row.PackageID) {
			ids = append(ids, row.PackageID)
		}
		if row.Visible && !slices.Contains(visibleIDs, row.PackageID) {
			visibleIDs = append(visibleIDs, row.PackageID)
		}
	}
	summaries, err := s.SummariesByID(ctx, visibleIDs, locale)
	if err != nil {
		return nil, err
	}
	apps := []any{}
	developers := []string{}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	freshOwner := false
	for _, row := range owners {
		pkg, ok := summaries[row.PackageID]
		if !ok || !row.Visible {
			continue
		}
		freshOwner = freshOwner || facts.NewProof(row.SourceURL, row.CheckedAt, facts.StaleAfter, "verified", now).Status == "verified"
		apps = append(apps, map[string]any{"package": pkg, "kind": row.Kind, "phase": row.Phase, "evidence": facts.NewProof(row.SourceURL, row.CheckedAt, facts.StaleAfter, "verified", now), "uninstallUrl": "opennavo://uninstall/" + pkg.Token + "?preview=1"})
		if row.Developer != nil {
			developer := strings.TrimSpace(*row.Developer)
			if developer != "" && !slices.Contains(developers, developer) {
				developers = append(developers, developer)
			}
		}
	}
	slices.Sort(developers)
	content, err := s.Store.BackgroundContent(ctx, label, locale)
	if err != nil {
		return nil, err
	}
	indexable := false
	if content != nil {
		content.Evidence = content.Evidence.At(now)
		indexable = strings.TrimSpace(content.Description) != "" && content.Evidence.Status == "verified" && content.Locale == locale && freshOwner && seo.LocaleOpen(open, locale)
	}
	// The label's hreflang candidates: ready languages that are open for indexing.
	locales := []string{}
	if freshOwner {
		ready, err := s.Store.BackgroundReadyLocales(ctx, label, now)
		if err != nil {
			return nil, err
		}
		for _, code := range ready {
			if seo.LocaleOpen(open, code) {
				locales = append(locales, code)
			}
		}
	}
	return map[string]any{"label": label, "apps": apps, "developers": developers, "shared": len(ids) > 1, "ownership": "declared_candidates", "identityChecks": []string{"signature", "path", "version"}, "content": content, "indexable": indexable, "locales": locales, "startupUrl": "opennavo://startup?" + url.Values{"label": {label}}.Encode()}, nil
}
func (s *PublicService) BackgroundItems(ctx context.Context, q, locale string, current, size int) (map[string]any, error) {
	if !ValidPage(current, size, 100) || len(q) > 255 {
		return nil, domain.Validation()
	}
	labels, total, err := s.Store.BackgroundLabels(ctx, q, current, size)
	if err != nil {
		return nil, err
	}
	settings, err := s.Store.SEOSettings(ctx)
	if err != nil {
		return nil, err
	}
	records := []any{}
	for _, label := range labels {
		item, err := s.backgroundItem(ctx, label, locale, settings.Settings.Pages.OpenLocales)
		if err != nil {
			return nil, err
		}
		records = append(records, item)
	}
	return map[string]any{"current": current, "size": size, "total": total, "records": records}, nil
}
