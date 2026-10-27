//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFactBackfillCommitsBoundedBatchesAndResumes(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 32)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,min_macos,raw,raw_hash,artifacts,updated_at)
 SELECT i,'cask','backfill-'||i,'backfill-'||i,'homebrew/cask','Fixture','1','1','13','{}','fixture',
 jsonb_build_object('entries',jsonb_build_array(jsonb_build_object('phase','uninstall','declaration',jsonb_build_object('launchctl','org.example.backfill.'||i)))),'2024-01-01'
 FROM generate_series(1,260) i`).Error)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO articles(id,slug,working_revision_id) VALUES(1,'backfill',1),(2,'backfill-missing',2); INSERT INTO article_revisions(id,article_id,number,source_locale,type,cover_alt) VALUES(1,1,1,'en-US','news','Original cover'),(2,2,1,'en-US','news','Missing source cover'); INSERT INTO article_revision_i18n(revision_id,locale,source_locale,title) VALUES(1,'en-US','en-US','Fixture'),(1,'zh-CN','en-US','Fixture'),(2,'en-US','en-US','Fixture')`).Error; err != nil {
			return err
		}
		return nil
	}))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 43))
	var n int64
	require.NoError(t, st.DB.Table("background_item_packages").Count(&n).Error)
	require.Zero(t, n, "DDL must not backfill under its lock")
	require.NoError(t, st.DB.Table("package_version_requirements").Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, st.DB.Exec(`UPDATE article_revision_i18n SET cover_alt='Later editor value' WHERE revision_id=1 AND locale='en-US';
 UPDATE packages SET homebrew_checked_at='2026-01-01' WHERE id=1;
 CREATE FUNCTION stop_fact_backfill() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id=257 THEN RAISE EXCEPTION 'fixture interruption'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER stop_fact_backfill BEFORE UPDATE OF homebrew_checked_at ON packages FOR EACH ROW EXECUTE FUNCTION stop_fact_backfill()`).Error)
	require.ErrorContains(t, goose.UpToContext(ctx, db, ".", 44), "fixture interruption")
	require.NoError(t, st.DB.Table("background_item_packages").Count(&n).Error)
	require.EqualValues(t, 256, n)
	var cursor int64
	require.NoError(t, st.DB.Raw(`SELECT (cursor->>'id')::bigint FROM fact_metadata_backfill_progress WHERE name='00044:packages'`).Scan(&cursor).Error)
	require.EqualValues(t, 256, cursor)
	var ctid string
	require.NoError(t, st.DB.Raw(`SELECT ctid::text FROM background_item_packages WHERE package_id=1`).Scan(&ctid).Error)
	require.NoError(t, st.DB.Exec(`DROP TRIGGER stop_fact_backfill ON packages; DROP FUNCTION stop_fact_backfill()`).Error)
	require.NoError(t, goose.UpContext(ctx, db, "."))
	require.NoError(t, st.DB.Table("background_item_packages").Count(&n).Error)
	require.EqualValues(t, 260, n)
	var after string
	require.NoError(t, st.DB.Raw(`SELECT ctid::text FROM background_item_packages WHERE package_id=1`).Scan(&after).Error)
	require.Equal(t, ctid, after, "resume must not rewrite completed rows")
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM packages WHERE (id=1 AND homebrew_checked_at='2026-01-01') OR (id<>1 AND homebrew_checked_at='2024-01-01')`).Scan(&n).Error)
	require.EqualValues(t, 260, n)
	require.NoError(t, st.DB.Raw(`SELECT cover_alt FROM article_revision_i18n WHERE revision_id=1 AND locale='en-US'`).Scan(&after).Error)
	require.Equal(t, "Later editor value", after)
	require.NoError(t, st.DB.Raw(`SELECT cover_alt FROM article_revision_i18n WHERE revision_id=2 AND locale='en-US'`).Scan(&after).Error)
	require.Equal(t, "Missing source cover", after)
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM article_revision_i18n WHERE revision_id=1 AND locale='zh-CN' AND cover_alt IS NULL`).Scan(&n).Error)
	require.EqualValues(t, 1, n, "source alt backfill must not invent a translation")
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM pg_constraint WHERE connamespace='public'::regnamespace AND contype='c' AND NOT convalidated AND pg_get_constraintdef(oid) LIKE '%locale = ''zh-Hant''%'`).Scan(&n).Error)
	require.EqualValues(t, 13, n, "derived Traditional Chinese limits remain intentionally NOT VALID")
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM pg_constraint WHERE connamespace='public'::regnamespace AND contype='c' AND NOT convalidated AND pg_get_constraintdef(oid) NOT LIKE '%locale = ''zh-Hant''%'`).Scan(&n).Error)
	require.Zero(t, n, "unexpected unvalidated constraint")
	require.Error(t, st.DB.Exec(`INSERT INTO feedback(type,content,platform,links) VALUES('other','fixture','web','{}')`).Error)
}

func TestFactTriggersSkipUnchangedDerivedRows(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,min_macos,raw,raw_hash,artifacts)
 VALUES(1,'cask','write-fixture','write-fixture','homebrew/cask','Fixture','1','1','13','{}','fixture',
 '{"entries":[{"phase":"uninstall","declaration":{"launchctl":["org.example.keep","org.example.remove"]}}]}')`).Error)
	read := func(table, where string) string {
		var s string
		require.NoError(t, st.DB.Raw("SELECT string_agg(ctid::text||':'||checked_at::text,',' ORDER BY ctid) FROM "+table+" WHERE "+where).Scan(&s).Error)
		return s
	}
	b := read("background_item_packages", "package_id=1")
	v := read("package_version_requirements", "package_id=1")
	require.NoError(t, st.DB.Exec(`DELETE FROM package_page_dirty; UPDATE packages SET artifacts=artifacts,dependencies=dependencies,version=version,tap=tap,raw=raw,updated_at=now()+interval '1 day' WHERE id=1`).Error)
	require.Equal(t, b, read("background_item_packages", "package_id=1"))
	require.Equal(t, v, read("package_version_requirements", "package_id=1"))
	var n int64
	require.NoError(t, st.DB.Table("package_page_dirty").Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, st.ConfirmHomebrewDefinitions(context.Background(), []store.HomebrewConfirmation{{Token: "write-fixture", Tap: "homebrew/cask", RawHash: "fixture"}}, time.Now()))
	require.Equal(t, b, read("background_item_packages", "package_id=1"), "freshness lives on packages, without rewriting declarations")
	require.Equal(t, v, read("package_version_requirements", "package_id=1"))
	require.NoError(t, st.DB.Exec(`UPDATE packages SET raw='{"unrelated":"changed"}' WHERE id=1`).Error)
	require.Equal(t, b, read("background_item_packages", "package_id=1"))
	require.Equal(t, v, read("package_version_requirements", "package_id=1"))
	keep := read("background_item_packages", "label='org.example.keep'")
	require.NoError(t, st.DB.Exec(`UPDATE packages SET artifacts='{"entries":[{"phase":"uninstall","declaration":{"launchctl":["org.example.keep","org.example.new"]}}]}' WHERE id=1`).Error)
	require.Equal(t, keep, read("background_item_packages", "label='org.example.keep'"))
	require.Equal(t, v, read("package_version_requirements", "package_id=1"))
	var labels []string
	require.NoError(t, st.DB.Raw(`SELECT label FROM background_item_packages ORDER BY label`).Scan(&labels).Error)
	require.Equal(t, []string{"org.example.keep", "org.example.new"}, labels)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET min_macos='14' WHERE id=1`).Error)
	require.NotEqual(t, v, read("package_version_requirements", "package_id=1"))
	var platforms string
	require.NoError(t, st.DB.Raw(`SELECT platforms::text FROM package_version_requirements WHERE package_id=1`).Scan(&platforms).Error)
	require.Contains(t, platforms, `"minMacos": "14"`)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET dependencies='{"platforms":[{"version":"1","tag":"arm64_sonoma","arch":"arm64","minMacos":"14","artifacts":{"entries":[]}}]}' WHERE id=1`).Error)
	v = read("package_version_requirements", "package_id=1")
	require.NoError(t, st.DB.Exec(`UPDATE packages SET dependencies=jsonb_set(dependencies,'{platforms,0,artifacts}','{"entries":[{"phase":"install","source":"Example.app"}]}') WHERE id=1`).Error)
	require.Equal(t, v, read("package_version_requirements", "package_id=1"), "unrelated artifacts do not change compatibility snapshots")
}

func TestFactDownRefusesStoredEvidence(t *testing.T) {
	// The Down guards run where every table they protect still lives; 00110 archives the SEO ones.
	st := testutil.NewSEOStoreAtVersion(t, 109)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(1,'cask','down-fixture','down-fixture','homebrew/cask','Fixture','1','1','{}','fixture'); INSERT INTO collections(id,slug) VALUES(1,'fixture')`).Error)
	cases := []struct{ file, seed string }{
		{"00033_fact_records.sql", `INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(1,'security','fixture','{}','{"sourceUrl":"https://example.com","status":"verified","checkedAt":"2026-01-01","validUntil":"2027-01-01"}')`},
		{"00034_app_store_prices.sql", `INSERT INTO app_store_price_snapshots VALUES(1,'2026-01-01','US','org.example.fixture',0,'USD','https://apps.apple.com/example','2026-01-01')`},
		{"00035_cve_checks.sql", `INSERT INTO package_cve_checks VALUES(1,'fixture','1','[]','2026-01-01','2027-01-01')`},
		{"00036_background_items.sql", `INSERT INTO background_items VALUES('org.example.fixture')`},
		{"00037_collection_setup.sql", `INSERT INTO collection_setup_criteria VALUES(1,'en-US','Fixture','{}')`},
		{"00038_version_requirements.sql", `INSERT INTO package_version_requirements VALUES(1,'1','[]','https://example.com','2026-01-01')`},
		{"00039_private_feedback.sql", `INSERT INTO feedback(type,content,platform) VALUES('other','fixture','web')`},
		{"00040_app_integrations_flag.sql", `UPDATE app_config SET value='true' WHERE key='web.appIntegrationsEnabled'`},
		{"00041_asset_renditions.sql", `INSERT INTO assets(kind,storage_key,url,mime,bytes,sha256,files) VALUES('icon','fixture','https://example.com/icon','image/webp',1,'fixture','[{"role":"original"}]')`},
		{"00042_article_cover_alt.sql", `INSERT INTO articles(id,slug,working_revision_id) VALUES(1,'fixture',1); INSERT INTO article_revisions(id,article_id,number,source_locale,type) VALUES(1,1,1,'en-US','news'); INSERT INTO article_revision_i18n(revision_id,locale,source_locale,cover_alt) VALUES(1,'en-US','en-US','Fixture cover')`},
		{"00043_homebrew_confirmation.sql", `UPDATE packages SET homebrew_checked_at=now() WHERE id=1`},
		{"00090_seo_tuning_import.sql", `INSERT INTO seo_import_cursors(owner,profile,cursor) VALUES('fixture','fixture','{}')`},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := migrations.Files.ReadFile(tc.file)
			require.NoError(t, err)
			down := strings.Split(string(raw), "-- +goose Down")[1]
			err = st.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(tc.seed).Error; err != nil {
					return err
				}
				return tx.Exec(down).Error
			})
			require.ErrorContains(t, err, "before Down", "populated rollback must refuse before dropping any evidence")
		})
	}
}
