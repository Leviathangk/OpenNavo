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

// Archived SEO rows keep naming the merged package without blocking the merge;
// evidence, intent switches and search titles move to the surviving package.
func TestLinkRenameMovesEditorialRowsAndIgnoresArchive(t *testing.T) {
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
	exec("INSERT INTO seo_archive.seo_urls(url,page_kind,locale,package_id) VALUES('https://example.org/old','overview','en-US',?)", from)
	exec("INSERT INTO seo_archive.seo_listing_candidates(token,state,payload,package_id) VALUES('seo-new','published','{}',?)", from)
	exec(`INSERT INTO evidence_snapshots(id,entity_key,source_url,final_url,body_text,excerpt,body_hash,fetched_at,valid_until,authority,fetch_status,created_by,capture_key,request_hash) VALUES
 (901,?,'https://example.org/docs','https://example.org/docs','text','text',repeat('a',64),'2026-01-01','2027-01-01','official',200,'fixture','capture-one','hash'),
 (902,?,'https://example.org/docs','https://example.org/docs','text','text',repeat('a',64),'2026-01-01','2027-01-01','official',200,'fixture','capture-two','hash'),
 (903,?,'https://example.org/faq','https://example.org/faq','faq','faq',repeat('c',64),'2026-01-01','2027-01-01','official',200,'fixture','capture-three','hash')`, next, old, old)
	exec("INSERT INTO package_intent_pages(package_id,page_kind,locale,created_by) VALUES(?,'alternatives','en-US','admin:1'),(?,'alternatives','en-US','system'),(?,'alternatives','ja-JP','system')", to, from, from)
	exec("INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(?,'en-US','Canonical title'),(?,'en-US','Merged title'),(?,'de-DE','Deutscher Titel')", to, from, from)
	mutation := store.CatalogMutation{Package: remediationCask(t, "seo-new"), Previous: previous, RenamedFrom: "seo-old"}
	exec(`CREATE FUNCTION reject_seo_merge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.token='seo-new' AND NEW.kind='cask' THEN RAISE EXCEPTION 'rollback fixture'; END IF; RETURN NEW; END $$`)
	exec("CREATE TRIGGER reject_seo_merge BEFORE UPDATE ON packages FOR EACH ROW EXECUTE FUNCTION reject_seo_merge()")
	require.ErrorContains(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{mutation}), "rollback fixture")
	require.Equal(t, "0", scalar("SELECT count(*) FROM audit_logs WHERE action='link.adopt.rename'"))
	require.Equal(t, old, scalar("SELECT entity_key FROM evidence_snapshots WHERE id=902"))
	require.Equal(t, "2", scalar("SELECT count(*) FROM package_intent_pages WHERE package_id=?", from))
	exec("DROP TRIGGER reject_seo_merge ON packages")
	intentRevision := fmt.Sprintf("SELECT revision::text FROM entity_revisions WHERE entity_key='intent:%d'", to)
	before := scalar(intentRevision)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{mutation}))
	require.Equal(t, "0", scalar("SELECT count(*) FROM packages WHERE id=?", from))
	// The archive is history: it keeps its original identities.
	require.Equal(t, fmt.Sprint(from), scalar("SELECT package_id FROM seo_archive.seo_urls WHERE url='https://example.org/old'"))
	require.Equal(t, fmt.Sprint(from), scalar("SELECT package_id FROM seo_archive.seo_listing_candidates WHERE token='seo-new'"))
	// Duplicate captures collapse to the newer snapshot; others follow the package.
	require.Equal(t, "902", scalar("SELECT string_agg(id::text,',' ORDER BY id) FROM evidence_snapshots WHERE source_url='https://example.org/docs'"))
	require.Equal(t, next, scalar("SELECT entity_key FROM evidence_snapshots WHERE id=902"))
	require.Equal(t, next, scalar("SELECT entity_key FROM evidence_snapshots WHERE id=903"))
	// The canonical app keeps its own switch and title; the merged app adds the rest.
	require.Equal(t, "en-US:admin:1,ja-JP:system", scalar("SELECT string_agg(locale||':'||created_by,',' ORDER BY locale) FROM package_intent_pages WHERE package_id=?", to))
	require.Equal(t, "de-DE:Deutscher Titel,en-US:Canonical title", scalar("SELECT string_agg(locale||':'||title,',' ORDER BY locale) FROM package_seo_overrides WHERE package_id=?", to))
	require.Equal(t, "0", scalar("SELECT count(*) FROM package_intent_pages WHERE package_id=?", from))
	require.Equal(t, "0", scalar("SELECT count(*) FROM package_seo_overrides WHERE package_id=?", from))
	require.NotEqual(t, before, scalar(intentRevision), "a moved switch changes the canonical app's intent revision")
	audit := scalar("SELECT before::text FROM audit_logs WHERE action='link.adopt.rename'")
	for _, value := range []string{"evidence_snapshots", "capture-one", "capture-two", "package_intent_pages", "Merged title"} {
		require.Contains(t, audit, value)
	}
}
