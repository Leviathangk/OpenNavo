package catalog

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
)

func pageDecisionView(d seo.PageDecision) publicapi.PageDecision {
	locales := make([]publicapi.Locale, len(d.Hreflang))
	for i, locale := range d.Hreflang {
		locales[i] = publicapi.Locale(locale)
	}
	return publicapi.PageDecision{Robots: publicapi.PageDecisionRobots(d.Robots), CanonicalPath: d.CanonicalPath,
		Hreflang: locales, InSitemap: d.InSitemap, Announce: d.Announce, Reason: d.Reason}
}

func packagePages(row store.IndexRow, locale string, policy seo.Policy) publicapi.PackagePageDecisions {
	f := row.Facts()
	decide := func(kind seo.PageKind) publicapi.PageDecision {
		return pageDecisionView(seo.DecidePage(f, seo.PageRef{Kind: kind, Locale: locale, Page: 1}, policy))
	}
	alternatives, uninstall, troubleshooting := decide(seo.PageAlternatives), decide(seo.PageUninstall), decide(seo.PageTroubleshooting)
	return publicapi.PackagePageDecisions{Troubleshooting: &troubleshooting, Alternatives: &alternatives, Uninstall: &uninstall, Overview: decide(seo.PageOverview), Versions: decide(seo.PageVersions),
		Dependencies: decide(seo.PageDependencies), Details: decide(seo.PageDetails)}
}

func (s *PublicService) releaseDecision(ctx context.Context, token, locale string, page, size int, filtered bool) (publicapi.PageDecision, error) {
	row, found, err := s.Store.IndexRowByToken(ctx, token)
	if err != nil {
		return publicapi.PageDecision{}, err
	}
	if !found {
		return pageDecisionView(seo.PageDecision{Robots: "noindex, follow", Hreflang: []string{}, Reason: "not_found"}), nil
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return publicapi.PageDecision{}, err
	}
	rows := []store.IndexRow{row}
	if !filtered && size == seo.VersionsPageSize {
		if err := s.Store.PopulatePageNotes(ctx, rows, page); err != nil {
			return publicapi.PageDecision{}, err
		}
	}
	return pageDecisionView(seo.DecidePage(rows[0].Facts(), seo.PageRef{Kind: seo.PageVersions, Locale: locale, Page: page, Filtered: filtered || size != seo.VersionsPageSize}, policy)), nil
}
