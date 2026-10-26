//go:build integration

package store_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// seedReasonedAlternatives gives owner n alternatives, each with a verified open-source attribute, so the
// alternatives page passes its content gate when n is at least three.
func seedReasonedAlternatives(t *testing.T, st *store.Store, owner, first int64, n int) []int64 {
	t.Helper()
	ids := []int64{}
	for i := range n {
		id := first + int64(i)
		token := fmt.Sprint("reasoned-alt-", id)
		require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(?,'cask',?,?,'homebrew/cask','Alternative','1','1','{}','fixture')`, id, token, token).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(?,?,?,ARRAY['open_source'],'admin')`, owner, id, i).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,'attribute','open-source','{"name":"open_source","booleanValue":true}',
 jsonb_build_object('sourceUrl','https://vendor.fixture.example/docs','checkedAt',now()-interval '1 day','validUntil',now()+interval '30 days','status','verified'))`, id).Error)
		ids = append(ids, id)
	}
	return ids
}

func TestIntentPageSwitchesContentGatesAndDerivedLocale(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,artifacts) VALUES
 (973,'cask','fixture-intent','fixture-intent','homebrew/cask','Fixture Intent','1','1','{}','fixture','{"entries":[{"phase":"uninstall","declaration":{"uninstall":[{"pkgutil":"com.fixture.example"}]}}]}'),
 (980,'link','fixture-link','fixture-link','','Fixture Link','1','1','{}','fixture','{}')`).Error)
	alternatives := seedReasonedAlternatives(t, st, 973, 974, 3)
	seedReasonedAlternatives(t, st, 980, 990, 3)
	policy := seo.Policy{IntentPagesEnabled: true}
	eligible := func(id int64, p seo.Policy) map[seo.PageKind][]string {
		t.Helper()
		row, found, err := st.IndexRowByID(ctx, id)
		require.NoError(t, err)
		require.True(t, found)
		rows := []store.IndexRow{row}
		require.NoError(t, st.PopulateIntentEligibility(ctx, rows, p, ""))
		return rows[0].IntentLocales
	}
	set := func(id int64, owner string, changes ...store.IntentPageChange) (bool, error) {
		t.Helper()
		var changed bool
		err := st.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			changed, err = (&store.Store{DB: tx}).SetIntentPages(ctx, id, changes, owner)
			return err
		})
		return changed, err
	}
	revision := func(key string) int64 {
		t.Helper()
		var n int64
		require.NoError(t, st.DB.Raw("SELECT coalesce((SELECT revision FROM entity_revisions WHERE entity_key=?),0)", key).Scan(&n).Error)
		return n
	}

	contentChanges := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, st.DB.Table("catalog_changes").Where("package_id=973 AND reason='content'").Count(&n).Error)
		return n
	}
	before := contentChanges()

	// Content alone admits nothing: an editor or agent opens each page.
	require.Empty(t, eligible(973, policy)[seo.PageAlternatives])
	changed, err := set(973, "agent:3:7",
		store.IntentPageChange{PageKind: "alternatives", Locale: "en-US", Enabled: true},
		store.IntentPageChange{PageKind: "alternatives", Locale: "zh-CN", Enabled: true},
		store.IntentPageChange{PageKind: "uninstall", Locale: "en-US", Enabled: true})
	require.NoError(t, err)
	require.True(t, changed)
	got := eligible(973, policy)
	require.Equal(t, []string{"en-US", "zh-CN", "zh-Hant"}, got[seo.PageAlternatives], "zh-Hant follows zh-CN")
	require.Equal(t, []string{"en-US"}, got[seo.PageUninstall])
	require.Empty(t, eligible(973, seo.Policy{})[seo.PageAlternatives], "the global switch closes every intent page")
	require.EqualValues(t, 3, revision("intent:973"))
	require.Equal(t, before+1, contentChanges(), "a change goes to the URL change feed once")
	var patterns string
	require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE unique_key='cache:invalidate:intent-pages:973'").Scan(&patterns).Error)
	require.Contains(t, patterns, "cask:fixture-intent:*")
	require.Contains(t, patterns, "c:pkg:sitemap:*")

	// The same request again changes nothing, so the revision stays.
	changed, err = set(973, "admin:1", store.IntentPageChange{PageKind: "alternatives", Locale: "en-US", Enabled: true})
	require.NoError(t, err)
	require.False(t, changed)
	require.EqualValues(t, 3, revision("intent:973"))
	require.Equal(t, before+1, contentChanges(), "an unchanged write announces nothing")
	pages, err := st.IntentPages(ctx, []int64{973})
	require.NoError(t, err)
	require.Len(t, pages, 3)
	require.Equal(t, "agent:3:7", pages[0].CreatedBy, "reopening keeps the first opener")
	require.Equal(t, []string{"alternatives:en-US", "alternatives:zh-CN", "uninstall:en-US"}, []string{pages[0].PageKind + ":" + pages[0].Locale, pages[1].PageKind + ":" + pages[1].Locale, pages[2].PageKind + ":" + pages[2].Locale})

	// Closing zh-CN closes the zh-Hant copy too.
	changed, err = set(973, "admin:1", store.IntentPageChange{PageKind: "alternatives", Locale: "zh-CN", Enabled: false})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"en-US"}, eligible(973, policy)[seo.PageAlternatives])

	// Without three reasoned alternatives the open switch admits nothing; uninstall keeps its own gate.
	require.NoError(t, st.DB.Exec(`UPDATE package_fact_records SET evidence=jsonb_set(evidence,'{status}','"needs_review"') WHERE package_id=?`, alternatives[0]).Error)
	got = eligible(973, policy)
	require.Empty(t, got[seo.PageAlternatives])
	require.Equal(t, []string{"en-US"}, got[seo.PageUninstall])
	content, err := st.IntentContents(ctx, []store.IndexRow{{ID: 973, Kind: "cask"}, {ID: 980, Kind: "link"}})
	require.NoError(t, err)
	require.Equal(t, map[int64]map[seo.PageKind]bool{973: {seo.PageAlternatives: false, seo.PageUninstall: true}, 980: {seo.PageAlternatives: true}}, content)

	// Writes the pages cannot have are rejected with their reason.
	for _, tc := range []struct {
		id     int64
		change store.IntentPageChange
		reason string
	}{
		{973, store.IntentPageChange{PageKind: "alternatives", Locale: "zh-Hant", Enabled: true}, "derived_locale_read_only:zh-Hant:zh-CN"},
		{973, store.IntentPageChange{PageKind: "details", Locale: "en-US", Enabled: true}, "page_kind_invalid:pageKind"},
		{973, store.IntentPageChange{PageKind: "alternatives", Locale: "xx-XX", Enabled: true}, "locale_invalid:locale"},
		{980, store.IntentPageChange{PageKind: "uninstall", Locale: "en-US", Enabled: true}, "intent_page_unsupported:uninstall"},
	} {
		_, err := set(tc.id, "admin:1", tc.change)
		var app *domain.AppError
		require.ErrorAs(t, err, &app, tc.reason)
		require.Equal(t, "rejected: "+tc.reason, app.Msg)
	}
	_, err = set(99999, "admin:1", store.IntentPageChange{PageKind: "alternatives", Locale: "en-US", Enabled: true})
	var app *domain.AppError
	require.ErrorAs(t, err, &app)
	require.Equal(t, domain.CodeNotFound, app.Code)
	_, err = set(973, "root", store.IntentPageChange{PageKind: "alternatives", Locale: "fr-FR", Enabled: true})
	require.Error(t, err, "owners have a fixed form")

	// A link app opens its alternatives page like a cask.
	changed, err = set(980, "agent:3:7",
		store.IntentPageChange{PageKind: "alternatives", Locale: "en-US", Enabled: true},
		store.IntentPageChange{PageKind: "alternatives", Locale: "fr-FR", Enabled: true})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"en-US", "fr-FR"}, eligible(980, policy)[seo.PageAlternatives])

	// The site list filters by kind and language and pages newest first.
	list, total, err := st.ListIntentPages(ctx, "", "", 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 4, total)
	require.Len(t, list, 4)
	list, total, err = st.ListIntentPages(ctx, "alternatives", "en-US", 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, list, 1)
	require.Equal(t, "fixture-link", list[0].Token)
	list, _, err = st.ListIntentPages(ctx, "alternatives", "en-US", 3, 1)
	require.NoError(t, err)
	require.Empty(t, list)
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE id=980").Error)
	_, total, err = st.ListIntentPages(ctx, "", "", 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "removed apps have no pages")
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=NULL WHERE id=980").Error)

	// Adoption by a cask keeps the cask's switches and moves the link's other ones.
	require.NoError(t, st.DB.Exec("INSERT INTO package_seo_overrides(package_id,locale,title,updated_by) VALUES(980,'en-US','Link title','admin:1'),(973,'en-US','Cask title','admin:2')").Error)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return store.MergePackagePageSettings(ctx, tx, 980, 973) }))
	pages, err = st.IntentPages(ctx, []int64{973, 980})
	require.NoError(t, err)
	keys := []string{}
	for _, page := range pages {
		keys = append(keys, fmt.Sprint(page.PackageID, ":", page.PageKind, ":", page.Locale, ":", page.CreatedBy))
	}
	require.Equal(t, []string{"973:alternatives:en-US:agent:3:7", "973:alternatives:fr-FR:agent:3:7", "973:uninstall:en-US:agent:3:7"}, keys)
	override, err := st.PackageSEOOverride(ctx, 973, "en-US")
	require.NoError(t, err)
	require.Equal(t, "Cask title", *override.Title)
	override, err = st.PackageSEOOverride(ctx, 980, "en-US")
	require.NoError(t, err)
	require.Nil(t, override)
}

// statementCounter counts the SQL statements a session runs.
type statementCounter struct{ n atomic.Int64 }

func (c *statementCounter) LogMode(logger.LogLevel) logger.Interface { return c }
func (c *statementCounter) Info(context.Context, string, ...any)     {}
func (c *statementCounter) Warn(context.Context, string, ...any)     {}
func (c *statementCounter) Error(context.Context, string, ...any)    {}
func (c *statementCounter) Trace(context.Context, time.Time, func() (string, int64), error) {
	c.n.Add(1)
}

// Intent eligibility reads alternatives, facts and prices in a fixed number of queries: sitemap pages,
// public details and page decisions call it for every app with an open switch.
func TestIntentEligibilityQueriesDoNotGrowWithAlternatives(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	for _, owner := range []int64{1001, 1002, 1003} {
		token := fmt.Sprint("query-owner-", owner)
		require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,artifacts) VALUES(?,'cask',?,?,'homebrew/cask','Owner','1','1','{}','fixture','{"entries":[{"phase":"uninstall","declaration":{"uninstall":[{"pkgutil":"com.fixture.owner"}]}}]}')`, owner, token, token).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(?,'alternatives','en-US'),(?,'uninstall','en-US')`, owner, owner).Error)
	}
	few := seedReasonedAlternatives(t, st, 1001, 1100, 3)
	many := seedReasonedAlternatives(t, st, 1002, 1200, 8) // the most an app may list
	seedReasonedAlternatives(t, st, 1003, 1300, 8)
	// Some alternatives prove their reason with a reviewed price instead of a record; that path is batched too.
	for _, id := range append(few[:1], many[:4]...) {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_facts(package_id,price) VALUES(?,jsonb_build_object('type','free','status','verified','verifiedAt',now(),'sourceUrl','https://vendor.example/pricing'))`, id).Error)
	}
	policy := seo.Policy{IntentPagesEnabled: true}
	count := func(ids ...int64) (int64, []store.IndexRow) {
		t.Helper()
		rows, err := st.IndexRowsByID(ctx, ids)
		require.NoError(t, err)
		counter := &statementCounter{}
		counted := &store.Store{DB: st.DB.Session(&gorm.Session{Logger: counter})}
		require.NoError(t, counted.PopulateIntentEligibility(ctx, rows, policy, ""))
		return counter.n.Load(), rows
	}
	small, rows := count(1001)
	require.Equal(t, []string{"en-US"}, rows[0].IntentLocales[seo.PageAlternatives])
	large, rows := count(1002)
	require.Equal(t, []string{"en-US"}, rows[0].IntentLocales[seo.PageAlternatives])
	require.Equal(t, []string{"en-US"}, rows[0].IntentLocales[seo.PageUninstall])
	all, _ := count(1001, 1002, 1003)
	require.Equal(t, small, large, "eight alternatives cost the same queries as three")
	require.Equal(t, small, all, "three apps cost the same queries as one")
	require.LessOrEqual(t, small, int64(6))
	unopened, _ := count(few...)
	require.EqualValues(t, 1, unopened, "apps without an open switch only read the switches")
}

// Rows of apps deleted from packages are swept by the daily cleanup; removed apps keep theirs.
func TestPruneOrphanPackagePageSettings(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,removed_at) VALUES
 (1401,'cask','orphan-live','orphan-live','homebrew/cask','Live','1','1','{}','fixture',NULL),
 (1402,'cask','orphan-removed','orphan-removed','homebrew/cask','Removed','1','1','{}','fixture',now())`).Error)
	for _, id := range []int64{1401, 1402, 1403} {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(?,'alternatives','en-US'),(?,'alternatives','ja-JP')`, id, id).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(?,'en-US','Title')`, id).Error)
	}
	pruned, err := st.PruneOrphanPackagePageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"package_intent_pages": 2, "package_seo_overrides": 1}, pruned)
	var left []int64
	require.NoError(t, st.DB.Raw("SELECT DISTINCT package_id FROM package_intent_pages ORDER BY package_id").Scan(&left).Error)
	require.Equal(t, []int64{1401, 1402}, left)
	require.NoError(t, st.DB.Raw("SELECT package_id FROM package_seo_overrides ORDER BY package_id").Scan(&left).Error)
	require.Equal(t, []int64{1401, 1402}, left)
	pruned, err = st.PruneOrphanPackagePageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"package_intent_pages": 0, "package_seo_overrides": 0}, pruned)
}
