package catalog

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Intent facts can expire while the detail cache is alive; do not retain an earlier admission.
func (s *PublicService) refreshIntentPages(ctx context.Context, token, locale string, d *publicapi.PackageDetail) error {
	if d.Indexing == nil {
		return nil
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return err
	}
	row, found, err := s.Store.TroubleshootingIndexRow(ctx, token, s.now())
	if err != nil {
		return err
	}
	f := seo.Facts{Kind: string(d.Kind), Token: token}
	if found {
		f = row.Facts()
	}
	troubleshooting := pageDecisionView(seo.DecidePage(f, seo.PageRef{Kind: seo.PageTroubleshooting, Locale: locale}, policy))
	d.Indexing.Pages.Troubleshooting = &troubleshooting
	if policy.IntentPagesEnabled {
		row, found, err := s.Store.IndexRowByToken(ctx, token)
		if err != nil {
			return err
		}
		if found {
			rows := []store.IndexRow{row}
			if err := s.Store.PopulateIntentEligibility(ctx, rows, policy, s.WebBaseURL); err != nil {
				return err
			}
			f = rows[0].Facts()
		}
	}
	alternatives := pageDecisionView(seo.DecidePage(f, seo.PageRef{Kind: seo.PageAlternatives, Locale: locale}, policy))
	uninstall := pageDecisionView(seo.DecidePage(f, seo.PageRef{Kind: seo.PageUninstall, Locale: locale}, policy))
	d.Indexing.Pages.Alternatives = &alternatives
	d.Indexing.Pages.Uninstall = &uninstall
	return nil
}
