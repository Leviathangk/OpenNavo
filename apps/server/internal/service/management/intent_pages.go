package management

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// Intent pages (alternatives, uninstall) open by an editorial switch per app, kind and language. The server
// keeps pages.intentPagesEnabled and the content gates: three reasoned alternatives, uninstall steps from a
// cask. zh-Hant follows zh-CN's switch.
func (s *Service) intentPages(ctx context.Context, op string, in Input) (any, error) {
	switch op {
	case "GetPackageIntentPages":
		return s.intentPagesView(ctx, in)
	case "SetPackageIntentPages":
		return s.setIntentPages(ctx, in)
	default:
		return s.listIntentPages(ctx, in)
	}
}

type intentPageItem struct {
	PageKind string `json:"pageKind"`
	Locale   string `json:"locale"`
	Enabled  *bool  `json:"enabled"`
}

// decodeIntentChanges reads {items:[{pageKind,locale,enabled}]}: one to twenty distinct switches.
func decodeIntentChanges(body map[string]any) ([]store.IntentPageChange, error) {
	var request struct {
		Items []intentPageItem `json:"items"`
	}
	if err := decodeWriteBody(body, &request); err != nil {
		return nil, err
	}
	if len(request.Items) == 0 || len(request.Items) > 2*len(seo.Locales) {
		return nil, domain.Rejected("items_invalid:items")
	}
	seen := map[string]bool{}
	changes := []store.IntentPageChange{}
	for _, item := range request.Items {
		if item.Enabled == nil {
			return nil, domain.Rejected("field_required:items.enabled")
		}
		key := item.PageKind + ":" + item.Locale
		if seen[key] {
			return nil, domain.Rejected("items_duplicate:items")
		}
		seen[key] = true
		changes = append(changes, store.IntentPageChange{PageKind: item.PageKind, Locale: item.Locale, Enabled: *item.Enabled})
	}
	return changes, nil
}

func (s *Service) setIntentPages(ctx context.Context, in Input) (any, error) {
	changes, err := decodeIntentChanges(in.Body)
	if err != nil {
		return nil, err
	}
	op := domain.CurrentOperation(ctx)
	var result any
	err = s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		local := *s
		local.Store = &store.Store{DB: tx, Redis: s.Store.Redis}
		if err := local.checkAdminRevision(ctx, "SetPackageIntentPages", in); err != nil {
			return err
		}
		before, err := local.Store.IntentPages(ctx, []int64{in.ID})
		if err != nil {
			return err
		}
		snapshot, err := local.packagePageSnapshot(ctx, "intent_pages", in)
		if err != nil {
			return err
		}
		changed, err := local.Store.SetIntentPages(ctx, in.ID, changes, writeOwner(ctx))
		if err != nil {
			return err
		}
		view, err := local.intentPagesView(ctx, in)
		if err != nil {
			return err
		}
		result = view
		if op.DryRun && !agentTransaction(ctx) {
			after, err := local.packagePageSnapshot(ctx, "intent_pages", in)
			if err != nil {
				return err
			}
			result = map[string]any{"dryRun": true, "requestId": op.RequestID, "affectedUrls": intentURLs(s.Config.WebBaseURL, fmt.Sprint(view["token"]), changes),
				"changes": []any{map[string]any{"object": map[string]any{"entity": "intent_pages", "objectKey": fmt.Sprint(in.ID)}, "action": "update", "before": snapshot, "after": after}}}
			return errPreviewRollback
		}
		if !changed {
			return nil
		}
		after, err := local.Store.IntentPages(ctx, []int64{in.ID})
		if err != nil {
			return err
		}
		return store.Audit(ctx, tx, in.Actor, "SetPackageIntentPages", "package", fmt.Sprint(in.ID), intentSwitches(before), intentSwitches(after))
	})
	if errors.Is(err, errPreviewRollback) {
		return result, nil
	}
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	return result, nil
}

// intentURLs are the pages a switch change affects: each requested page, and its zh-Hant copy when the
// change is to zh-CN.
func intentURLs(siteURL, token string, changes []store.IntentPageChange) []string {
	base, row := strings.TrimRight(siteURL, "/"), store.IndexRow{Token: token}
	urls := []string{}
	for _, change := range changes {
		locales := []string{change.Locale}
		if change.Locale == store.IntentDerivedSource {
			locales = append(locales, store.IntentDerivedLocale)
		}
		for _, locale := range locales {
			if url := base + store.IntentPagePath(row, seo.PageKind(change.PageKind), locale); !slices.Contains(urls, url) {
				urls = append(urls, url)
			}
		}
	}
	return urls
}

// intentSwitches is the audit and snapshot form of open switches.
func intentSwitches(rows []store.IntentPage) []map[string]any {
	out := []map[string]any{}
	for _, row := range rows {
		out = append(out, map[string]any{"pageKind": row.PageKind, "locale": row.Locale, "createdAt": row.CreatedAt.UTC().Format(time.RFC3339), "createdBy": row.CreatedBy})
	}
	return out
}

// checkAdminRevision compares the X-Expected-Revision an admin session sent with the write's current
// revision under the catalog lock. Agent writes are checked by the write layer (catalogEdit) instead.
func (s *Service) checkAdminRevision(ctx context.Context, op string, in Input) error {
	if agentTransaction(ctx) {
		return nil
	}
	if err := store.LockCatalogWrites(ctx, s.Store.DB); err != nil {
		return err
	}
	raw, present := in.Params["x-expected-revision"]
	if !present || raw == nil {
		return nil
	}
	expected, ok := raw.(float64)
	if !ok || expected < 0 || expected != math.Trunc(expected) {
		return domain.Rejected("parameter_invalid:X-Expected-Revision")
	}
	current, err := s.CurrentRevision(ctx, op, Input{ID: in.ID})
	if err != nil {
		return err
	}
	if current != int64(expected) {
		return agentgate.RevisionConflict(current)
	}
	return nil
}

// intentPagesView is the full matrix of an app's intent pages, both kinds in every site language: its
// switch (zh-Hant derived from zh-CN, read-only), whether the app has the content, and the page's final
// decision, which also applies pages.intentPagesEnabled and the languages open for indexing. Link apps have
// no uninstall page. The revision is the one SetPackageIntentPages checks.
func (s *Service) intentPagesView(ctx context.Context, in Input) (map[string]any, error) {
	row, found, err := s.Store.IndexRowByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(domain.CodeNotFound)
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, err
	}
	switches, err := s.Store.IntentPages(ctx, []int64{row.ID})
	if err != nil {
		return nil, err
	}
	// The content is read once: every item shows it, and the decisions apply it to the open switches.
	rows := []store.IndexRow{row}
	contents, err := s.Store.IntentContents(ctx, rows)
	if err != nil {
		return nil, err
	}
	content := contents[row.ID]
	if policy.IntentPagesEnabled {
		store.ApplyIntentSwitches(rows, switches, contents)
	}
	revision, err := s.CurrentRevision(ctx, "SetPackageIntentPages", Input{ID: row.ID})
	if err != nil {
		return nil, err
	}
	open := map[string]store.IntentPage{}
	for _, item := range switches {
		open[item.PageKind+":"+item.Locale] = item
	}
	base := strings.TrimRight(s.Config.WebBaseURL, "/")
	items := []any{}
	for _, kind := range store.IntentPageKinds {
		for _, locale := range seo.Locales {
			source, derived := locale, locale == store.IntentDerivedLocale
			if derived {
				source = store.IntentDerivedSource
			}
			item, enabled := open[string(kind)+":"+source]
			decision := seo.DecidePage(rows[0].Facts(), seo.PageRef{Kind: kind, Locale: locale, Page: 1}, policy)
			view := map[string]any{"pageKind": string(kind), "locale": locale, "enabled": enabled, "derived": derived,
				"contentEligible": content[kind], "indexable": seo.DecisionRobots(string(decision.Robots)).Index, "reason": nullableText(decision.Reason),
				"url": base + store.IntentPagePath(row, kind, locale), "enabledAt": nil, "enabledBy": nil}
			if enabled {
				view["enabledAt"], view["enabledBy"] = item.CreatedAt.UTC().Format(time.RFC3339), item.CreatedBy
			}
			items = append(items, view)
		}
	}
	return map[string]any{"packageId": row.ID, "token": row.Token, "revision": revision, "globalEnabled": policy.IntentPagesEnabled, "items": items}, nil
}

// listIntentPages pages the open switches of the site with each page's content gate and final decision.
func (s *Service) listIntentPages(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	kind, locale := text(in.Params, "pagekind"), text(in.Params, "locale")
	if kind != "" && !store.ValidIntentPageKind(kind) {
		return nil, domain.Rejected("page_kind_invalid:pageKind")
	}
	if locale != "" && !i18n.Valid(locale) {
		return nil, domain.Rejected("locale_invalid:locale")
	}
	if locale == store.IntentDerivedLocale {
		// The derived language has the switches of its source.
		locale = store.IntentDerivedSource
	}
	switches, total, err := s.Store.ListIntentPages(ctx, kind, locale, current, size)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, item := range switches {
		if !slices.Contains(ids, item.PackageID) {
			ids = append(ids, item.PackageID)
		}
	}
	rows, err := s.Store.IndexRowsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, err
	}
	// Decisions need every switch of the listed apps, not only the page's; the content is read once.
	all, err := s.Store.IntentPages(ctx, ids)
	if err != nil {
		return nil, err
	}
	content, err := s.Store.IntentContents(ctx, rows)
	if err != nil {
		return nil, err
	}
	if policy.IntentPagesEnabled {
		store.ApplyIntentSwitches(rows, all, content)
	}
	byID := map[int64]store.IndexRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	base := strings.TrimRight(s.Config.WebBaseURL, "/")
	records := []any{}
	for _, item := range switches {
		row, ok := byID[item.PackageID]
		if !ok {
			continue
		}
		pageLocale := item.Locale
		if text(in.Params, "locale") == store.IntentDerivedLocale {
			pageLocale = store.IntentDerivedLocale
		}
		decision := seo.DecidePage(row.Facts(), seo.PageRef{Kind: seo.PageKind(item.PageKind), Locale: pageLocale, Page: 1}, policy)
		records = append(records, map[string]any{"packageId": row.ID, "token": row.Token, "name": row.DisplayName, "kind": row.Kind,
			"pageKind": item.PageKind, "locale": pageLocale, "contentEligible": content[row.ID][seo.PageKind(item.PageKind)],
			"indexable": seo.DecisionRobots(string(decision.Robots)).Index, "reason": nullableText(decision.Reason),
			"url": base + store.IntentPagePath(row, seo.PageKind(item.PageKind), pageLocale), "enabledAt": item.CreatedAt.UTC().Format(time.RFC3339),
			"enabledBy": item.CreatedBy, "derived": pageLocale != item.Locale})
	}
	return map[string]any{"current": current, "size": size, "total": total, "globalEnabled": policy.IntentPagesEnabled, "records": records}, nil
}
