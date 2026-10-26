//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var retiredSEOTables = []string{"seo_tasks", "seo_entity_leases", "seo_actions", "seo_action_guards",
	"seo_experiments", "seo_experiment_pages", "seo_experiment_checkpoints", "seo_geo_snapshots",
	"seo_gsc_page_daily", "seo_gsc_query_daily", "seo_bing_daily", "seo_bing_crawl_daily", "seo_audit_issues",
	"seo_urls", "seo_url_status", "seo_internal_links", "seo_link_crawls", "seo_inspection_quota",
	"seo_data_watermarks", "seo_watermark_events", "seo_ingest_days",
	"seo_breakers", "seo_cost_receipts", "seo_control_receipts", "seo_daily_entities",
	"seo_opportunity_cursors", "seo_import_cursors", "seo_keywords", "seo_keyword_links", "seo_targets",
	"seo_listing_candidates", "seo_listing_baselines", "seo_translation_corrections",
	"seo_execution_policy_backup", "seo_token_policy_backup", "seo_provider_settings"}

var renamedWriteTables = map[string]string{"seo_task_controls": "agent_write_controls", "seo_gate_events": "agent_write_events",
	"seo_entity_revisions": "entity_revisions", "seo_evidence_snapshots": "evidence_snapshots"}

// The token default of the release before the removal (00081), which its admin still uses
// while both releases run.
var previousTokenDefault = append([]string{"seo:snippet:propose", "seo:task:handle", "seo:insight:view", "seo:keyword:edit"},
	slices.DeleteFunc(slices.Clone(agent.Grantable), func(p string) bool { return p == "site:index:view" })...)

func TestRetireSEOEngineArchivesTablesAndKeepsCoreWrites(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 109)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	exec := func(q string, args ...any) { t.Helper(); require.NoError(t, st.DB.Exec(q, args...).Error) }
	scalar := func(q string, args ...any) string {
		t.Helper()
		var s string
		require.NoError(t, st.DB.Raw(q, args...).Scan(&s).Error)
		return s
	}
	exists := func(name string) bool {
		t.Helper()
		return scalar("SELECT (to_regclass(?) IS NOT NULL)::text", name) == "true"
	}
	kind := func(name string) string {
		t.Helper()
		return scalar("SELECT coalesce((SELECT relkind::text FROM pg_class WHERE oid=to_regclass(?)),'none')", name)
	}
	permissions := func(name string) string {
		t.Helper()
		return scalar("SELECT array_to_string(permissions,' ') FROM agent_tokens WHERE name=?", name)
	}
	revision := func(table, key string) string {
		t.Helper()
		return scalar("SELECT coalesce((SELECT revision::text FROM "+table+" WHERE entity_key=?),'none')", key)
	}

	exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (9101,'cask','retire-cask','retire-cask','homebrew/cask','Retire Cask','1.0','1.0','{}','fixture'),
 (9102,'cask','retire-gone','retire-gone','homebrew/cask','Retire Gone','1.0','1.0','{}','fixture'),
 (9103,'cask','retire-other','retire-other','homebrew/cask','Retire Other','1.0','1.0','{}','fixture')`)
	exec("INSERT INTO admin_users(id,user_name,password_hash) VALUES(9101,'retire-reviewer','fixture-not-a-password-hash')")
	exec("INSERT INTO admin_users(id,user_name,password_hash,is_machine) VALUES(9102,'retire-agent','unusable',true)")
	exec("INSERT INTO seo_urls(id,url,page_kind,locale,package_id) VALUES(9101,'https://example.test/apps/retire-gone','overview','en-US',9102)")
	exec("INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,volume_state,observed_at) VALUES(9101,'retire','retire','gsc','google','US','en-US','desktop','2026-10','unknown',now())")
	exec("INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES(9101,9102,'alternative',0.9,'confirmed')")
	exec("INSERT INTO seo_listing_baselines(package_id,domain,developer,team_id,fact_id,evidence_id,evidence_hash,verified_by,valid_until) VALUES(9102,'example.test','Fixture','ABCDEFGHIJ',1,1,repeat('a',64),9101,now()+interval '1 day')")
	exec("INSERT INTO seo_listing_candidates(token,state,payload,package_id) VALUES('retire-gone','published','{}',9102)")
	exec("INSERT INTO seo_tasks(id,kind,entity_key,dedupe_key,rule_version) VALUES(9101,'translate','package:9101','retire-task','fixture')")
	exec("INSERT INTO seo_gate_events(request_id,task_id,owner,operation,rule_version,result,reason) VALUES('retire-event',9101,'fixture','UpdatePackageMeta','fixture','live','validated')")
	exec("INSERT INTO seo_task_controls(owner,idempotency_key,operation,request_hash,result) VALUES('agent:1:1','retire-key-1','UpdatePackageMeta',repeat('c',64),'{}')")
	exec(`INSERT INTO seo_evidence_snapshots(id,entity_key,source_url,final_url,body_text,excerpt,body_hash,fetched_at,valid_until,authority,fetch_status,created_by,capture_key,request_hash)
 VALUES(9101,'pkg:9101','https://example.test/docs','https://example.test/docs','text','text',repeat('a',64),now(),now()+interval '30 days','official',200,'fixture','retire-capture','hash')`)
	exec(`UPDATE seo_settings SET automation=automation||'{"marker":true}'`)
	exec("UPDATE seo_provider_settings SET enabled=true,credentials_ciphertext='fixture-ciphertext' WHERE source='gsc'")
	exec("INSERT INTO background_items(label) VALUES('com.example.retire')")
	exec("INSERT INTO background_item_content(label,locale,description,item_type,evidence) VALUES('com.example.retire','en-US','Fixture agent','launch_agent','{}')")
	require.Equal(t, "1", revision("seo_entity_revisions", "background:com.example.retire"))
	keywords := revision("seo_entity_revisions", "site:keywords")

	var clientID, grantID, roleID int64
	require.NoError(t, st.DB.Raw("INSERT INTO agent_clients(user_id,name) VALUES(9102,'Retire fixture') RETURNING id").Scan(&clientID).Error)
	exec(`INSERT INTO agent_tokens(client_id,name,token_hash,token_prefix,permissions) VALUES
 (?,'reader',repeat('1',64),'onv_aaaa',ARRAY['seo:insight:view','seo:keyword:edit','catalog:package:view']),
 (?,'tuner',repeat('2',64),'onv_bbbb',ARRAY['seo:automation:tune','seo:insight:view','dashboard:view']),
 (?,'executor',repeat('3',64),'onv_cccc',ARRAY['seo:task:handle','seo:snippet:propose','catalog:package:edit']),
 (?,'plain',repeat('4',64),'onv_dddd',ARRAY['catalog:package:view'])`, clientID, clientID, clientID, clientID)
	exec(`INSERT INTO mcp_oauth_clients(id,name,redirect_uris,scopes) VALUES('retire-client','Retire','["https://example.test/cb"]','seo:task:handle catalog:package:view seo:snippet:propose')`)
	require.NoError(t, st.DB.Raw(`INSERT INTO mcp_oauth_grants(client_id,user_id,session_version,agent_client_id,scopes,expires_at) VALUES('retire-client',9101,1,?,'seo:insight:view dashboard:view',now()+interval '1 day') RETURNING id`, clientID).Scan(&grantID).Error)
	require.NoError(t, st.DB.Raw("INSERT INTO admin_roles(role_code,role_name) VALUES('R_RETIRE','Retire fixture') RETURNING id").Scan(&roleID).Error)
	exec("INSERT INTO admin_role_permissions(role_id,permission_code) VALUES(?,'seo:insight:view'),(?,'seo:keyword:edit'),(?,'dashboard:view')", roleID, roleID, roleID)
	exec("INSERT INTO content_revision_requests(request_id) VALUES('retire-request')")
	exec(`INSERT INTO content_revisions(entity,object_key,version,action,operation_id,required_permissions,actor_type,request_id,before_data,after_data) VALUES
 ('package','9101',1,'update','compensateSeoAction',ARRAY['seo:task:handle'],'agent','retire-request','{}','{"id":9101}'),
 ('package','9101',2,'update','nominateSeoListing',ARRAY['catalog:package:edit','seo:task:handle'],'agent','retire-request','{}','{"id":9101}'),
 ('package','9101',3,'update','updatePackageMeta',ARRAY['catalog:package:edit'],'admin','retire-request','{}','{"id":9101}')`)

	// The previous release connects as the application role with the grants of
	// internal/cli/dbbootstrap; views created later inherit them as default privileges.
	for _, q := range []string{"CREATE ROLE previous_app NOLOGIN", "GRANT USAGE ON SCHEMA public TO previous_app",
		"GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO previous_app",
		"GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO previous_app",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO previous_app",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE,SELECT ON SEQUENCES TO previous_app"} {
		exec(q)
	}

	// A request reading packages holds a lock that dropping a key into packages waits for. 00110
	// gives up after its lock_timeout, at that last statement and with nothing changed, instead
	// of queueing every later request behind its own lock.
	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = blocker.ExecContext(ctx, "LOCK TABLE packages IN ACCESS SHARE MODE")
	require.NoError(t, err)
	attempt := make(chan error, 1)
	go func() { attempt <- goose.UpToContext(ctx, db, ".", 110) }()
	select {
	case err = <-attempt:
	case <-time.After(time.Minute):
		require.NoError(t, blocker.Rollback())
		t.Fatal("00110 kept waiting for packages past its lock_timeout")
	}
	require.NoError(t, blocker.Rollback())
	require.ErrorContains(t, err, "lock timeout")
	require.ErrorContains(t, err, "retired_foreign_keys", "only the final foreign key step locks packages")
	version, err := goose.GetDBVersionContext(ctx, db)
	require.NoError(t, err)
	require.EqualValues(t, 109, version)
	require.Equal(t, "0", scalar("SELECT count(*) FROM pg_namespace WHERE nspname='seo_archive'"))

	require.NoError(t, goose.UpToContext(ctx, db, ".", 110))
	t.Run("engine tables move to seo_archive behind same-name views", func(t *testing.T) {
		for _, name := range retiredSEOTables {
			require.Equal(t, "v", kind(name), name)
			require.Equal(t, "r", kind("seo_archive."+name), name)
		}
		for old, renamed := range renamedWriteTables {
			require.Equal(t, "v", kind(old), old)
			require.Equal(t, "r", kind(renamed), renamed)
			require.False(t, exists("seo_archive."+renamed), renamed)
		}
		views := append(slices.Clone(retiredSEOTables), slices.Collect(maps.Keys(renamedWriteTables))...)
		require.Equal(t, strconv.Itoa(len(views)), scalar("SELECT count(*) FROM information_schema.views WHERE table_schema='public' AND table_name IN ? AND is_updatable='YES' AND is_insertable_into='YES'", views))
		// Views own no constraints: the link merge's check of keys into packages sees only tables.
		require.Equal(t, "0", scalar(`SELECT count(*) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid
 WHERE c.contype='f' AND c.confrelid='packages'::regclass AND (r.relkind<>'r' OR r.relnamespace<>'public'::regnamespace)`))
		require.Equal(t, "0", scalar(`SELECT count(*) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_class f ON f.oid=c.confrelid
 WHERE c.contype='f' AND r.relnamespace<>f.relnamespace AND 'seo_archive'::regnamespace IN (r.relnamespace,f.relnamespace)`))
		require.Equal(t, "1", scalar("SELECT count(*) FROM seo_archive.seo_urls WHERE package_id=9102"))
		require.Equal(t, "9101", scalar("SELECT task_id FROM agent_write_events WHERE request_id='retire-event'"))
		require.Equal(t, "agent_write_events_pkey", scalar("SELECT conname FROM pg_constraint WHERE conrelid='agent_write_events'::regclass AND contype='p'"))
		require.True(t, exists("agent_write_events_id_seq"))
		require.True(t, exists("evidence_snapshots_id_seq"))
		require.True(t, exists("evidence_snapshots_entity"))
		require.Equal(t, "seo_archive", scalar("SELECT pronamespace::regnamespace::text FROM pg_proc WHERE proname='protect_automatic_listing_package'"))
		require.Equal(t, "true", scalar("SELECT automation->>'marker' FROM seo_settings"))
		require.Equal(t, "3", scalar("SELECT count(*) FROM seo_archive.seo_provider_settings WHERE NOT enabled AND credentials_ciphertext=''"))
	})
	t.Run("core writes keep working", func(t *testing.T) {
		// Archived rows still name package 9102 but no longer constrain it.
		exec("DELETE FROM packages WHERE id=9102")
		exec("INSERT INTO agent_write_events(request_id,owner,operation,rule_version,result,reason) VALUES('after-retire','fixture','UpdatePackageMeta','agent-v1','live','validated')")
		exec("INSERT INTO agent_write_controls(owner,idempotency_key,operation,request_hash,result) VALUES('agent:1:1','retire-key-2','UpdatePackageMeta',repeat('d',64),'{}')")
		exec(`INSERT INTO evidence_snapshots(entity_key,source_url,final_url,body_text,excerpt,body_hash,fetched_at,valid_until,authority,fetch_status,created_by,capture_key,request_hash)
 VALUES('pkg:9101','https://example.test/more','https://example.test/more','text','text',repeat('b',64),now(),now()+interval '30 days','official',200,'fixture','retire-capture-2','hash')`)
		exec("UPDATE background_item_content SET description='Fixture agent v2' WHERE label='com.example.retire'")
		require.Equal(t, "2", revision("entity_revisions", "background:com.example.retire"))
		exec("INSERT INTO update_feeds(bundle_id,source,feed_url,origin) VALUES('org.fixture.Retire','sparkle','https://example.test/feed','manual')")
		require.Equal(t, "1", revision("entity_revisions", "update_feed:sparkle:org.fixture.Retire"))
		exec("INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(9101,'alternatives','en-US')")
		exec("INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(9101,'uninstall','en-US')")
		exec("DELETE FROM package_intent_pages WHERE package_id=9101 AND page_kind='uninstall'")
		require.Equal(t, "3", revision("entity_revisions", "intent:9101"))
		exec("INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(9101,'en-US','Retire Cask for Mac')")
		exec("UPDATE package_seo_overrides SET description='Plain text.' WHERE package_id=9101")
		require.Equal(t, "2", revision("entity_revisions", "pkgseo:9101"))
		// Moving a row changes both entities.
		exec("UPDATE package_seo_overrides SET package_id=9103 WHERE package_id=9101")
		require.Equal(t, "3", revision("entity_revisions", "pkgseo:9101"))
		require.Equal(t, "1", revision("entity_revisions", "pkgseo:9103"))
	})
	t.Run("the previous release keeps working", func(t *testing.T) {
		// Statements of the release before the removal, run as the application role.
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
			run := func(q string, args ...any) { t.Helper(); require.NoError(t, tx.Exec(q, args...).Error, q) }
			value := func(q string, args ...any) string {
				t.Helper()
				var s string
				require.NoError(t, tx.Raw(q, args...).Scan(&s).Error, q)
				return s
			}
			run("SET LOCAL ROLE previous_app")
			// Store.WriteContentText: every source edit expires and queues translation tasks.
			enqueue := `INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version) VALUES('translate',?,?,?,?,?::jsonb,?,'translation-v1') ON CONFLICT(dedupe_key) DO UPDATE SET state='open',priority=greatest(seo_tasks.priority,EXCLUDED.priority),fencing=seo_tasks.fencing+1,lease_owner=NULL,lease_until=NULL,next_retry_at=NULL,updated_at=now() WHERE seo_tasks.state IN ('done','expired','skipped')`
			run(enqueue, "category:880", "zh-CN", "translate:category:880:zh-CN:h1", 1, `{"sourceHash":"h1"}`, "h1")
			run(`UPDATE seo_tasks SET state='expired',lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE kind='translate' AND entity_key=? AND state IN ('open','leased','deferred') AND evidence_hash<>?`, "category:880", "h2")
			run(enqueue, "category:880", "zh-CN", "translate:category:880:zh-CN:h1", 2, `{"sourceHash":"h1"}`, "h1")
			require.Equal(t, "open 1 2", value("SELECT state||' '||fencing||' '||priority FROM seo_tasks WHERE dedupe_key='translate:category:880:zh-CN:h1'"))
			// seotasks: the expiry sweep and claims skip rows another worker holds.
			run(`WITH expired AS (SELECT id FROM seo_tasks WHERE expires_at<=clock_timestamp() AND state IN ('open','leased','deferred') ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED) UPDATE seo_tasks t SET state='expired',lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() FROM expired e WHERE t.id=e.id`)
			var claimed []int64
			require.NoError(t, tx.Raw("SELECT id FROM seo_tasks WHERE state='open' ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED").Scan(&claimed).Error)
			require.Contains(t, claimed, int64(9101))
			// seotasks.Control: the idempotency ledger.
			run("INSERT INTO seo_task_controls(owner,idempotency_key,operation,request_hash,result) VALUES(?,?,?,?,?::jsonb)", "agent:1:1", "previous-release", "UpdatePackageMeta", strings.Repeat("e", 64), "{}")
			require.Equal(t, "UpdatePackageMeta", value("SELECT operation FROM seo_task_controls WHERE owner=? AND idempotency_key=?", "agent:1:1", "previous-release"))
			// agentgate.Log: the write log, with the task it served and the evidence it used.
			run(`INSERT INTO seo_gate_events(request_id,task_id,owner,operation,rule_version,result,reason,evidence_ids) VALUES(?,(SELECT id FROM seo_tasks WHERE id=?),?,?,?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)::bigint))`,
				"previous-release", 9101, "fixture", "UpdatePackageMeta", "fixture", "live", "validated", "[9101]")
			require.Equal(t, "9101 {9101}", value("SELECT task_id||' '||evidence_ids::text FROM agent_write_events WHERE request_id='previous-release'"))
			// agentgate.Evidence and the revision read of catalog edits.
			var evidence int64
			require.NoError(t, tx.Raw(`INSERT INTO seo_evidence_snapshots(entity_key,source_url,final_url,body_text,excerpt,body_hash,fetched_at,valid_until,authority,fetch_status,created_by,capture_key,request_hash)
 VALUES('pkg:9101','https://example.test/previous','https://example.test/previous','text','text',repeat('e',64),now(),now()+interval '30 days','official',200,'fixture','previous-capture','hash') RETURNING id`).Scan(&evidence).Error)
			require.NotZero(t, evidence)
			require.Equal(t, "2", value("SELECT coalesce((SELECT revision FROM seo_entity_revisions WHERE entity_key=?),0)::text", "background:com.example.retire"))
			// agentgate reads the automation policy.
			require.Equal(t, "true", value("SELECT automation->>'marker' FROM seo_settings LIMIT 1"))
			// Providers read as unconfigured, and the settings page can still save them.
			require.Equal(t, "false:", value("SELECT enabled::text||':'||credentials_ciphertext FROM seo_provider_settings WHERE source=? FOR UPDATE", "gsc"))
			run("UPDATE seo_provider_settings SET enabled=?,credentials_ciphertext=?,updated_at=clock_timestamp() WHERE source=?", false, "", "bing")
			// The keyword ledger: imports take serial ids, and edits still bump its revision.
			var keyword int64
			require.NoError(t, tx.Raw(`INSERT INTO seo_keywords(keyword,normalized,source,engine,country,language,device,period,search_volume,volume_state,observed_at)
 VALUES('retire cask alternative','retire cask alternative','gsc','google','US','en-US','desktop','2026-10',120,'known',now()) RETURNING id`).Scan(&keyword).Error)
			run("INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES(?,9101,'alternative',0.9,'confirmed')", keyword)
			run("INSERT INTO seo_targets(entity_key,intent,locale,target_url) VALUES('pkg:9101','alternative','en-US','https://example.test/apps/retire-cask/alternatives')")
			require.NotEqual(t, keywords, value("SELECT coalesce((SELECT revision FROM seo_entity_revisions WHERE entity_key=?),0)::text", "site:keywords"))
			// Store.PopulateIntentEligibility: public app pages join the targets to the ledger.
			var targets []struct {
				PackageID                 int64
				Intent, Locale, TargetURL string
			}
			require.NoError(t, tx.Raw(`SELECT p.id AS package_id,t.intent,t.locale,t.target_url FROM seo_targets t JOIN packages p ON t.entity_key='pkg:'||p.id::text
 WHERE p.id IN ? AND t.intent IN('alternative','uninstall') AND EXISTS(
 SELECT 1 FROM seo_keyword_links l JOIN seo_keywords k ON k.id=l.keyword_id WHERE l.package_id=p.id AND l.intent=t.intent AND l.state='confirmed' AND k.language=t.locale
 AND ((k.volume_state='known' AND k.search_volume>0) OR (k.source IN ('gsc','bing') AND k.anomaly IS NULL AND k.our_position IS NOT NULL AND k.observed_at>=clock_timestamp()-interval '60 days' AND k.observed_at<=clock_timestamp())))`, []int64{9101}).Scan(&targets).Error)
			require.Len(t, targets, 1)
			require.Equal(t, "alternative", targets[0].Intent)
			// The admin creates tokens with the previous permission set.
			granted, err := json.Marshal(previousTokenDefault)
			require.NoError(t, err)
			run("INSERT INTO agent_tokens(client_id,name,token_hash,token_prefix,permissions,created_by) VALUES(?,'previous release',repeat('8',64),'onv_hhhh',ARRAY(SELECT jsonb_array_elements_text(?::jsonb)::text),9101)", clientID, string(granted))
			// The worker's link merge locks the SEO tables it archives rows from.
			run("LOCK TABLE seo_listing_baselines,seo_listing_candidates,seo_urls,seo_keyword_links,article_test_artifacts,seo_targets,seo_tasks,seo_entity_leases,seo_actions,seo_evidence_snapshots,seo_gate_events,seo_daily_entities,article_revisions IN SHARE ROW EXCLUSIVE MODE")
			return nil
		}))
		require.Equal(t, strings.Join(previousTokenDefault, " "), permissions("previous release"))
	})
	t.Run("permissions follow the index read view", func(t *testing.T) {
		require.Equal(t, "site:index:view catalog:package:view", permissions("reader"))
		require.Equal(t, "site:index:view dashboard:view", permissions("tuner"))
		require.Equal(t, "site:index:view catalog:package:edit", permissions("executor"))
		require.Equal(t, "catalog:package:view", permissions("plain"))
		// Clients keep their registration, which new code maps when it reads it; grants are rewritten.
		require.Equal(t, "seo:task:handle catalog:package:view seo:snippet:propose", scalar("SELECT scopes FROM mcp_oauth_clients WHERE id='retire-client'"))
		require.Equal(t, "site:index:view dashboard:view", scalar("SELECT scopes FROM mcp_oauth_grants WHERE id=?", grantID))
		require.Error(t, st.DB.Exec("INSERT INTO agent_tokens(client_id,name,token_hash,token_prefix,permissions) VALUES(?,'unknown',repeat('5',64),'onv_eeee',ARRAY['agent:manage'])", clientID).Error)
		exec("INSERT INTO agent_tokens(client_id,name,token_hash,token_prefix) VALUES(?,'defaulted',repeat('6',64),'onv_ffff')", clientID)
		require.Equal(t, strings.Join(agent.Grantable, " "), permissions("defaulted"))
		require.Equal(t, "site", scalar("SELECT group_name FROM admin_permissions WHERE code='site:index:view'"))
		require.Equal(t, "0", scalar("SELECT count(*) FROM admin_permissions WHERE code LIKE 'seo:%'"))
		require.Equal(t, "dashboard:view site:index:view", scalar("SELECT string_agg(permission_code,' ' ORDER BY permission_code) FROM admin_role_permissions WHERE role_id=?", roleID))
		require.Equal(t, "content:revision:restore", scalar("SELECT array_to_string(required_permissions,' ') FROM content_revisions WHERE version=1 AND object_key='9101'"))
		require.Equal(t, "catalog:package:edit", scalar("SELECT array_to_string(required_permissions,' ') FROM content_revisions WHERE version=2 AND object_key='9101'"))
		require.Equal(t, "catalog:package:edit", scalar("SELECT array_to_string(required_permissions,' ') FROM content_revisions WHERE version=3 AND object_key='9101'"))
	})

	// Changed and new credentials must survive a rollback with the closest old grant.
	exec("UPDATE agent_tokens SET permissions=ARRAY['site:index:view'] WHERE name='executor'")
	exec("INSERT INTO agent_tokens(client_id,name,token_hash,token_prefix,permissions) VALUES(?,'fresh',repeat('7',64),'onv_gggg',ARRAY['site:index:view','dashboard:view'])", clientID)
	exec("DELETE FROM agent_tokens WHERE name='defaulted'")

	require.NoError(t, goose.DownToContext(ctx, db, ".", 109))
	t.Run("down restores the 109 schema", func(t *testing.T) {
		require.Equal(t, "0", scalar("SELECT count(*) FROM pg_namespace WHERE nspname='seo_archive'"))
		for _, name := range retiredSEOTables {
			require.Equal(t, "r", kind(name), name)
		}
		for old, renamed := range renamedWriteTables {
			require.Equal(t, "r", kind(old), old)
			require.False(t, exists(renamed), renamed)
		}
		require.Equal(t, "seo_gate_events_pkey", scalar("SELECT conname FROM pg_constraint WHERE conrelid='seo_gate_events'::regclass AND contype='p'"))
		require.True(t, exists("seo_gate_events_id_seq"))
		require.True(t, exists("seo_evidence_snapshots_id_seq"))
		require.True(t, exists("seo_evidence_entity"))
		require.Equal(t, "9101", scalar("SELECT task_id FROM seo_gate_events WHERE request_id='retire-event'"))
		require.Equal(t, "1", scalar("SELECT count(*) FROM pg_constraint WHERE conname='seo_gate_events_task_id_fkey'"))
		require.Equal(t, "false", scalar("SELECT convalidated::text FROM pg_constraint WHERE conname='seo_urls_package_id_fkey'"))
		require.Equal(t, "3", scalar("SELECT count(*) FROM seo_provider_settings WHERE NOT enabled AND credentials_ciphertext=''"))
		require.Equal(t, "true", scalar("SELECT automation->>'marker' FROM seo_settings"))
		require.Equal(t, "public", scalar("SELECT pronamespace::regnamespace::text FROM pg_proc WHERE proname='protect_automatic_listing_package'"))
		require.Equal(t, "seo:insight:view seo:keyword:edit catalog:package:view", permissions("reader"))
		require.Equal(t, "seo:automation:tune seo:insight:view dashboard:view", permissions("tuner"))
		require.Equal(t, "seo:insight:view", permissions("executor"))
		require.Equal(t, "seo:insight:view dashboard:view", permissions("fresh"))
		require.Equal(t, "catalog:package:view", permissions("plain"))
		require.Equal(t, strings.Join(previousTokenDefault, " "), permissions("previous release"))
		require.Contains(t, scalar("SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='agent_tokens'::regclass AND adnum=(SELECT attnum FROM pg_attribute WHERE attrelid='agent_tokens'::regclass AND attname='permissions')"), "seo:snippet:propose")
		require.Equal(t, "seo:task:handle catalog:package:view seo:snippet:propose", scalar("SELECT scopes FROM mcp_oauth_clients WHERE id='retire-client'"))
		require.Equal(t, "seo:insight:view dashboard:view", scalar("SELECT scopes FROM mcp_oauth_grants WHERE id=?", grantID))
		require.Equal(t, "0", scalar("SELECT count(*) FROM admin_permissions WHERE code='site:index:view'"))
		require.Equal(t, "5", scalar("SELECT count(*) FROM admin_permissions WHERE code LIKE 'seo:%'"))
		require.Equal(t, "dashboard:view seo:insight:view seo:keyword:edit", scalar("SELECT string_agg(permission_code,' ' ORDER BY permission_code) FROM admin_role_permissions WHERE role_id=?", roleID))
		require.Equal(t, "seo:task:handle", scalar("SELECT array_to_string(required_permissions,' ') FROM content_revisions WHERE version=1 AND object_key='9101'"))
		require.Equal(t, "catalog:package:edit seo:task:handle", scalar("SELECT array_to_string(required_permissions,' ') FROM content_revisions WHERE version=2 AND object_key='9101'"))
		// Revisions written at 110 stay; the 109 triggers write the old ledger again.
		require.Equal(t, "2", revision("seo_entity_revisions", "background:com.example.retire"))
		before := revision("seo_entity_revisions", "site:keywords")
		exec("INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,volume_state,observed_at) VALUES(9103,'restored','restored','gsc','google','US','en-US','desktop','2026-10','unknown',now())")
		require.NotEqual(t, before, revision("seo_entity_revisions", "site:keywords"))
		exec("INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(9103,'alternatives','en-US')")
		require.Equal(t, "none", revision("seo_entity_revisions", "intent:9103"))
	})

	require.NoError(t, goose.UpToContext(ctx, db, ".", 110))
	require.Equal(t, "r", kind("seo_archive.seo_tasks"))
	require.Equal(t, "v", kind("seo_tasks"))
	require.Equal(t, "site:index:view catalog:package:view", permissions("reader"))
	require.Equal(t, "site:index:view", permissions("executor"))
	exec("INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(9103,'uninstall','en-US')")
	require.Equal(t, "1", revision("entity_revisions", "intent:9103"))
}
