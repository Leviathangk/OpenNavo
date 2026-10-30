package catalog

import (
	"context"
	"encoding/json"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/store"
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

// sourceFacts adds what the facts:sources job read from GitHub and the Mac App Store. A public
// GitHub repository with a stored subject is published even when its license is not
// OSI-approved; the openSource object remains limited to the stored license decision.
func (s *PublicService) sourceFacts(ctx context.Context, d *publicapi.PackageDetail, id int64) error {
	rows, err := s.Store.PublicSourceFacts(ctx, id)
	if err != nil {
		return err
	}
	for _, row := range rows {
		switch row.Source {
		case "github":
			var github sources.GitHubFact
			if json.Unmarshal(row.Data, &github) != nil {
				continue
			}
			// Rows written by an older worker may have recorded the repository in
			// subject before the JSON payload was populated.
			if github.Repo == "" && row.Subject != "" {
				github.Repo, github.URL = row.Subject, "https://github.com/"+row.Subject
			}
			if github.Repo == "" || github.URL == "" {
				continue
			}
			if row.Subject != "" {
				repository := &publicapi.RepositoryFact{Url: github.URL, Archived: github.Archived}
				if github.Stars != nil {
					stars := *github.Stars
					repository.Stars = &stars
				}
				if github.PushedAt != nil {
					pushedAt := github.PushedAt.UTC()
					repository.PushedAt = &pushedAt
				}
				if row.SignalsCheckedAt != nil {
					signalsCheckedAt := row.SignalsCheckedAt.UTC()
					repository.SignalsCheckedAt = &signalsCheckedAt
				}
				d.Repository = repository
			}
			if row.Status == "found" && github.License != "" {
				d.OpenSource = &publicapi.OpenSourceFact{Repo: github.Repo, Url: github.URL, License: github.License, Archived: github.Archived, CheckedAt: row.CheckedAt}
			}
		case "app_store":
			var listing sources.Listing
			if json.Unmarshal(row.Data, &listing) != nil || listing.Name == "" || listing.URL == "" || listing.Currency == "" {
				continue
			}
			languages := listing.Languages
			if languages == nil {
				languages = []string{}
			}
			d.AppStore = &publicapi.AppStoreListing{Name: listing.Name, Url: listing.URL, BundleId: listing.BundleID, Price: listing.Price,
				Currency: listing.Currency, Region: listing.Region, Languages: languages, CheckedAt: row.CheckedAt}
		}
	}
	return nil
}

// homebrewStatus is Homebrew's own lifecycle, which disabled mixes with OpenNavo's 90-day rule.
func homebrewStatus(p store.PublicPackage) *publicapi.HomebrewStatus {
	status := &publicapi.HomebrewStatus{State: publicapi.HomebrewActive}
	var date *string
	switch {
	case p.UpstreamDisabled:
		status.State, status.Reason, date = publicapi.HomebrewDisabled, p.DisableReason, p.DisableDate
	case p.Deprecated:
		status.State, status.Reason, date = publicapi.HomebrewDeprecated, p.DeprecationReason, p.DeprecationDate
	}
	if date != nil {
		if value, err := time.Parse("2006-01-02", *date); err == nil {
			status.Date = &openapi_types.Date{Time: value}
		}
	}
	return status
}
