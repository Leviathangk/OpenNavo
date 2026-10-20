package catalog

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
)

func (s *PublicService) priceFacts(ctx context.Context, d *publicapi.PackageDetail, id int64, now time.Time) ([]facts.PriceFact, error) {
	records, err := s.Store.FactRecords(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []facts.PriceFact{}
	for _, r := range records {
		if r.Kind != "price" {
			continue
		}
		var price facts.PriceObservation
		if json.Unmarshal(r.Value, &price) != nil || price.Validate() != nil {
			continue
		}
		out = append(out, facts.PriceFact{PriceObservation: price, Evidence: r.Evidence.At(now)})
	}
	// Legacy prices remain readable until Hermes replaces them with dimensioned observations.
	if len(out) == 0 && d.Price != nil {
		p := d.Price
		period := "unknown"
		if p.Period != nil {
			period = map[string]string{"one_time": "one_time", "month": "monthly", "year": "yearly"}[string(*p.Period)]
		}
		if p.Type == "free" {
			period = "free"
		}
		region := "ZZ"
		if p.Region != nil {
			region = *p.Region
		}
		edition := "unspecified"
		if p.Plan != nil {
			edition = *p.Plan
		}
		value := facts.PriceObservation{Region: region, Edition: edition, Period: period, Usage: string(p.Type), DownloadFree: p.Type == "free" || p.Type == "free_with_iap", Currency: p.Currency, PurchaseURL: p.SourceUrl, Channel: "vendor"}
		if p.Amount != nil {
			amount := strconv.FormatFloat(*p.Amount, 'f', -1, 64)
			value.Amount = &amount
		}
		out = append(out, facts.PriceFact{PriceObservation: value, Evidence: facts.NewProof(p.SourceUrl, p.VerifiedAt, facts.StaleAfter, "verified", now)})
	}
	if d.AppStore != nil {
		p := d.AppStore
		amount := strconv.FormatFloat(p.Price, 'f', -1, 64)
		// Lookup does not describe the in-app purchases or subscription needed to use an app.
		value := facts.PriceObservation{Region: p.Region, Edition: "App Store", Period: "unknown", Usage: "unknown", DownloadFree: p.Price == 0, Amount: &amount, Currency: &p.Currency, PurchaseURL: p.Url, Channel: "app_store"}
		out = append(out, facts.PriceFact{PriceObservation: value, Evidence: facts.NewProof(p.Url, p.CheckedAt, 24*time.Hour, "verified", now)})
	}
	return out, nil
}

// refreshFacts runs after the cache lookup, including cache hits. Crossing an expiry boundary
// must withdraw offers immediately rather than waiting for a cached detail document to expire.
func refreshFacts(d *publicapi.PackageDetail, now time.Time) error {
	if d.Facts == nil {
		return nil
	}
	data, err := json.Marshal(d.Facts)
	if err != nil {
		return err
	}
	var value map[string]any
	if err = json.Unmarshal(data, &value); err != nil {
		return err
	}
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if proof, ok := v["evidence"]; ok {
				raw, _ := json.Marshal(proof)
				var e facts.Proof
				if json.Unmarshal(raw, &e) == nil {
					v["evidence"] = e.At(now)
				}
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(value)
	offers := []facts.PriceFact{}
	if prices, ok := value["prices"].([]any); ok {
		for _, v := range prices {
			raw, _ := json.Marshal(v)
			var p facts.PriceFact
			if json.Unmarshal(raw, &p) == nil && p.OfferReady(now) {
				offers = append(offers, p)
			}
		}
	}
	value["offers"] = offers
	if alternatives, ok := value["alternatives"].([]any); ok {
		for _, raw := range alternatives {
			alternative, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			truth := map[string]bool{}
			for _, field := range []string{"attributes", "prices"} {
				shown := []any{}
				if entries, ok := alternative[field].([]any); ok {
					for _, rawEntry := range entries {
						entry, ok := rawEntry.(map[string]any)
						if !ok {
							continue
						}
						proof, ok := entry["evidence"].(facts.Proof)
						if !ok || proof.Status != "verified" {
							continue
						}
						shown = append(shown, entry)
						if name, ok := entry["name"].(string); ok {
							yes, _ := entry["booleanValue"].(bool)
							truth[name] = yes
						}
						if usage, ok := entry["usage"].(string); ok {
							truth[usage] = true
							if usage == "paid" && entry["period"] == "one_time" {
								truth["one_time_purchase"] = true
							}
						}
					}
				}
				alternative[field] = shown
			}
			for _, field := range []string{"reasons", "limits"} {
				shown := []string{}
				if codes, ok := alternative[field].([]any); ok {
					for _, code := range codes {
						if text, ok := code.(string); ok && truth[text] {
							shown = append(shown, text)
						}
					}
				}
				alternative[field] = shown
			}
			if migration, ok := alternative["migration"].(map[string]any); ok {
				parent, ok := migration["evidence"].(facts.Proof)
				valid := ok && parent.Status == "verified"
				if steps, ok := migration["steps"].([]any); ok {
					for _, rawStep := range steps {
						step, ok := rawStep.(map[string]any)
						if !ok {
							valid = false
							break
						}
						proof, ok := step["evidence"].(facts.Proof)
						valid = valid && ok && proof.Status == "verified"
					}
				}
				if !valid {
					delete(alternative, "migration")
				}
			}
		}
	}
	for _, field := range []string{"issues", "integrations"} {
		shown := []any{}
		if items, ok := value[field].([]any); ok {
			for _, raw := range items {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				proof, ok := entry["evidence"].(facts.Proof)
				if ok && proof.Status == "verified" {
					shown = append(shown, entry)
				}
			}
		}
		if _, exists := value[field]; exists {
			value[field] = shown
		}
	}
	if security, ok := value["security"].(map[string]any); ok {
		products := map[string]bool{}
		if identities, ok := security["identities"].([]any); ok {
			for _, raw := range identities {
				identity, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				proof, ok := identity["evidence"].(facts.Proof)
				if ok && proof.Status == "verified" {
					cpe, _ := identity["productCpe"].(string)
					products[cpe] = true
				}
			}
		}
		shown := []any{}
		if vulnerabilities, ok := security["vulnerabilities"].([]any); ok {
			for _, item := range vulnerabilities {
				entry, ok := item.(map[string]any)
				if !ok {
					continue
				}
				proof, ok := entry["evidence"].(facts.Proof)
				cpe, _ := entry["productCpe"].(string)
				if ok && proof.Status == "verified" && products[cpe] {
					shown = append(shown, item)
				}
			}
		}
		security["vulnerabilities"] = shown
	}
	if d.Price != nil {
		d.Price.Stale = facts.Stale(d.Price.VerifiedAt, now)
		d.Price.Offer = false
		for _, offer := range offers {
			if offer.PurchaseURL == d.Price.SourceUrl && offer.Usage == string(d.Price.Type) {
				d.Price.Offer = true
				break
			}
		}
		value["price"] = d.Price
	}
	if d.MacosSupplement != nil {
		d.MacosSupplement.Stale = facts.Stale(d.MacosSupplement.VerifiedAt, now)
		value["macosSupplement"] = d.MacosSupplement
	}
	data, err = json.Marshal(value)
	if err != nil {
		return err
	}
	var refreshed publicapi.AppFacts
	if err := json.Unmarshal(data, &refreshed); err != nil {
		return err
	}
	d.Facts = &refreshed
	return nil
}
