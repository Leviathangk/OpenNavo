//go:build integration

package catalog

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/facts"
)

func (s *PublicService) legacyCollectionSetupForTest(ctx context.Context, slug, locale string) (map[string]any, error) {
	rows, err := s.Store.CollectionItems(ctx, slug, locale)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, raw := range rows {
		var row struct{ ID int64 }
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		ids = append(ids, row.ID)
	}
	packages, err := s.Store.PublicPackagesByID(ctx, ids, locale)
	if err != nil {
		return nil, err
	}
	tokens := []string{}
	external := []any{}
	compatibility := []any{}
	prices := [][]facts.PriceFact{}
	now := s.now()
	for _, p := range packages {
		if facts.ClientCaskToken(p.Kind, p.Token) {
			tokens = append(tokens, p.Token)
		} else if p.Kind == "link" && p.Homepage != nil && facts.ValidSourceURL(*p.Homepage) {
			external = append(external, map[string]any{"token": p.Token, "url": *p.Homepage})
		}
		detail, err := s.Detail(ctx, p.Kind, p.Token, locale)
		if err != nil {
			return nil, err
		}
		current := []facts.PriceFact{}
		if detail.Facts != nil {
			if detail.Facts.Prices != nil {
				raw, err := json.Marshal(detail.Facts.Prices)
				if err != nil {
					return nil, err
				}
				if err = json.Unmarshal(raw, &current); err != nil {
					return nil, err
				}
			}
			compatibility = append(compatibility, map[string]any{"token": p.Token, "requirements": detail.Facts.Requirements})
		}
		prices = append(prices, current)
	}
	criteria, err := s.Store.CollectionCriteria(ctx, slug, locale)
	if err != nil {
		return nil, err
	}
	if criteria != nil {
		criteria.Evidence = criteria.Evidence.At(now)
	}
	totals, unknown := facts.PriceTotals(prices, now)
	return map[string]any{"criteria": criteria, "installableTokens": tokens, "externalApps": external, "brewfile": facts.Brewfile(tokens), "priceTotals": totals, "priceUnknownCount": unknown, "compatibility": compatibility}, nil
}
