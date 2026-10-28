package management

import (
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
)

func translationImpact(rows []store.IndexRow, policy seo.Policy, current, size int) map[string]any {
	enforced := policy
	enforced.TranslationMode = "enforce"
	enforced.TranslationLocales = nil
	counts := []map[string]any{}
	records := []map[string]any{}
	total, start := 0, (current-1)*size
	for _, locale := range seo.Locales {
		nowCount, afterCount, losing := 0, 0, 0
		for _, row := range rows {
			ref := seo.PageRef{Kind: seo.PageOverview, Locale: locale, Page: 1}
			before := seo.DecidePage(row.Facts(), ref, policy)
			after := seo.DecidePage(row.Facts(), ref, enforced)
			if before.Announce {
				nowCount++
			}
			if after.Announce {
				afterCount++
			}
			if before.Announce && !after.Announce {
				losing++
				total++
				if total > start && len(records) < size {
					records = append(records, map[string]any{"id": row.ID, "token": row.Token, "locale": locale, "current": before, "enforced": after})
				}
			}
		}
		counts = append(counts, map[string]any{"locale": locale, "indexableNow": nowCount, "indexableEnforced": afterCount, "losing": losing})
	}
	return map[string]any{"mode": policy.TranslationMode, "counts": counts, "records": records, "current": current, "size": size, "total": total}
}
