//go:build integration

package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOLinkRenameNewestReferencesAuditAndRollback(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "seo-old")}}))
	previous, err := st.CatalogRow(ctx, "cask", "seo-old")
	require.NoError(t, err)
	to := previous.ID
	from := remediationLink(t, st, "seo-new")
	exec := func(q string, args ...any) { t.Helper(); require.NoError(t, st.DB.Exec(q, args...).Error) }
	scalar := func(q string, args ...any) string {
		t.Helper()
		var s string
		require.NoError(t, st.DB.Raw(q, args...).Scan(&s).Error)
		return s
	}
	old, next := fmt.Sprintf("pkg:%d", from), fmt.Sprintf("pkg:%d", to)
	exec("INSERT INTO seo_urls(url,page_kind,locale,package_id) VALUES('https://example.org/old','overview','en-US',?)", from)
	exec("INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,volume_state,observed_at) VALUES(901,'editor','editor','gsc','google','US','en-US','desktop','2026-10','unknown',now())")
	exec("INSERT INTO seo_keyword_links VALUES(901,?,'price',0.3,'candidate')", to)
	exec("INSERT INTO seo_keyword_links VALUES(901,?,'price',0.9,'confirmed')", from)
	exec("INSERT INTO seo_targets VALUES(?,'price','en-US','https://example.org/older','2026-01-01'),(?,'price','en-US','https://example.org/newer','2026-02-01')", next, old)
	exec(`INSERT INTO seo_tasks(id,kind,entity_key,dedupe_key,rule_version,evidence,updated_at,state,lease_owner,lease_until,fencing) VALUES
 (901,'research',?,?, 'fixture',jsonb_build_object('packageId',?::bigint),'2026-01-01','open',NULL,NULL,1),
 (902,'research',?,?, 'fixture',jsonb_build_object('packageId',?::bigint,'ref',jsonb_build_object('entity','package','id',?::bigint)),'2026-02-01','leased','fixture',now()+interval '1 hour',7)`, next, "research:"+next, to, old, "research:"+old, from, from)
	exec("INSERT INTO seo_entity_leases VALUES(?,902,'fixture',7,now()+interval '1 hour'),(?,901,'fixture',1,now()+interval '1 hour')", old, next)
	exec(`INSERT INTO seo_evidence_snapshots(id,entity_key,source_url,final_url,body_text,excerpt,body_hash,fetched_at,valid_until,authority,fetch_status,created_by,capture_key,request_hash) VALUES
 (901,?,'https://example.org/docs','https://example.org/docs','text','text',repeat('a',64),'2026-01-01','2027-01-01','official',200,'fixture','capture-one','hash'),
 (902,?,'https://example.org/docs','https://example.org/docs','text','text',repeat('a',64),'2026-01-01','2027-01-01','official',200,'fixture','capture-two','hash')`, next, old)
	exec(`INSERT INTO seo_actions(id,task_id,idempotency_key,request_hash,owner,mode,operation,object,object_id,fields,before_data,after_data,base_revision,evidence_ids,rule_version,playbook_version,result) VALUES(901,901,'unchanged-idempotency',repeat('b',64),'fixture','proposal','fixture','package',?,ARRAY['price'],jsonb_build_object('packageId',?::bigint),'{}',0,ARRAY[901,902],'fixture','fixture','{}')`, fmt.Sprint(from), from)
	exec("INSERT INTO seo_gate_events(request_id,task_id,owner,operation,rule_version,result,reason,evidence_ids) VALUES('fixture',901,'fixture','fixture','fixture','proposal','fixture',ARRAY[901])")
	exec("INSERT INTO package_page_state(package_id,locale,page_kind,page_number,content_hash,content_modified_at) VALUES(?,'en-US','overview',1,repeat('a',64),now()),(?,'en-US','overview',1,repeat('b',64),now())", to, from)
	exec("INSERT INTO admin_users(id,user_name,password_hash) VALUES(901,'merge-reviewer','fixture-not-a-password-hash')")
	exec("INSERT INTO seo_listing_baselines(package_id,domain,developer,team_id,fact_id,evidence_id,evidence_hash,verified_by,valid_until) VALUES(?,'example.org','Fixture','ABCDEFGHIJ',1,901,repeat('a',64),901,now()+interval '1 day')", from)
	exec("INSERT INTO seo_listing_candidates(token,state,payload,baseline_package_id,package_id,updated_at) VALUES('seo-old','published',jsonb_build_object('baselinePackageId',?::bigint,'evidenceId',901),?,?,'2026-01-01'),('seo-new','published',jsonb_build_object('baselinePackageId',?::bigint,'evidenceId',901),?,?,'2026-02-01')", to, to, to, from, from, from)
	exec("UPDATE seo_listing_candidates c SET payload=payload||jsonb_build_object('link',jsonb_build_object('homepage',l.homepage,'downloadUrl',l.download_url,'teamId',l.team_id)) FROM link_apps l WHERE c.package_id=l.package_id")
	exec("INSERT INTO seo_daily_entities VALUES('fixture','2026-10-19','research',?,'old-request','2026-01-01'),('fixture','2026-10-19','research',?,'new-request','2026-02-01')", next, old)
	mutation := store.CatalogMutation{Package: remediationCask(t, "seo-new"), Previous: previous, RenamedFrom: "seo-old"}
	exec(`CREATE FUNCTION reject_seo_merge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.token='seo-new' AND NEW.kind='cask' THEN RAISE EXCEPTION 'rollback fixture'; END IF; RETURN NEW; END $$`)
	exec("CREATE TRIGGER reject_seo_merge BEFORE UPDATE ON packages FOR EACH ROW EXECUTE FUNCTION reject_seo_merge()")
	require.ErrorContains(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{mutation}), "rollback fixture")
	require.Equal(t, "2", scalar("SELECT count(*) FROM seo_tasks WHERE id IN (901,902)"))
	require.Equal(t, "0", scalar("SELECT count(*) FROM audit_logs WHERE action='link.adopt.rename'"))
	require.Equal(t, old, scalar("SELECT entity_key FROM seo_evidence_snapshots WHERE id=902"))
	exec("DROP TRIGGER reject_seo_merge ON packages")
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{mutation}))
	require.Equal(t, fmt.Sprint(to), scalar("SELECT package_id FROM seo_urls WHERE url='https://example.org/old'"))
	require.Equal(t, "confirmed", scalar("SELECT state FROM seo_keyword_links WHERE package_id=?", to))
	require.Equal(t, "https://example.org/newer", scalar("SELECT target_url FROM seo_targets WHERE entity_key=?", next))
	require.Equal(t, "0", scalar("SELECT count(*) FROM seo_tasks WHERE id=901"))
	require.Equal(t, next, scalar("SELECT entity_key FROM seo_tasks WHERE id=902"))
	require.Equal(t, fmt.Sprint(to), scalar("SELECT evidence->>'packageId' FROM seo_tasks WHERE id=902"))
	require.Equal(t, fmt.Sprint(to), scalar("SELECT evidence->'ref'->>'id' FROM seo_tasks WHERE id=902"))
	require.Equal(t, "open", scalar("SELECT state FROM seo_tasks WHERE id=902"))
	require.Equal(t, "0", scalar("SELECT count(*) FROM seo_entity_leases WHERE owner IS NOT NULL"))
	require.Equal(t, "902", scalar("SELECT task_id FROM seo_actions WHERE id=901"))
	require.Equal(t, "{902}", scalar("SELECT evidence_ids::text FROM seo_actions WHERE id=901"))
	require.Equal(t, fmt.Sprint(to), scalar("SELECT object_id FROM seo_actions WHERE id=901"))
	require.Equal(t, fmt.Sprint(from), scalar("SELECT before_data->>'packageId' FROM seo_actions WHERE id=901"))
	require.Equal(t, "unchanged-idempotency", scalar("SELECT idempotency_key FROM seo_actions WHERE id=901"))
	require.Equal(t, "902", scalar("SELECT task_id FROM seo_gate_events WHERE request_id='fixture'"))
	require.Equal(t, "{902}", scalar("SELECT evidence_ids::text FROM seo_gate_events WHERE request_id='fixture'"))
	require.Equal(t, "0", scalar("SELECT count(*) FROM package_page_state WHERE package_id IN (?,?)", from, to))
	require.Equal(t, "1", scalar("SELECT count(*) FROM package_page_dirty WHERE package_id=?", to))
	require.Equal(t, "0", scalar("SELECT count(*) FROM packages WHERE id=?", from))
	require.Equal(t, "true", scalar("SELECT suspicious FROM seo_listing_baselines WHERE package_id=?", to))
	require.Equal(t, "902", scalar("SELECT evidence_id FROM seo_listing_baselines WHERE package_id=?", to))
	require.Equal(t, "seo-new", scalar("SELECT token FROM seo_listing_candidates WHERE package_id=?", to))
	require.Equal(t, fmt.Sprint(to), scalar("SELECT baseline_package_id FROM seo_listing_candidates WHERE package_id=?", to))
	require.Equal(t, fmt.Sprint(from), scalar("SELECT payload->>'baselinePackageId' FROM seo_listing_candidates WHERE package_id=?", to))
	require.Equal(t, "902", scalar("SELECT payload->>'evidenceId' FROM seo_listing_candidates WHERE package_id=?", to))
	require.Equal(t, "new-request", scalar("SELECT first_request_id FROM seo_daily_entities WHERE entity_key=?", next))
	require.Equal(t, "0", scalar("SELECT count(*) FROM seo_daily_entities WHERE entity_key=?", old))
	audit := scalar("SELECT before::text FROM audit_logs WHERE action='link.adopt.rename'")
	for _, value := range []string{"seo_targets", "seo_tasks", "seo_evidence_snapshots", "seo_actions", "seo_gate_events", "capture-one", "capture-two", "https://example.org/older"} {
		require.Contains(t, audit, value)
	}
}
