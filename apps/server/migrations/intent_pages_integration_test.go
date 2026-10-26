//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// 00109 opens intent page switches from the demand that admitted the pages before and leaves the targets in
// place, so rolling it back and forward again yields the same switches.
func TestIntentPageSwitchesBackfillFromConfirmedDemand(t *testing.T) {
	// The migrate command has WEB_BASE_URL from its configuration; the backfill reads the site origin from it.
	t.Setenv("WEB_BASE_URL", "https://site.example/")
	st := testutil.NewSEOStoreAtVersion(t, 108)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	exec := func(query string, args ...any) {
		t.Helper()
		require.NoError(t, st.DB.Exec(query, args...).Error)
	}
	exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (901,'cask','demand-cask','demand-cask','homebrew/cask','Demand Cask','1','1','{}','fixture'),
 (902,'link','demand-link','demand-link','','Demand Link','1','1','{}','fixture'),
 (903,'formula','demand-formula','demand-formula','homebrew/core','Demand Formula','1','1','{}','fixture'),
 (904,'cask','renamed-cask','renamed-cask','homebrew/cask','Renamed Cask','1','1','{}','fixture'),
 (905,'cask','zh-cask','zh-cask','homebrew/cask','Chinese Cask','1','1','{}','fixture'),
 (906,'cask','host-cask','host-cask','homebrew/cask','Host Cask','1','1','{}','fixture'),
 (907,'cask','query-cask','query-cask','homebrew/cask','Query Cask','1','1','{}','fixture')`)
	// Keyword 1 has paid volume; 2 a recent own ranking; 3 an old one; 4 an anomaly.
	exec(`INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,search_volume,volume_state,our_position,anomaly,observed_at) VALUES
 (1,'demand','demand one','openseo:dataforseo','google','US','en-US','all','2026-10',20,'known',NULL,NULL,now()-interval '120 days'),
 (2,'demand','demand two','gsc','google','ALL','en-US','all','2026-10',NULL,'unknown',3,NULL,now()-interval '1 day'),
 (3,'demand','demand three','gsc','google','ALL','ja-JP','all','2026-10',NULL,'unknown',3,NULL,now()-interval '61 days'),
 (4,'demand','demand four','bing','bing','ALL','de-DE','all','2026-10',NULL,'unknown',3,'spike',now()-interval '1 day'),
 (5,'demand','demand five','gsc','google','ALL','zh-Hant','all','2026-10',NULL,'unknown',3,NULL,now()-interval '1 day'),
 (6,'demand','demand six','gsc','google','ALL','es-ES','all','2026-10',NULL,'unknown',3,NULL,now()-interval '1 day'),
 (7,'demand','demand seven','openseo:dataforseo','google','CN','zh-CN','all','2026-10',30,'known',NULL,NULL,now()-interval '3 days')`)
	exec(`INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES
 (1,901,'alternative',1,'confirmed'),(2,901,'uninstall',1,'confirmed'),(3,901,'alternative',1,'confirmed'),(4,901,'alternative',1,'confirmed'),
 (5,901,'alternative',1,'confirmed'),(6,901,'alternative',1,'candidate'),
 (1,902,'alternative',1,'confirmed'),(2,902,'uninstall',1,'confirmed'),(1,903,'alternative',1,'confirmed'),
 (7,901,'alternative',1,'confirmed'),(1,904,'alternative',1,'confirmed'),(7,905,'alternative',1,'confirmed'),(1,906,'alternative',1,'confirmed'),(1,907,'alternative',1,'confirmed')`)
	exec(`INSERT INTO seo_targets(entity_key,intent,locale,target_url,created_at) VALUES
 ('pkg:901','alternative','en-US','https://site.example/apps/demand-cask/alternatives','2026-10-01T00:00:00Z'),
 ('pkg:901','uninstall','en-US','https://site.example/apps/demand-cask/uninstall','2026-10-02T00:00:00Z'),
 ('pkg:901','alternative','zh-CN','https://site.example/apps/demand-cask/alternatives','2026-10-01T00:00:00Z'),
 ('pkg:901','alternative','ja-JP','https://site.example/ja/apps/demand-cask/alternatives','2026-10-01T00:00:00Z'),
 ('pkg:901','alternative','de-DE','https://site.example/de/apps/demand-cask/alternatives','2026-10-01T00:00:00Z'),
 ('pkg:901','alternative','zh-Hant','https://site.example/zh-hant/apps/demand-cask/alternatives','2026-10-01T00:00:00Z'),
 ('pkg:901','alternative','es-ES','https://site.example/es/apps/demand-cask/alternatives','2026-10-01T00:00:00Z'),
 ('pkg:901','price','en-US','https://site.example/apps/demand-cask','2026-10-01T00:00:00Z'),
 ('pkg:902','alternative','en-US','https://site.example/apps/demand-link/alternatives','2026-10-03T00:00:00Z'),
 ('pkg:902','uninstall','en-US','https://site.example/apps/demand-link/uninstall','2026-10-03T00:00:00Z'),
 ('pkg:903','alternative','en-US','https://site.example/apps/demand-formula/alternatives','2026-10-03T00:00:00Z'),
 ('pkg:904','alternative','en-US','https://site.example/apps/renamed-before/alternatives','2026-10-04T00:00:00Z'),
 ('pkg:905','alternative','zh-CN','https://site.example/zh/apps/zh-cask/alternatives','2026-10-04T00:00:00Z'),
 ('pkg:906','alternative','en-US','https://elsewhere.example/apps/host-cask/alternatives','2026-10-04T00:00:00Z'),
 ('pkg:907','alternative','en-US','https://site.example/apps/query-cask/alternatives?ref=1','2026-10-04T00:00:00Z')`)

	type row struct {
		PackageID        int64
		PageKind, Locale string
		CreatedBy        string
		CreatedAt        string
	}
	switches := func() []row {
		t.Helper()
		var rows []row
		require.NoError(t, st.DB.Raw(`SELECT package_id,page_kind,locale,created_by,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD') AS created_at FROM package_intent_pages ORDER BY package_id,page_kind,locale`).Scan(&rows).Error)
		return rows
	}
	want := []row{
		{901, "alternatives", "en-US", "system", "2026-10-01"},
		{901, "uninstall", "en-US", "system", "2026-10-02"},
		{902, "alternatives", "en-US", "system", "2026-10-03"},
		{905, "alternatives", "zh-CN", "system", "2026-10-04"},
	}
	require.NoError(t, goose.UpToContext(ctx, db, ".", 109))
	// Only targets at the page's own address open: not a renamed app's old token (904), a path without the
	// language prefix (901 zh-CN), another host (906) or a query (907). zh-Hant follows zh-CN, links have no
	// uninstall page and formulae no pages.
	require.Equal(t, want, switches())

	// The tables hold only what the write paths can produce.
	for _, query := range []string{
		"INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(901,'alternatives','zh-Hant')",
		"INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(901,'troubleshooting','en-US')",
		"INSERT INTO package_intent_pages(package_id,page_kind,locale,created_by) VALUES(901,'alternatives','fr-FR','agent:1')",
		"INSERT INTO package_seo_overrides(package_id,locale) VALUES(901,'en-US')",
		"INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(901,'zh-Hant','Title')",
		"INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(901,'en-US',' padded')",
		"INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(901,'en-US','<b>Bold</b>')",
		"INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(901,'en-US',E'Two\\nlines')",
		"INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(901,'en-US',repeat('a',71))",
		"INSERT INTO package_seo_overrides(package_id,locale,description) VALUES(901,'en-US',repeat('a',201))",
		"INSERT INTO package_seo_overrides(package_id,locale,title,updated_by) VALUES(901,'en-US','Title','root')",
	} {
		require.Error(t, st.DB.Exec(query).Error, query)
	}
	exec("INSERT INTO package_seo_overrides(package_id,locale,title,description,updated_by) VALUES(901,'en-US',repeat('a',70),repeat('b',200),'agent:3:7')")

	// Writes queue the app's page state like the other app tables.
	exec("DELETE FROM package_page_dirty")
	exec("UPDATE package_seo_overrides SET title='Changed' WHERE package_id=901")
	exec("INSERT INTO package_intent_pages(package_id,page_kind,locale,created_by) VALUES(902,'alternatives','fr-FR','admin:1')")
	var dirty []int64
	require.NoError(t, st.DB.Raw("SELECT package_id FROM package_page_dirty ORDER BY package_id").Scan(&dirty).Error)
	require.Equal(t, []int64{901, 902}, dirty)

	// Down removes both tables and the revisions they produced; the demand they came from is still there.
	exec("INSERT INTO seo_entity_revisions(entity_key,revision) VALUES('intent:901',4),('pkgseo:901',2),('background:keep',1) ON CONFLICT DO NOTHING")
	require.NoError(t, goose.DownToContext(ctx, db, ".", 108))
	var tables int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM pg_tables WHERE tablename IN ('package_intent_pages','package_seo_overrides')").Scan(&tables).Error)
	require.Zero(t, tables)
	var keys []string
	require.NoError(t, st.DB.Raw("SELECT entity_key FROM seo_entity_revisions WHERE entity_key IN ('intent:901','pkgseo:901','background:keep') ORDER BY entity_key").Scan(&keys).Error)
	require.Equal(t, []string{"background:keep"}, keys)
	var targets int64
	require.NoError(t, st.DB.Table("seo_targets").Count(&targets).Error)
	require.EqualValues(t, 15, targets)

	// Without the site origin nothing opens; with it the same switches open again.
	t.Setenv("WEB_BASE_URL", "")
	require.NoError(t, goose.UpToContext(ctx, db, ".", 109))
	require.Empty(t, switches())
	require.NoError(t, goose.DownToContext(ctx, db, ".", 108))
	t.Setenv("WEB_BASE_URL", "https://site.example")
	require.NoError(t, goose.UpToContext(ctx, db, ".", 109))
	require.Equal(t, want, switches())
}

// The previous release's link merge refuses to run when a foreign key to packages is missing from its own
// table list. 00109's tables carry none this release, so those workers keep merging during a rollout or
// after a rollback, on 109 and on 110.
func TestIntentPageTablesStayOutOfThePackageForeignKeys(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 109)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	referencing := func() []string {
		t.Helper()
		var tables []string
		// The query link merge runs before it deletes anything.
		require.NoError(t, st.DB.Raw("SELECT DISTINCT conrelid::regclass::text FROM pg_constraint WHERE contype='f' AND confrelid='packages'::regclass").Scan(&tables).Error)
		return tables
	}
	for _, version := range []int64{109, 110} {
		require.NoError(t, goose.UpToContext(ctx, db, ".", version))
		tables := referencing()
		require.NotEmpty(t, tables, "the query still sees the other relationships")
		require.NotContains(t, tables, "package_intent_pages", version)
		require.NotContains(t, tables, "package_seo_overrides", version)
	}
	// Without a key, deleting an app leaves its rows for the daily cleanup instead of failing.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(9301,'cask','keyless','keyless','homebrew/cask','Keyless','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(9301,'alternatives','en-US')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(9301,'en-US','Keyless')`).Error)
	require.NoError(t, st.DB.Exec(`DELETE FROM packages WHERE id=9301`).Error)
	var rows int64
	require.NoError(t, st.DB.Raw("SELECT (SELECT count(*) FROM package_intent_pages WHERE package_id=9301)+(SELECT count(*) FROM package_seo_overrides WHERE package_id=9301)").Scan(&rows).Error)
	require.EqualValues(t, 2, rows)
}
