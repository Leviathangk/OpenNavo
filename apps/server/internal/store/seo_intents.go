package store

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"gorm.io/gorm"
)

// IntentPageKinds are the intent pages an app may open, in display order.
var IntentPageKinds = []seo.PageKind{seo.PageAlternatives, seo.PageUninstall}

// IntentDerivedLocale has no switch of its own: it follows IntentDerivedSource, as its text does.
const (
	IntentDerivedLocale = traditional.Locale
	IntentDerivedSource = traditional.Source
)

// IntentPage is one open switch (package_intent_pages): an editor or agent opened the page.
type IntentPage struct {
	PackageID        int64
	PageKind, Locale string
	Kind, Token      string
	CreatedAt        time.Time
	// CreatedBy is the write owner: admin:<user>, agent:<client>:<token> or system.
	CreatedBy string
}

// IntentPageChange opens (Enabled) or closes one switch.
type IntentPageChange struct {
	PageKind string
	Locale   string
	Enabled  bool
}

// IntentSwitchLocale reports whether a language has a switch of its own: every site language except the
// derived one.
func IntentSwitchLocale(locale string) bool {
	return i18n.Valid(locale) && locale != IntentDerivedLocale
}

// ValidIntentPageKind names the intent pages.
func ValidIntentPageKind(kind string) bool {
	return slices.Contains(IntentPageKinds, seo.PageKind(kind))
}

const intentPageColumns = `i.package_id,i.page_kind,i.locale,i.created_at,i.created_by,p.token,p.kind
 FROM package_intent_pages i JOIN packages p ON p.id=i.package_id`

// IntentPages lists the open switches of the given apps, by app, kind and site language order.
func (s *Store) IntentPages(ctx context.Context, ids []int64) ([]IntentPage, error) {
	rows := []IntentPage{}
	if len(ids) == 0 {
		return rows, nil
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT "+intentPageColumns+" WHERE i.package_id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	sortIntentPages(rows)
	return rows, nil
}

// ListIntentPages pages the open switches of live apps, newest first. kind and locale filter when set.
func (s *Store) ListIntentPages(ctx context.Context, kind, locale string, current, size int) ([]IntentPage, int64, error) {
	where, args := "p.kind IN ('cask','link') AND p.removed_at IS NULL", []any{}
	if kind != "" {
		where += " AND i.page_kind=?"
		args = append(args, kind)
	}
	if locale != "" {
		where += " AND i.locale=?"
		args = append(args, locale)
	}
	var total int64
	if err := s.DB.WithContext(ctx).Raw("SELECT count(*) FROM package_intent_pages i JOIN packages p ON p.id=i.package_id WHERE "+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []IntentPage{}
	offset, found := domain.PageOffset(current, size, total)
	if !found {
		return rows, total, nil
	}
	err := s.DB.WithContext(ctx).Raw("SELECT "+intentPageColumns+" WHERE "+where+" ORDER BY i.created_at DESC,i.package_id,i.page_kind,i.locale LIMIT ? OFFSET ?", append(args, size, offset)...).Scan(&rows).Error
	return rows, total, err
}

func sortIntentPages(rows []IntentPage) {
	slices.SortStableFunc(rows, func(a, b IntentPage) int {
		return cmp.Or(cmp.Compare(a.PackageID, b.PackageID),
			cmp.Compare(slices.Index(IntentPageKinds, seo.PageKind(a.PageKind)), slices.Index(IntentPageKinds, seo.PageKind(b.PageKind))),
			cmp.Compare(slices.Index(seo.Locales, a.Locale), slices.Index(seo.Locales, b.Locale)))
	})
}

// SetIntentPages opens and closes switches of one live app inside the caller's transaction and reports
// whether any switch changed. A change refreshes the app's cached detail and sitemap pages and goes to
// the URL change feed, which refreshes and announces the pages. Uninstall pages exist for casks only, and
// the derived language follows its source, so neither takes a switch.
func (s *Store) SetIntentPages(ctx context.Context, id int64, changes []IntentPageChange, owner string) (bool, error) {
	var p struct{ Kind, Token string }
	if err := s.DB.WithContext(ctx).Raw("SELECT kind,token FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL", id).Scan(&p).Error; err != nil {
		return false, err
	}
	if p.Token == "" {
		return false, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	for _, change := range changes {
		if err := validIntentChange(change, p.Kind); err != nil {
			return false, err
		}
	}
	changed := false
	for _, change := range changes {
		var result *gorm.DB
		if change.Enabled {
			result = s.DB.WithContext(ctx).Exec("INSERT INTO package_intent_pages(package_id,page_kind,locale,created_by) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", id, change.PageKind, change.Locale, owner)
		} else {
			result = s.DB.WithContext(ctx).Exec("DELETE FROM package_intent_pages WHERE package_id=? AND page_kind=? AND locale=?", id, change.PageKind, change.Locale)
		}
		if result.Error != nil {
			return false, MapAdminError(result.Error)
		}
		changed = changed || result.RowsAffected > 0
	}
	if !changed {
		return false, nil
	}
	return true, s.packagePageChanged(ctx, id, p.Kind, p.Token, "intent-pages")
}

func validIntentChange(change IntentPageChange, kind string) error {
	switch {
	case !ValidIntentPageKind(change.PageKind):
		return domain.Rejected("page_kind_invalid:pageKind")
	case change.Locale == IntentDerivedLocale:
		return domain.Rejected("derived_locale_read_only:" + IntentDerivedLocale + ":" + IntentDerivedSource)
	case !IntentSwitchLocale(change.Locale):
		return domain.Rejected("locale_invalid:locale")
	case change.PageKind == string(seo.PageUninstall) && kind != "cask":
		return domain.Rejected("intent_page_unsupported:uninstall")
	}
	return nil
}

// packagePageChanged refreshes the API caches an app's pages read and queues the URL change feed. The
// page-state triggers on the edited table queue the page state itself.
func (s *Store) packagePageChanged(ctx context.Context, id int64, kind, token, scope string) error {
	if err := s.AppendContentChange(ctx, id); err != nil {
		return err
	}
	identity := cache.PatternLiteral(kind) + ":" + cache.PatternLiteral(token) + ":*"
	return QueueCacheInvalidation(ctx, s.DB, fmt.Sprintf("%s:%d", scope, id), cache.PackageDetailPrefix+identity, "c:pkg:sitemap:*")
}

// IntentContents reports, for each row, the intent pages its app has the content for: three alternatives
// with a verified reason, and the uninstall steps of a cask (link apps have no uninstall page). It reads all
// rows' alternatives, their fact records, prices and the casks' artifacts in a fixed number of queries,
// whatever the number of rows or alternatives.
func (s *Store) IntentContents(ctx context.Context, rows []IndexRow) (map[int64]map[seo.PageKind]bool, error) {
	out := map[int64]map[seo.PageKind]bool{}
	if len(rows) == 0 {
		return out, nil
	}
	owners, casks := []int64{}, []int64{}
	for _, row := range rows {
		if _, seen := out[row.ID]; seen {
			continue
		}
		out[row.ID] = map[seo.PageKind]bool{seo.PageAlternatives: false}
		owners = append(owners, row.ID)
		if row.Kind == "cask" {
			out[row.ID][seo.PageUninstall] = false
			casks = append(casks, row.ID)
		}
	}
	counts, err := s.reasonedAlternatives(ctx, owners)
	if err != nil {
		return nil, err
	}
	for id, count := range counts {
		out[id][seo.PageAlternatives] = count >= 3
	}
	if len(casks) > 0 {
		var declared []struct {
			ID        int64
			Artifacts []byte
		}
		if err := s.DB.WithContext(ctx).Raw("SELECT id,artifacts FROM packages WHERE id IN ?", casks).Scan(&declared).Error; err != nil {
			return nil, err
		}
		for _, row := range declared {
			var artifacts homebrew.CaskArtifacts
			if err := json.Unmarshal(row.Artifacts, &artifacts); err != nil {
				return nil, err
			}
			out[row.ID][seo.PageUninstall] = len(facts.Uninstall(artifacts, facts.Proof{})) > 0
		}
	}
	return out, nil
}

// ApplyIntentSwitches sets each row's IntentLocales from its open switches whose page has the content;
// the derived language follows its source's switch.
func ApplyIntentSwitches(rows []IndexRow, switches []IntentPage, contents map[int64]map[seo.PageKind]bool) {
	positions := map[int64]int{}
	for i := range rows {
		positions[rows[i].ID] = i
		rows[i].IntentLocales = map[seo.PageKind][]string{}
	}
	for _, open := range switches {
		i, ok := positions[open.PackageID]
		kind := seo.PageKind(open.PageKind)
		if !ok || !contents[open.PackageID][kind] {
			continue
		}
		rows[i].IntentLocales[kind] = append(rows[i].IntentLocales[kind], open.Locale)
		if open.Locale == IntentDerivedSource {
			rows[i].IntentLocales[kind] = append(rows[i].IntentLocales[kind], IntentDerivedLocale)
		}
	}
}

// PopulateIntentEligibility admits the intent pages whose switch is open (package_intent_pages) and whose
// content passes IntentContents; the derived language follows its source's switch. A disabled rollout
// (pages.intentPagesEnabled) does not read the switches, and the content is read only for apps with an
// open switch. seo.DecidePage still requires the overview to be indexed in the language, so closed
// languages stay out.
func (s *Store) PopulateIntentEligibility(ctx context.Context, rows []IndexRow, policy seo.Policy, _ string) error {
	if !policy.IntentPagesEnabled || len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	positions := map[int64]int{}
	for i := range rows {
		ids[i] = rows[i].ID
		positions[rows[i].ID] = i
	}
	switches, err := s.IntentPages(ctx, ids)
	if err != nil {
		return err
	}
	opened := []IndexRow{}
	seen := map[int64]bool{}
	for _, open := range switches {
		if i, ok := positions[open.PackageID]; ok && !seen[open.PackageID] {
			seen[open.PackageID] = true
			opened = append(opened, rows[i])
		}
	}
	contents, err := s.IntentContents(ctx, opened)
	if err != nil {
		return err
	}
	ApplyIntentSwitches(rows, switches, contents)
	return nil
}

// reasonedAlternatives counts, per owner, the alternatives pages show that have a verified reason: the same
// reason codes comparison facts display. Bare relation rows or expired claims do not establish three
// independently useful recommendations. Three queries cover every owner and alternative.
func (s *Store) reasonedAlternatives(ctx context.Context, owners []int64) (map[int64]int, error) {
	counts := map[int64]int{}
	if len(owners) == 0 {
		return counts, nil
	}
	var relations []struct {
		OwnerID, AlternativeID int64
		Reasons, Version       string
	}
	visible := linkSQL(domain.WithLinks(ctx, true), publicVisible)
	if err := s.DB.WithContext(ctx).Raw(`SELECT a.package_id AS owner_id,a.alternative_id,to_jsonb(a.reasons)::text AS reasons,p.version
 FROM package_alternatives a JOIN packages p ON p.id=a.alternative_id LEFT JOIN package_meta m ON m.package_id=p.id
 WHERE a.package_id IN ? AND `+visible+` AND NOT `+PackageDisabledSQL, owners).Scan(&relations).Error; err != nil {
		return nil, err
	}
	if len(relations) == 0 {
		return counts, nil
	}
	targets := []int64{}
	versions := map[int64]string{}
	for _, relation := range relations {
		if _, ok := versions[relation.AlternativeID]; !ok {
			targets = append(targets, relation.AlternativeID)
		}
		versions[relation.AlternativeID] = relation.Version
	}
	var records []struct {
		PackageID       int64
		Kind            string
		Value, Evidence []byte
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT package_id,kind,value,evidence FROM package_fact_records WHERE package_id IN ? AND kind IN ('attribute','price') ORDER BY package_id,kind,key", targets).Scan(&records).Error; err != nil {
		return nil, err
	}
	var prices []struct {
		PackageID int64
		Price     []byte
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT package_id,price FROM package_facts WHERE package_id IN ? AND price IS NOT NULL", targets).Scan(&prices).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	truth := map[int64]map[string]bool{}
	priced := map[int64]bool{}
	for _, id := range targets {
		truth[id] = map[string]bool{}
	}
	for _, record := range records {
		if record.Kind == "price" {
			priced[record.PackageID] = true
		}
		var proof facts.Proof
		if json.Unmarshal(record.Evidence, &proof) != nil || proof.At(now).Status != "verified" {
			continue
		}
		known := truth[record.PackageID]
		switch record.Kind {
		case "attribute":
			var a facts.Attribute
			if json.Unmarshal(record.Value, &a) == nil && a.Validate() == nil && (a.AppVersion == "" || a.AppVersion == versions[record.PackageID]) && a.BooleanValue != nil {
				known[a.Name] = *a.BooleanValue
			}
		case "price":
			var p facts.PriceObservation
			if json.Unmarshal(record.Value, &p) == nil && p.Validate() == nil {
				known["free"] = known["free"] || p.Usage == "free"
				known["one_time_purchase"] = known["one_time_purchase"] || p.Usage == "paid" && p.Period == "one_time"
				known["subscription"] = known["subscription"] || p.Usage == "subscription"
			}
		}
	}
	// Apps without price records fall back to the reviewed price fact, as the comparison facts do.
	for _, row := range prices {
		if priced[row.PackageID] {
			continue
		}
		var p *facts.Price
		if err := json.Unmarshal(row.Price, &p); err != nil {
			return nil, fmt.Errorf("decode package price: %w", err)
		}
		if p != nil && p.Shown() && !facts.Stale(p.VerifiedAt, now) {
			known := truth[row.PackageID]
			known["free"] = p.Type == "free"
			known["subscription"] = p.Type == "subscription"
			known["one_time_purchase"] = p.Type == "paid" && p.Period != nil && *p.Period == "one_time"
		}
	}
	for _, relation := range relations {
		var reasons []string
		if err := json.Unmarshal([]byte(relation.Reasons), &reasons); err != nil {
			return nil, err
		}
		if slices.ContainsFunc(reasons, func(reason string) bool { return truth[relation.AlternativeID][reason] }) {
			counts[relation.OwnerID]++
		}
	}
	return counts, nil
}

func IntentPagePath(row IndexRow, kind seo.PageKind, locale string) string {
	return fmt.Sprintf("%s/%s", seo.OverviewPath(row.Facts(), locale), kind)
}

// MergePackagePageSettings moves a link app's intent page switches and overview overrides to the cask that
// adopted it, inside the merge transaction. The cask's own rows win; the link's rows are then removed.
func MergePackagePageSettings(ctx context.Context, tx *gorm.DB, from, to int64) error {
	for _, query := range []string{
		`INSERT INTO package_intent_pages(package_id,page_kind,locale,created_at,created_by)
 SELECT ?,i.page_kind,i.locale,i.created_at,i.created_by FROM package_intent_pages i JOIN packages p ON p.id=?
 WHERE i.package_id=? AND (i.page_kind='alternatives' OR p.kind='cask') ON CONFLICT DO NOTHING`,
		`INSERT INTO package_seo_overrides(package_id,locale,title,description,updated_at,updated_by)
 SELECT ?,o.locale,o.title,o.description,o.updated_at,o.updated_by FROM package_seo_overrides o JOIN packages p ON p.id=?
 WHERE o.package_id=? ON CONFLICT DO NOTHING`,
	} {
		if err := tx.WithContext(ctx).Exec(query, to, to, from).Error; err != nil {
			return err
		}
	}
	for _, table := range []string{"package_intent_pages", "package_seo_overrides"} {
		if err := tx.WithContext(ctx).Exec("DELETE FROM "+table+" WHERE package_id=?", from).Error; err != nil {
			return err
		}
	}
	return nil
}

// PruneOrphanPackagePageSettings deletes intent switches and overview overrides whose app row no longer
// exists. The tables have no foreign key to packages in this release (see 00109), so the daily cleanup
// sweeps what a hard delete leaves; removed apps keep their rows in case they return.
func (s *Store) PruneOrphanPackagePageSettings(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		for _, table := range []string{"package_intent_pages", "package_seo_overrides"} {
			r := tx.WithContext(ctx).Exec("DELETE FROM " + table + " t WHERE NOT EXISTS(SELECT 1 FROM packages p WHERE p.id=t.package_id)")
			if r.Error != nil {
				return r.Error
			}
			out[table] = r.RowsAffected
		}
		return nil
	})
	return out, err
}
