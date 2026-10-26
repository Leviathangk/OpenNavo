package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// packageSEO reads and writes the optional title and description of an app's overview page per language.
// The templates stay the default; zh-Hant is derived from zh-CN's override.
func (s *Service) packageSEO(ctx context.Context, op string, in Input) (any, error) {
	if op == "GetPackageSeo" {
		return s.packageSEOView(ctx, in)
	}
	return s.setPackageSEO(ctx, in)
}

// seoRequest decodes {locale,title?,description?}: an omitted field keeps its value, null or "" clears it.
func seoRequest(body map[string]any) (string, store.SEOText, store.SEOText, error) {
	var title, description store.SEOText
	raw, err := json.Marshal(body)
	if err != nil {
		return "", title, description, domain.Rejected("body_invalid")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", title, description, domain.Rejected("body_invalid")
	}
	locale := ""
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys) // the same request always names the same field
	for _, key := range keys {
		value := fields[key]
		switch key {
		case "locale":
			if json.Unmarshal(value, &locale) != nil {
				return "", title, description, domain.Rejected("field_invalid:locale")
			}
		case "title", "description":
			var text *string
			if json.Unmarshal(value, &text) != nil {
				return "", title, description, domain.Rejected("field_invalid:" + key)
			}
			field := store.SEOText{Set: true, Value: text}
			if key == "title" {
				title = field
			} else {
				description = field
			}
		default:
			return "", title, description, unknownField(key)
		}
	}
	if locale == "zh-Hant" {
		return "", title, description, domain.Rejected("derived_locale_read_only:zh-Hant:zh-CN")
	}
	if !store.PackageSEOLocale(locale) {
		return "", title, description, domain.Rejected("locale_invalid:locale")
	}
	if !title.Set && !description.Set {
		return "", title, description, domain.Rejected("field_required:title")
	}
	if title.Value, err = store.NormalizeSEOText(title.Value, store.SEOTitleMaxChars, "title"); err != nil {
		return "", title, description, err
	}
	if description.Value, err = store.NormalizeSEOText(description.Value, store.SEODescriptionMaxChars, "description"); err != nil {
		return "", title, description, err
	}
	return locale, title, description, nil
}

func (s *Service) setPackageSEO(ctx context.Context, in Input) (any, error) {
	locale, title, description, err := seoRequest(in.Body)
	if err != nil {
		return nil, err
	}
	op := domain.CurrentOperation(ctx)
	var result any
	err = s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		local := *s
		local.Store = &store.Store{DB: tx, Redis: s.Store.Redis}
		if err := local.checkAdminRevision(ctx, "SetPackageSeo", in); err != nil {
			return err
		}
		before, err := local.Store.PackageSEOOverride(ctx, in.ID, locale)
		if err != nil {
			return err
		}
		snapshot, err := local.packagePageSnapshot(ctx, "package_seo", in)
		if err != nil {
			return err
		}
		changed, err := local.Store.SetPackageSEO(ctx, in.ID, locale, title, description, writeOwner(ctx))
		if err != nil {
			return err
		}
		after, err := local.Store.PackageSEOOverride(ctx, in.ID, locale)
		if err != nil {
			return err
		}
		view, err := local.packageSEOView(ctx, in)
		if err != nil {
			return err
		}
		result = view
		if op.DryRun && !agentTransaction(ctx) {
			next, err := local.packagePageSnapshot(ctx, "package_seo", in)
			if err != nil {
				return err
			}
			result = map[string]any{"dryRun": true, "requestId": op.RequestID, "affectedUrls": overviewURLs(s.Config.WebBaseURL, fmt.Sprint(view["token"]), locale),
				"changes": []any{map[string]any{"object": map[string]any{"entity": "package_seo", "objectKey": fmt.Sprint(in.ID)}, "action": "update", "before": snapshot, "after": next}}}
			return errPreviewRollback
		}
		if !changed {
			return nil
		}
		return store.Audit(ctx, tx, in.Actor, "SetPackageSeo", "package", fmt.Sprint(in.ID), seoOverrideAudit(before), seoOverrideAudit(after))
	})
	if errors.Is(err, errPreviewRollback) {
		return result, nil
	}
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	return result, nil
}

func seoOverrideAudit(row *store.PackageSEO) any {
	if row == nil {
		return nil
	}
	return map[string]any{"locale": row.Locale, "title": row.Title, "description": row.Description}
}

// overviewURLs is the overview page an override of locale shows on.
func overviewURLs(siteURL, token, locale string) []string {
	return []string{strings.TrimRight(siteURL, "/") + seo.OverviewPath(seo.Facts{Token: token}, locale)}
}

// packageSEOView lists an app's overrides in site language order (zh-Hant derived from zh-CN, read-only)
// with the revision SetPackageSeo checks.
func (s *Service) packageSEOView(ctx context.Context, in Input) (map[string]any, error) {
	// Removed apps have no overview: reads answer 404 like writes.
	var p struct{ Token string }
	if err := s.Store.DB.WithContext(ctx).Raw("SELECT token FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL", in.ID).Scan(&p).Error; err != nil {
		return nil, err
	}
	if p.Token == "" {
		return nil, fail(domain.CodeNotFound)
	}
	rows, err := s.Store.PackageSEOOverrides(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	revision, err := s.CurrentRevision(ctx, "SetPackageSeo", Input{ID: in.ID})
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, row := range rows {
		items = append(items, map[string]any{"locale": row.Locale, "title": row.Title, "description": row.Description, "derived": row.Derived,
			"updatedAt": row.UpdatedAt.UTC().Format(time.RFC3339), "updatedBy": row.UpdatedBy})
	}
	return map[string]any{"packageId": in.ID, "token": p.Token, "revision": revision, "items": items}, nil
}
