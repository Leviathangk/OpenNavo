//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type articleMergeFixture struct {
	t        *testing.T
	st       *store.Store
	ctx      context.Context
	from, to int64
	mutation store.CatalogMutation
}

func newArticleMergeFixture(t *testing.T) articleMergeFixture {
	t.Helper()
	f := articleMergeFixture{t: t, st: testutil.NewFactsStore(t), ctx: context.Background()}
	require.NoError(t, f.st.ApplyCatalogBatch(f.ctx, []store.CatalogMutation{{Package: remediationCask(t, "article-old")}}))
	previous, err := f.st.CatalogRow(f.ctx, "cask", "article-old")
	require.NoError(t, err)
	f.to, f.from = previous.ID, remediationLink(t, f.st, "article-new")
	f.mutation = store.CatalogMutation{Package: remediationCask(t, "article-new"), Previous: previous, RenamedFrom: "article-old"}
	f.exec("INSERT INTO admin_users(id,user_name,password_hash) VALUES(901,'merge-fixture','fixture-not-a-password-hash')")
	return f
}

func (f articleMergeFixture) exec(q string, args ...any) {
	f.t.Helper()
	require.NoError(f.t, f.st.DB.Exec(q, args...).Error)
}

func (f articleMergeFixture) scalar(q string, args ...any) string {
	f.t.Helper()
	var s string
	require.NoError(f.t, f.st.DB.Raw(q, args...).Scan(&s).Error)
	return s
}

func (f articleMergeFixture) merge() error {
	return f.st.ApplyCatalogBatch(f.ctx, []store.CatalogMutation{f.mutation})
}

func (f articleMergeFixture) article() {
	f.t.Helper()
	require.NoError(f.t, f.st.WithTx(f.ctx, func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO articles(id,slug,working_revision_id) VALUES(901,'merge-fixture',902)").Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO article_revisions(id,article_id,number,source_locale,type,content_hash)
 VALUES(901,901,1,'en-US','review','original-hash'),(902,901,2,'en-US','review','working-hash'),
 (903,901,3,'en-US','review','target-only-hash'),(904,901,4,'en-US','review','unrelated-hash')`).Error
	}))
}

func (f articleMergeFixture) evidence(id, packageID int64) {
	f.t.Helper()
	f.exec(`INSERT INTO evidence_snapshots(id,entity_key,source_url,final_url,body_text,excerpt,body_hash,fetched_at,valid_until,authority,fetch_status,created_by,capture_key,request_hash)
 VALUES(?,?,'https://example.org/docs','https://example.org/docs','fixture','fixture',repeat('a',64),'2026-01-01','2027-01-01','official',200,'fixture',?,repeat('b',64))`, id, fmt.Sprintf("pkg:%d", packageID), fmt.Sprintf("merge-capture-%d", id))
}

func TestArticleLinkMergePreservesReportsReferencesAuditAndRollback(t *testing.T) {
	f := newArticleMergeFixture(t)
	f.article()
	f.evidence(901, f.from)
	report := articles.TestArtifactInput{PackageID: f.from, AppVersion: "1.0", MacOS: "14.0", Architecture: "arm64", Region: "US", PerformedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Runner: "fixture", Command: "fixture --check", Environment: map[string]string{"fixture": "true"}, Output: "Fixture result", Conclusions: []string{"Fixture result"}, Attested: true}
	artifact, err := f.st.InsertArticleTestArtifact(f.ctx, 901, 901, report)
	require.NoError(t, err)
	report.PackageID = f.to
	canonicalArtifact, err := f.st.InsertArticleTestArtifact(f.ctx, 901, 901, report)
	require.NoError(t, err)
	// Same article, different report hashes: both records must survive remapping.
	require.NotEqual(t, artifact.SHA256, canonicalArtifact.SHA256)
	claims := fmt.Sprintf(`[{"packageId":%d,"evidenceId":901,"testArtifactId":%d,"text":"literal pkg:%d remains","nested":{"packageId":%d}},
 {"packageId":%d,"evidenceId":901},{"packageId":null},{"packageId":%d}]`, f.from, artifact.ID, f.from, f.from, f.to, f.from*10)
	proof := fmt.Sprintf(`[{"evidenceId":901,"testArtifactId":%d,"testArtifactHash":%q}]`, artifact.ID, artifact.SHA256)
	f.exec("UPDATE article_revisions SET claims=?::jsonb,claim_evidence=?::jsonb,seo_target=jsonb_build_object('entityKey',?::text,'intent','price') WHERE id IN (901,902)", claims, proof, fmt.Sprintf("pkg:%d", f.from))
	f.exec("UPDATE article_revisions SET seo_target=jsonb_build_object('entityKey',?::text,'intent','price') WHERE id=903", fmt.Sprintf("pkg:%d", f.from))
	f.exec("UPDATE article_revisions SET claims=jsonb_build_array(jsonb_build_object('nested',jsonb_build_object('packageId',?::bigint))),seo_target=jsonb_build_object('entityKey',?::text) WHERE id=904", f.from, fmt.Sprintf("pkg:%d", f.from*10))
	f.exec("INSERT INTO article_revision_packages VALUES(901,?,0),(901,?,1)", f.to, f.from)
	reportsBefore := f.scalar("SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM article_test_artifacts a")
	reportIdentity := f.scalar("SELECT jsonb_agg(to_jsonb(a)-'package_id' ORDER BY id)::text FROM article_test_artifacts a")
	revisionsBefore := f.scalar("SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM article_revisions r")
	trackedRevisionsBefore := f.scalar("SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM article_revisions r WHERE id<904")
	unrelatedBefore := f.scalar("SELECT to_jsonb(r)::text FROM article_revisions r WHERE id=904")
	evidenceBefore := f.scalar("SELECT (to_jsonb(e)-'entity_key')::text FROM evidence_snapshots e WHERE id=901")

	f.exec(`CREATE FUNCTION reject_article_merge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.token='article-new' AND NEW.kind='cask' THEN RAISE EXCEPTION 'article rollback fixture'; END IF; RETURN NEW; END $$`)
	f.exec("CREATE TRIGGER reject_article_merge BEFORE UPDATE ON packages FOR EACH ROW EXECUTE FUNCTION reject_article_merge()")
	require.ErrorContains(t, f.merge(), "article rollback fixture")
	require.JSONEq(t, reportsBefore, f.scalar("SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM article_test_artifacts a"))
	require.JSONEq(t, revisionsBefore, f.scalar("SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM article_revisions r"))
	require.Equal(t, "0", f.scalar("SELECT count(*) FROM audit_logs WHERE action='link.adopt.rename'"))
	require.Equal(t, fmt.Sprintf("pkg:%d", f.from), f.scalar("SELECT entity_key FROM evidence_snapshots WHERE id=901"))
	f.exec("DROP TRIGGER reject_article_merge ON packages")
	require.NoError(t, f.merge())

	require.Equal(t, "0", f.scalar("SELECT count(*) FROM packages WHERE id=?", f.from))
	require.Equal(t, "2", f.scalar("SELECT count(*) FROM article_test_artifacts WHERE package_id=?", f.to))
	require.JSONEq(t, reportIdentity, f.scalar("SELECT jsonb_agg(to_jsonb(a)-'package_id' ORDER BY id)::text FROM article_test_artifacts a"))
	readback, err := f.st.ArticleTestArtifact(f.ctx, artifact.ID)
	require.NoError(t, err)
	var original articles.TestArtifactInput
	require.NoError(t, json.Unmarshal(readback.Payload, &original))
	require.Equal(t, f.from, original.PackageID)
	require.Equal(t, f.to, readback.PackageID)
	require.Equal(t, original.Hash(), readback.SHA256)
	// The database still rejects edits to the immutable report after remapping.
	require.ErrorContains(t, f.st.DB.Exec("UPDATE article_test_artifacts SET payload=jsonb_set(payload,'{packageId}',to_jsonb(?::bigint)) WHERE id=?", f.to, artifact.ID).Error, "test artifacts are immutable")
	require.ErrorContains(t, f.st.DB.Exec("UPDATE article_test_artifacts SET sha256=? WHERE id=?", strings.Repeat("f", 64), artifact.ID).Error, "test artifacts are immutable")
	for _, id := range []int64{901, 902} {
		require.Equal(t, fmt.Sprint(f.to), f.scalar("SELECT claims->0->>'packageId' FROM article_revisions WHERE id=?", id))
		require.Equal(t, fmt.Sprint(f.to), f.scalar("SELECT claims->1->>'packageId' FROM article_revisions WHERE id=?", id))
		require.Equal(t, fmt.Sprint(f.from*10), f.scalar("SELECT claims->3->>'packageId' FROM article_revisions WHERE id=?", id))
		require.Equal(t, fmt.Sprint(f.from), f.scalar("SELECT claims->0->'nested'->>'packageId' FROM article_revisions WHERE id=?", id))
		require.Equal(t, fmt.Sprintf("literal pkg:%d remains", f.from), f.scalar("SELECT claims->0->>'text' FROM article_revisions WHERE id=?", id))
		require.Equal(t, "901", f.scalar("SELECT claims->0->>'evidenceId' FROM article_revisions WHERE id=?", id))
		require.JSONEq(t, proof, f.scalar("SELECT claim_evidence::text FROM article_revisions WHERE id=?", id))
		require.Equal(t, fmt.Sprintf("pkg:%d", f.to), f.scalar("SELECT seo_target->>'entityKey' FROM article_revisions WHERE id=?", id))
	}
	require.Equal(t, "[]", f.scalar("SELECT claims::text FROM article_revisions WHERE id=903"))
	require.Equal(t, fmt.Sprintf("pkg:%d", f.to), f.scalar("SELECT seo_target->>'entityKey' FROM article_revisions WHERE id=903"))
	require.JSONEq(t, unrelatedBefore, f.scalar("SELECT to_jsonb(r)::text FROM article_revisions r WHERE id=904"))
	require.Equal(t, "original-hash", f.scalar("SELECT content_hash FROM article_revisions WHERE id=901"))
	require.Equal(t, "1", f.scalar("SELECT count(*) FROM article_revision_packages WHERE revision_id=901 AND package_id=?", f.to))
	require.JSONEq(t, evidenceBefore, f.scalar("SELECT (to_jsonb(e)-'entity_key')::text FROM evidence_snapshots e WHERE id=901"))
	require.Equal(t, fmt.Sprintf("pkg:%d", f.to), f.scalar("SELECT entity_key FROM evidence_snapshots WHERE id=901"))
	require.JSONEq(t, reportsBefore, f.scalar("SELECT before->'article_test_artifacts' FROM audit_logs WHERE action='link.adopt.rename'"))
	require.JSONEq(t, trackedRevisionsBefore, f.scalar("SELECT before->'article_revisions' FROM audit_logs WHERE action='link.adopt.rename'"))
	require.Equal(t, "3", f.scalar("SELECT jsonb_array_length(before->'article_revisions') FROM audit_logs WHERE action='link.adopt.rename'"))
	require.Equal(t, fmt.Sprint(f.from), f.scalar("SELECT r->'claims'->0->>'packageId' FROM audit_logs a CROSS JOIN LATERAL jsonb_array_elements(a.before->'article_revisions') r WHERE a.action='link.adopt.rename' AND r->>'id'='901'"))
}

func TestArticleLinkMergeRejectsDeletingReferencedEvidence(t *testing.T) {
	f := newArticleMergeFixture(t)
	f.article()
	f.evidence(901, f.to)
	f.evidence(902, f.from)
	// Both draft claims and published receipts independently pin snapshot IDs.
	for _, field := range []string{"claims", "claim_evidence"} {
		t.Run(field, func(t *testing.T) {
			f.exec("UPDATE article_revisions SET " + field + "='[{\"evidenceId\":901}]' WHERE id=901")
			require.ErrorContains(t, f.merge(), "article evidence snapshot 901 cannot be deduplicated")
			require.Equal(t, "2", f.scalar("SELECT count(*) FROM evidence_snapshots"))
			require.Equal(t, "2", f.scalar("SELECT count(*) FROM packages WHERE id IN (?,?)", f.from, f.to))
			require.Equal(t, "0", f.scalar("SELECT count(*) FROM audit_logs WHERE action='link.adopt.rename'"))
			require.Equal(t, "901", f.scalar("SELECT "+field+"->0->>'evidenceId' FROM article_revisions WHERE id=901"))
			f.exec("UPDATE article_revisions SET " + field + "='[]' WHERE id=901")
		})
	}
	// Referencing the retained newer snapshot is safe; its ID remains untouched.
	f.exec("UPDATE article_revisions SET claim_evidence='[{\"evidenceId\":902}]' WHERE id=901")
	require.NoError(t, f.merge())
	require.Equal(t, "902", f.scalar("SELECT id FROM evidence_snapshots"))
	require.Equal(t, "902", f.scalar("SELECT claim_evidence->0->>'evidenceId' FROM article_revisions WHERE id=901"))
}
