package catalog

import (
	"context"

	"github.com/opennavo/opennavo/server/internal/facts"
)

func (s *PublicService) collectionSetup(ctx context.Context, slug, locale string) (map[string]any, error) {
	now := s.now()
	packages, err := s.Store.CollectionSetupPackages(ctx, slug, now)
	if err != nil {
		return nil, err
	}
	tokens := []string{}
	external := []any{}
	compatibility := []any{}
	prices := [][]facts.PriceFact{}
	for _, p := range packages {
		if facts.ClientCaskToken(p.Kind, p.Token) {
			tokens = append(tokens, p.Token)
		} else if p.Kind == "link" && p.Homepage != nil && facts.ValidSourceURL(*p.Homepage) {
			external = append(external, map[string]any{"token": p.Token, "url": *p.Homepage})
		}
		compatibility = append(compatibility, map[string]any{"token": p.Token, "requirements": p.Requirements})
		prices = append(prices, p.Prices)
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
