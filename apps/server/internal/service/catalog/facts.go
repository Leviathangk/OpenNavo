package catalog

import (
	"context"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
)

// appFacts adds the WP12 facts pages may show: only verified evidence, with old evidence flagged,
// supplement conflicts listed and alternatives limited to apps that can be browsed.
func (s *PublicService) appFacts(ctx context.Context, d *publicapi.PackageDetail, id int64, locale string) error {
	now := time.Now()
	current, found, err := s.Store.PackageFacts(ctx, id)
	if err != nil || !found {
		return err
	}
	if p := current.Price; p != nil && p.Shown() {
		price := publicapi.AppPrice{Type: publicapi.AppPriceType(p.Type), Amount: p.Amount, Currency: p.Currency, Region: p.Region, Plan: p.Plan,
			AppVersion: p.AppVersion, SourceUrl: p.SourceURL, Method: publicapi.AppPriceMethod(p.Method), VerifiedAt: p.VerifiedAt,
			Stale: facts.Stale(p.VerifiedAt, now), Offer: facts.OfferReady(p, now)}
		if p.Period != nil {
			period := publicapi.AppPricePeriod(*p.Period)
			price.Period = &period
		}
		d.Price = &price
	}
	if m := current.Macos; m != nil && m.Shown() {
		supplement := publicapi.MacosSupplement{AppVersion: m.AppVersion, SourceUrl: m.SourceURL, Method: publicapi.MacosSupplementMethod(m.Method),
			VerifiedAt: m.VerifiedAt, Stale: facts.Stale(m.VerifiedAt, now), Ranges: []publicapi.MacosRange{}, Conflicts: []publicapi.MacosSupplementConflicts{}}
		for _, r := range m.Ranges {
			supplement.Ranges = append(supplement.Ranges, publicapi.MacosRange{Arch: publicapi.MacosRangeArch(r.Arch), Support: publicapi.MacosRangeSupport(r.Support), Minimum: r.Minimum, Maximum: r.Maximum})
		}
		for _, conflict := range facts.Conflicts(m, current.Homebrew) {
			supplement.Conflicts = append(supplement.Conflicts, publicapi.MacosSupplementConflicts(conflict))
		}
		d.MacosSupplement = &supplement
	}
	rows, err := s.Store.Alternatives(ctx, id, true)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PackageID)
	}
	summaries, err := s.SummariesByID(ctx, ids, locale)
	if err != nil {
		return err
	}
	alternatives := []publicapi.PackageAlternative{}
	for _, row := range rows {
		item, ok := summaries[row.PackageID]
		if !ok {
			continue
		}
		alternative := publicapi.PackageAlternative{Package: item, Reasons: []publicapi.PackageAlternativeReasons{}, Limits: []publicapi.PackageAlternativeLimits{}}
		for _, reason := range row.Reasons {
			alternative.Reasons = append(alternative.Reasons, publicapi.PackageAlternativeReasons(reason))
		}
		for _, limit := range row.Limits {
			alternative.Limits = append(alternative.Limits, publicapi.PackageAlternativeLimits(limit))
		}
		if row.BestFor != nil {
			audience := publicapi.PackageAlternativeBestFor(*row.BestFor)
			alternative.BestFor = &audience
		}
		alternatives = append(alternatives, alternative)
	}
	d.Alternatives = &alternatives
	return nil
}
