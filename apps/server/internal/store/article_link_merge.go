package store

import (
	"fmt"

	"gorm.io/gorm"
)

// These are live package references even in historical revisions. Evidence
// receipts and immutable test report payloads retain their original identities.
func articlePackageReferencesSQL(from, to int64) string {
	return fmt.Sprintf(`EXISTS(SELECT 1 FROM jsonb_array_elements(claims) c WHERE c->>'packageId' IN ('%d','%d'))
 OR seo_target->>'entityKey' IN ('pkg:%d','pkg:%d')`, from, to, from, to)
}

func mergeArticleRelationships(db *gorm.DB, from, to int64) error {
	// The unique key is (article_id,sha256), so remapping package_id introduces
	// no new collision. The immutable UPDATE trigger verifies the report is intact.
	if err := db.Exec("UPDATE article_test_artifacts SET package_id=? WHERE package_id=?", to, from).Error; err != nil {
		return err
	}
	return db.Exec(`UPDATE article_revisions SET
 claims=(SELECT COALESCE(jsonb_agg(CASE WHEN c->>'packageId'=? THEN jsonb_set(c,'{packageId}',to_jsonb(?::bigint)) ELSE c END ORDER BY position),'[]'::jsonb)
         FROM jsonb_array_elements(claims) WITH ORDINALITY items(c,position)),
 seo_target=CASE WHEN seo_target->>'entityKey'=? THEN jsonb_set(seo_target,'{entityKey}',to_jsonb(?::text)) ELSE seo_target END
 WHERE `+articlePackageReferencesSQL(from, from), fmt.Sprint(from), to, fmt.Sprintf("pkg:%d", from), fmt.Sprintf("pkg:%d", to)).Error
}

// H2 snapshots can deduplicate during a merge, but article evidence receipts
// must keep their IDs. Refuse the merge instead of orphaning a published claim.
func protectArticleEvidence(db *gorm.DB, evidenceID int64) error {
	var referenced bool
	if err := db.Raw(`SELECT EXISTS(SELECT 1 FROM article_revisions r
 WHERE EXISTS(SELECT 1 FROM jsonb_array_elements(r.claims || r.claim_evidence) c WHERE c->>'evidenceId'=?))`, fmt.Sprint(evidenceID)).Scan(&referenced).Error; err != nil {
		return err
	}
	if referenced {
		return fmt.Errorf("article evidence snapshot %d cannot be deduplicated during link merge", evidenceID)
	}
	return nil
}
