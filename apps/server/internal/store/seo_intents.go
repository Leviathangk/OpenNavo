package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/seo"
)

// PopulateIntentEligibility joins unique target reservations to confirmed demand and public facts.
// Disabled rollout does not query the optional keyword ledger.
func (s *Store) PopulateIntentEligibility(ctx context.Context, rows []IndexRow, policy seo.Policy, siteURL string) error {
	if !policy.IntentPagesEnabled || len(rows) == 0 {
		return nil
	}
	base, err := url.Parse(siteURL)
	if err != nil {
		return err
	}
	ids := make([]int64, len(rows))
	positions := map[int64]int{}
	for i := range rows {
		ids[i] = rows[i].ID
		positions[rows[i].ID] = i
		rows[i].IntentLocales = map[seo.PageKind][]string{}
	}
	var targets []struct {
		PackageID                 int64
		Intent, Locale, TargetURL string
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT p.id AS package_id,t.intent,t.locale,t.target_url FROM seo_targets t JOIN packages p ON t.entity_key='pkg:'||p.id::text
 WHERE p.id IN ? AND t.intent IN('alternative','uninstall') AND EXISTS(
 SELECT 1 FROM seo_keyword_links l JOIN seo_keywords k ON k.id=l.keyword_id WHERE l.package_id=p.id AND l.intent=t.intent AND l.state='confirmed' AND k.language=t.locale AND k.volume_state='known' AND k.search_volume>0)`, ids).Scan(&targets).Error; err != nil {
		return err
	}
	content := map[int64]map[seo.PageKind]bool{}
	for _, target := range targets {
		row := &rows[positions[target.PackageID]]
		kind := seo.PageAlternatives
		if target.Intent == "uninstall" {
			kind = seo.PageUninstall
		}
		parsed, err := url.Parse(target.TargetURL)
		if err != nil || parsed.Scheme != base.Scheme || parsed.Host != base.Host || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != seo.OverviewPath(row.Facts(), target.Locale)+"/"+string(kind) {
			continue
		}
		if content[row.ID] == nil {
			content[row.ID] = map[seo.PageKind]bool{}
			count, err := s.reasonedAlternatives(ctx, row.ID)
			if err != nil {
				return err
			}
			content[row.ID][seo.PageAlternatives] = count >= 3
			if row.Kind == "cask" {
				var raw struct{ Artifacts []byte }
				if err := s.DB.WithContext(ctx).Raw("SELECT artifacts FROM packages WHERE id=?", row.ID).Scan(&raw).Error; err != nil {
					return err
				}
				var artifacts homebrew.CaskArtifacts
				if err := json.Unmarshal(raw.Artifacts, &artifacts); err != nil {
					return err
				}
				content[row.ID][seo.PageUninstall] = len(facts.Uninstall(artifacts, facts.Proof{})) > 0
			}
		}
		if content[row.ID][kind] {
			row.IntentLocales[kind] = append(row.IntentLocales[kind], target.Locale)
		}
	}
	return nil
}

// Count the same verified reason codes displayed by comparison facts. Bare relation rows or
// expired claims do not establish three independently useful recommendations.
func (s *Store) reasonedAlternatives(ctx context.Context, id int64) (int, error) {
	rows, err := s.Alternatives(domain.WithLinks(ctx, true), id, true)
	if err != nil {
		return 0, err
	}
	count := 0
	now := time.Now()
	for _, row := range rows {
		records, err := s.FactRecords(ctx, row.PackageID)
		if err != nil {
			return 0, err
		}
		var version string
		if err := s.DB.WithContext(ctx).Raw("SELECT version FROM packages WHERE id=?", row.PackageID).Scan(&version).Error; err != nil {
			return 0, err
		}
		truth := map[string]bool{}
		hasPrices := false
		for _, record := range records {
			if record.Kind == "price" {
				hasPrices = true
			}
			if record.Evidence.At(now).Status != "verified" {
				continue
			}
			switch record.Kind {
			case "attribute":
				var a facts.Attribute
				if json.Unmarshal(record.Value, &a) == nil && a.Validate() == nil && (a.AppVersion == "" || a.AppVersion == version) && a.BooleanValue != nil {
					truth[a.Name] = *a.BooleanValue
				}
			case "price":
				var p facts.PriceObservation
				if json.Unmarshal(record.Value, &p) == nil && p.Validate() == nil {
					truth["free"] = truth["free"] || p.Usage == "free"
					truth["one_time_purchase"] = truth["one_time_purchase"] || p.Usage == "paid" && p.Period == "one_time"
					truth["subscription"] = truth["subscription"] || p.Usage == "subscription"
				}
			}
		}
		if !hasPrices {
			current, _, err := s.PackageFacts(ctx, row.PackageID)
			if err != nil {
				return 0, err
			}
			if p := current.Price; p != nil && p.Shown() && !facts.Stale(p.VerifiedAt, now) {
				truth["free"] = p.Type == "free"
				truth["subscription"] = p.Type == "subscription"
				truth["one_time_purchase"] = p.Type == "paid" && p.Period != nil && *p.Period == "one_time"
			}
		}
		if slices.ContainsFunc(row.Reasons, func(reason string) bool { return truth[reason] }) {
			count++
		}
	}
	return count, nil
}

func IntentPagePath(row IndexRow, kind seo.PageKind, locale string) string {
	return fmt.Sprintf("%s/%s", seo.OverviewPath(row.Facts(), locale), kind)
}
