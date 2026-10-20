package catalog

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
)

type migrationFact struct {
	facts.Migration
	Evidence facts.Proof `json:"evidence"`
}

func (s *PublicService) alternativeFacts(ctx context.Context, d *publicapi.PackageDetail, id int64, locale string, now time.Time) ([]map[string]any, error) {
	out := []map[string]any{}
	if d.Alternatives == nil {
		return out, nil
	}
	targets, err := s.Store.Alternatives(ctx, id, true)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, v := range targets {
		ids[v.Token] = v.PackageID
	}
	sourceRecords, err := s.Store.FactRecords(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, alternative := range *d.Alternatives {
		targetID := ids[alternative.Package.Token]
		if targetID == 0 {
			continue
		}
		records, err := s.Store.FactRecords(ctx, targetID)
		if err != nil {
			return nil, err
		}
		attrs := []any{}
		truth := map[string]bool{}
		for _, r := range records {
			if r.Kind != "attribute" || r.Evidence.At(now).Status != "verified" {
				continue
			}
			var a facts.Attribute
			if json.Unmarshal(r.Value, &a) != nil || a.Validate() != nil || a.AppVersion != "" && a.AppVersion != alternative.Package.Version {
				continue
			}
			attrs = append(attrs, struct {
				facts.Attribute
				Evidence facts.Proof `json:"evidence"`
			}{a, r.Evidence.At(now)})
			if a.BooleanValue != nil {
				truth[a.Name] = *a.BooleanValue
			}
		}
		target := publicapi.PackageDetail{Version: alternative.Package.Version}
		old, _, err := s.Store.PackageFacts(ctx, targetID)
		if err != nil {
			return nil, err
		}
		if old.Price != nil && old.Price.Shown() {
			raw, _ := json.Marshal(old.Price)
			var p publicapi.AppPrice
			if json.Unmarshal(raw, &p) == nil {
				target.Price = &p
			}
		}
		prices, err := s.priceFacts(ctx, &target, targetID, now)
		if err != nil {
			return nil, err
		}
		verifiedPrices := []facts.PriceFact{}
		for _, p := range prices {
			if p.Evidence.Status == "verified" {
				verifiedPrices = append(verifiedPrices, p)
				truth["free"] = truth["free"] || p.Usage == "free"
				truth["one_time_purchase"] = truth["one_time_purchase"] || p.Usage == "paid" && p.Period == "one_time"
				truth["paid"] = truth["paid"] || p.Usage == "paid"
				truth["subscription"] = truth["subscription"] || p.Usage == "subscription"
			}
		}
		reasons := []publicapi.PackageAlternativeReasons{}
		for _, r := range alternative.Reasons {
			if truth[string(r)] {
				reasons = append(reasons, r)
			}
		}
		limits := []publicapi.PackageAlternativeLimits{}
		for _, r := range alternative.Limits {
			if truth[string(r)] {
				limits = append(limits, r)
			}
		}
		view := map[string]any{"package": alternative.Package, "reasons": reasons, "limits": limits, "bestFor": alternative.BestFor, "attributes": attrs, "prices": verifiedPrices}
		var selected *migrationFact
		for _, r := range sourceRecords {
			if r.Kind != "migration" || r.Evidence.At(now).Status != "verified" {
				continue
			}
			var m facts.Migration
			if json.Unmarshal(r.Value, &m) != nil || m.TargetPackageID != targetID || !slices.Contains([]string{locale, "en-US"}, m.Locale) || m.SourceVersion != "" && m.SourceVersion != d.Version || m.TargetVersion != "" && m.TargetVersion != alternative.Package.Version {
				continue
			}
			valid := len(m.Steps) > 0
			for _, step := range m.Steps {
				valid = valid && step.Evidence.At(now).Status == "verified"
			}
			if valid && (selected == nil || m.Locale == locale) {
				selected = &migrationFact{m, r.Evidence.At(now)}
			}
		}
		if selected != nil {
			view["migration"] = selected
		}
		out = append(out, view)
	}
	return out, nil
}
