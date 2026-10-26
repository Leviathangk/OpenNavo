//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicationRollbackPreservesEvidence(t *testing.T) {
	// 00110 archives the listing tables; check their rollback guard where they still live.
	st := testutil.NewSEOStoreAtVersion(t, 109)
	refuse := func(name, message string) {
		raw, err := migrations.Files.ReadFile(name)
		require.NoError(t, err)
		parts := strings.Split(string(raw), "-- +goose Down")
		require.Len(t, parts, 2)
		err = st.DB.Transaction(func(tx *gorm.DB) error { return tx.Exec(parts[1]).Error })
		require.ErrorContains(t, err, message)
	}
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_listing_candidates(token,payload) VALUES('rollback-fixture','{}')`).Error)
	refuse("00055_seo_listing_candidates.sql", "Archive and explicitly resolve publication evidence")
	var candidates int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM seo_listing_candidates").Scan(&candidates).Error)
	require.Equal(t, 1, candidates)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO articles(id,slug,working_revision_id) VALUES(901,'rollback-fixture',901)`).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO article_revisions(id,article_id,number,source_locale,type,claims)
 VALUES(901,901,1,'en-US','news','[{"text":"Fixture claim"}]')`).Error
	}))
	refuse("00056_article_evidence_artifacts.sql", "Archive and explicitly resolve publication evidence")
	var claims string
	require.NoError(t, st.DB.Raw("SELECT claims::text FROM article_revisions WHERE id=901").Scan(&claims).Error)
	require.Contains(t, claims, "Fixture claim")
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(901,'cask','rollback-fixture','rollback-fixture','homebrew/cask','Fixture','1','1','{}','fixture');
 INSERT INTO package_page_state(package_id,locale,page_kind,content_hash,content_modified_at) VALUES(901,'en-US','overview',repeat('a',64),'2026-01-01')`).Error)
	refuse("00052_package_page_state.sql", "Archive and explicitly resolve page content dates")
	var states int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM package_page_state").Scan(&states).Error)
	require.Equal(t, 1, states)
}

func TestSEOBatchMigrationRoundTrip(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 57)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	// Empty isolated database: checks DDL dependencies, not a populated-data rollback.
	require.NoError(t, goose.DownToContext(ctx, db, ".", 32))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 57))
	var unvalidated int
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace
 WHERE n.nspname='public' AND c.contype='c' AND NOT c.convalidated`).Scan(&unvalidated).Error)
	require.Zero(t, unvalidated, "00057 validates checks after metadata locks have committed")
}

func TestPageDirtyDeduplicationAndDependencyPlans(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 SELECT 'cask','review-fixture-'||i,'review-fixture-'||i,'homebrew/cask','Fixture '||i,'1','1','{}','fixture'
 FROM generate_series(1,128) i`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET dependencies='{"dependsOn":{"cask":["review-fixture-1"]}}' WHERE token='review-fixture-2'`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source)
 SELECT a.id,b.id,0,ARRAY['similar'],'admin' FROM packages a,packages b WHERE a.token='review-fixture-3' AND b.token='review-fixture-1'`).Error)
	require.NoError(t, st.DB.Exec("DELETE FROM package_page_dirty").Error)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE packages SET name='Changed fixture' WHERE token='review-fixture-1'").Error; err != nil {
			return err
		}
		var before, after string
		q := `SELECT string_agg(package_id||':'||ctid::text,',' ORDER BY package_id) FROM package_page_dirty`
		require.NoError(t, tx.Raw(q).Scan(&before).Error)
		if err := tx.Exec("UPDATE packages SET name='Changed again' WHERE token='review-fixture-1'").Error; err != nil {
			return err
		}
		require.NoError(t, tx.Raw(q).Scan(&after).Error)
		require.Equal(t, before, after, "same transaction must not rewrite already queued rows")
		return nil
	}))
	var tokens []string
	require.NoError(t, st.DB.Raw(`SELECT p.token FROM package_page_dirty d JOIN packages p ON p.id=d.package_id ORDER BY p.token`).Scan(&tokens).Error)
	require.Equal(t, []string{"review-fixture-1", "review-fixture-2", "review-fixture-3"}, tokens)
	require.NoError(t, st.DB.Exec("DELETE FROM package_page_dirty").Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET dependencies='{}' WHERE token='review-fixture-2'`).Error)
	require.NoError(t, st.DB.Raw(`SELECT p.token FROM package_page_dirty d JOIN packages p ON p.id=d.package_id ORDER BY p.token`).Scan(&tokens).Error)
	require.Contains(t, tokens, "review-fixture-1", "removing an edge must refresh its former destination")
	require.NoError(t, st.DB.Exec("ANALYZE packages").Error)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		// A tiny fixture may rationally prefer a sequential scan. This verifies index
		// eligibility without asserting timings or pretending to model production cost.
		var defaultPlan []string
		require.NoError(t, tx.Raw(`EXPLAIN (COSTS OFF) SELECT id FROM packages WHERE removed_at IS NULL AND dependencies->'dependsOn'->'cask' @> '["review-fixture-1"]'`).Scan(&defaultPlan).Error)
		t.Log("Small-fixture default plan:\n" + strings.Join(defaultPlan, "\n"))
		// Remove competing broad partial indexes only inside this rolled-back probe.
		// The remaining plan demonstrates GIN eligibility, not production selection.
		require.NoError(t, tx.SavePoint("explain_probe").Error)
		require.NoError(t, tx.Exec("DROP INDEX packages_kind_changed,packages_kind_popularity").Error)
		if err := tx.Exec("SET LOCAL enable_seqscan=off").Error; err != nil {
			return err
		}
		for _, check := range []struct{ query, index string }{
			{`SELECT id FROM packages WHERE removed_at IS NULL AND dependencies->'dependsOn'->'cask' @> '["review-fixture-1"]'`, "packages_depends_on_cask"},
			{`SELECT id FROM packages WHERE kind='cask' AND token='review-fixture-1'`, "Index Cond: (token ="},
			{`SELECT package_id FROM package_page_dirty ORDER BY changed_at,package_id LIMIT 100 FOR UPDATE SKIP LOCKED`, "package_page_dirty_order"},
		} {
			var plan []string
			require.NoError(t, tx.Raw("EXPLAIN (COSTS OFF) "+check.query).Scan(&plan).Error)
			require.Contains(t, strings.Join(plan, "\n"), check.index)
			t.Log(strings.Join(plan, "\n"))
		}
		return tx.RollbackTo("explain_probe").Error
	}))
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"intentPagesEnabled":true}'::jsonb) WHERE id=true`).Error)
	policy, err := st.IndexPolicy(ctx)
	require.NoError(t, err)
	require.True(t, policy.IntentPagesEnabled)
}
