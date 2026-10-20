package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
)

type CollectionSetupPackage struct {
	Token, Kind  string
	Homepage     *string
	Prices       []facts.PriceFact
	Requirements []facts.Requirement
}

// CollectionSetupPackages projects only the facts consumed by setup. The two
// queries stay independent of member count; screenshots, releases, alternatives,
// article relations and page eligibility are deliberately absent.
func (s *Store) CollectionSetupPackages(ctx context.Context, slug string, now time.Time) ([]CollectionSetupPackage, error) {
	var rows []struct {
		ID                                              int64
		Kind, Token, Version, Tap, SourcePath           string
		Homepage, DownloadURL, GithubRepo, MinMacos     *string
		SupportsArm64                                   bool
		SupportsX8664                                   bool `gorm:"column:supports_x86_64"`
		HomebrewCheckedAt, AppStoreCheckedAt            time.Time
		Dependencies, Price, Macos, Platforms, AppStore []byte
		RequirementsSource                              string
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id,p.kind,p.token,p.version,p.tap,COALESCE(p.raw->>'ruby_source_path','') AS source_path,p.homepage,p.download_url,l.github_repo,p.min_macos,p.supports_arm64,p.supports_x86_64,p.homebrew_checked_at,p.dependencies,
 f.price,f.macos,r.platforms,r.source_url AS requirements_source,s.data AS app_store,s.checked_at AS app_store_checked_at
 FROM collections c JOIN collection_items ci ON ci.collection_id=c.id JOIN packages p ON p.id=ci.package_id
 LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN link_apps l ON l.package_id=p.id
 LEFT JOIN package_facts f ON f.package_id=p.id
 LEFT JOIN package_version_requirements r ON r.package_id=p.id AND r.app_version=p.version
 LEFT JOIN package_source_facts s ON s.package_id=p.id AND s.source='app_store' AND s.status='found'
 WHERE c.slug=? AND `+linkSQL(ctx, publicVisible)+` AND NOT (p.disabled OR p.stale_disabled) ORDER BY ci.sort,p.id`, slug).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]CollectionSetupPackage, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	var records []struct {
		PackageID       int64
		Kind            string
		Value, Evidence []byte
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT package_id,kind,value,evidence FROM package_fact_records WHERE package_id IN ? AND kind IN ('price','compatibility') ORDER BY package_id,kind,key`, ids).Scan(&records).Error; err != nil {
		return nil, err
	}
	byPackage := map[int64][]facts.Record{}
	for _, r := range records {
		record := facts.Record{Kind: r.Kind, Value: r.Value}
		if err := json.Unmarshal(r.Evidence, &record.Evidence); err != nil {
			return nil, err
		}
		byPackage[r.PackageID] = append(byPackage[r.PackageID], record)
	}
	for _, row := range rows {
		var metadata homebrew.CaskPlatformMetadata
		if err := json.Unmarshal(row.Dependencies, &metadata); err != nil {
			return nil, err
		}
		var legacy PackageFacts
		for _, item := range []struct {
			raw    []byte
			target any
		}{{row.Price, &legacy.Price}, {row.Macos, &legacy.Macos}} {
			if len(item.raw) > 0 {
				if err := json.Unmarshal(item.raw, item.target); err != nil {
					return nil, err
				}
			}
		}
		legacy.Homebrew = facts.HomebrewFrom(row.MinMacos, row.SupportsArm64, row.SupportsX8664, metadata)
		path := row.SourcePath
		if path == "" {
			path = "Casks/" + row.Token[:1] + "/" + row.Token + ".rb"
		}
		source := "https://github.com/" + strings.Replace(row.Tap, "/", "/homebrew-", 1) + "/blob/HEAD/" + path
		proof := facts.NewProof(source, row.HomebrewCheckedAt, facts.StaleAfter, "verified", now)
		if row.Kind == "link" {
			source = ""
			if row.DownloadURL != nil {
				source = *row.DownloadURL
			}
			if row.Homepage != nil {
				source = *row.Homepage
			}
			if row.GithubRepo != nil {
				source = "https://github.com/" + *row.GithubRepo
			}
			proof = facts.Proof{SourceURL: source, Status: "unknown"}
		}
		item := CollectionSetupPackage{Token: row.Token, Kind: row.Kind, Homepage: row.Homepage, Prices: []facts.PriceFact{}, Requirements: facts.Requirements(metadata.Platforms, proof)}
		// Match normalizedFacts: visible legacy supplements precede additional
		// architecture-independent current-version declarations.
		if m := legacy.Macos; m != nil && m.Shown() {
			status := "verified"
			if len(facts.Conflicts(m, legacy.Homebrew)) > 0 || m.AppVersion == nil || *m.AppVersion != row.Version {
				status = "needs_review"
			}
			evidence := facts.NewProof(m.SourceURL, m.VerifiedAt, facts.StaleAfter, status, now)
			for _, r := range m.Ranges {
				native := "unknown"
				if r.Arch == "arm64" {
					native = "no"
					if r.Support == "native" {
						native = "yes"
					}
				}
				versions := []string{}
				if m.AppVersion != nil {
					versions = append(versions, *m.AppVersion)
				}
				item.Requirements = append(item.Requirements, facts.Requirement{Versions: versions, Architectures: []string{r.Arch}, PlatformMacOS: []string{}, Minimum: r.Minimum, Maximum: r.Maximum, AppleSilicon: native, Evidence: evidence})
			}
		}
		if len(row.Platforms) > 0 {
			var platforms []homebrew.CaskPlatform
			if err := json.Unmarshal(row.Platforms, &platforms); err != nil {
				return nil, err
			}
			for _, r := range facts.Requirements(platforms, facts.NewProof(row.RequirementsSource, row.HomebrewCheckedAt, facts.StaleAfter, "verified", now)) {
				if len(r.Architectures) == 0 {
					item.Requirements = append(item.Requirements, r)
				}
			}
		}
		for _, r := range byPackage[row.ID] {
			switch r.Kind {
			case "compatibility":
				var v facts.Compatibility
				if json.Unmarshal(r.Value, &v) == nil && v.Validate() == nil && v.AppVersion == row.Version {
					item.Requirements = append(item.Requirements, v.Requirement(r.Evidence.At(now)))
				}
			case "price":
				var v facts.PriceObservation
				if json.Unmarshal(r.Value, &v) == nil && v.Validate() == nil {
					item.Prices = append(item.Prices, facts.PriceFact{PriceObservation: v, Evidence: r.Evidence.At(now)})
				}
			}
		}
		// Same migration fallback as priceFacts: a valid dimensioned observation
		// suppresses the legacy price even when its evidence has expired.
		if p := legacy.Price; len(item.Prices) == 0 && p != nil && p.Shown() {
			period, region, edition := "unknown", "ZZ", "unspecified"
			if p.Period != nil {
				period = map[string]string{"one_time": "one_time", "month": "monthly", "year": "yearly"}[*p.Period]
			}
			if p.Type == "free" {
				period = "free"
			}
			if p.Region != nil {
				region = *p.Region
			}
			if p.Plan != nil {
				edition = *p.Plan
			}
			value := facts.PriceObservation{Region: region, Edition: edition, Period: period, Usage: p.Type, DownloadFree: p.Type == "free" || p.Type == "free_with_iap", Currency: p.Currency, PurchaseURL: p.SourceURL, Channel: "vendor"}
			if p.Amount != nil {
				amount := strconv.FormatFloat(*p.Amount, 'f', -1, 64)
				value.Amount = &amount
			}
			item.Prices = append(item.Prices, facts.PriceFact{PriceObservation: value, Evidence: facts.NewProof(p.SourceURL, p.VerifiedAt, facts.StaleAfter, "verified", now)})
		}
		var listing sources.Listing
		if json.Unmarshal(row.AppStore, &listing) == nil && listing.Name != "" && listing.URL != "" && listing.Currency != "" {
			amount := strconv.FormatFloat(listing.Price, 'f', -1, 64)
			value := facts.PriceObservation{Region: listing.Region, Edition: "App Store", Period: "unknown", Usage: "unknown", DownloadFree: listing.Price == 0, Amount: &amount, Currency: &listing.Currency, PurchaseURL: listing.URL, Channel: "app_store"}
			item.Prices = append(item.Prices, facts.PriceFact{PriceObservation: value, Evidence: facts.NewProof(listing.URL, row.AppStoreCheckedAt, 24*time.Hour, "verified", now)})
		}
		out = append(out, item)
	}
	return out, nil
}
