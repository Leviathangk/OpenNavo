package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Register package FKs in linkMergeTables and logical references here.
var evidenceLinkTables = []string{"evidence_snapshots", "article_revisions"}

// IDs here come from locked package rows; only fixed internal column names are
// accepted by callers. Typed JSON selectors avoid matching numbers in prose.
func packageIdentitySQL(column string, from, to int64) string {
	return fmt.Sprintf("%s ~ '(^|[^[:alnum:]_])(pkg:|package:|collection_item:[0-9]+:)(%d|%d)([^[:alnum:]_]|$)'", column, from, to)
}

// Serialize logical-reference inserts as well as FK writes until archive and
// migration commit together. Catalog merges are infrequent, bounded operations.
func archiveEvidenceLink(db *gorm.DB, archive map[string]json.RawMessage, from, to int64) error {
	if err := db.Exec("LOCK TABLE article_test_artifacts,evidence_snapshots,article_revisions IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
		return err
	}
	where := map[string]string{
		"article_revisions":  articlePackageReferencesSQL(from, to),
		"evidence_snapshots": packageIdentitySQL("entity_key", from, to),
	}
	for _, table := range evidenceLinkTables {
		var raw string
		if err := db.Raw("SELECT coalesce(jsonb_agg(to_jsonb(r)),'[]'::jsonb)::text FROM " + table + " r WHERE " + where[table]).Scan(&raw).Error; err != nil {
			return err
		}
		archive[table] = json.RawMessage(raw)
	}
	return nil
}

// A table with only one package dimension can retain the newer row wholesale.
// Caller has archived both sides. Equal timestamps choose the surviving package.
func mergeNewerPackageRows(db *gorm.DB, table, match, clock string, from, to int64) error {
	for _, q := range []string{
		"DELETE FROM " + table + " s USING " + table + " d WHERE s.package_id=? AND d.package_id=? AND (" + match + ") AND NOT (" + clock + ")",
		"DELETE FROM " + table + " d USING " + table + " s WHERE s.package_id=? AND d.package_id=? AND (" + match + ")",
		"UPDATE " + table + " SET package_id=? WHERE package_id=?",
	} {
		args := []any{from, to}
		if strings.HasPrefix(q, "UPDATE") {
			args = []any{to, from}
		}
		if err := db.Exec(q, args...).Error; err != nil {
			return err
		}
	}
	return nil
}

// Evidence snapshots move to the surviving package. A collision has identical
// fetched_at by definition; highest ID is the later capture. Keep immutable
// capture/request keys and never drop a snapshot an article claim cites.
func mergeEvidenceRelationships(ctx context.Context, tx *gorm.DB, from, to int64) error {
	db := tx.WithContext(ctx)
	next := fmt.Sprintf("pkg:%d", to)
	for _, evidenceEntity := range []string{fmt.Sprintf("pkg:%d", from), fmt.Sprintf("package:%d", from), fmt.Sprintf("package:%d", to)} {
		var collisions []struct{ Source, Target int64 }
		if err := db.Raw(`SELECT s.id AS source,d.id AS target FROM evidence_snapshots s JOIN evidence_snapshots d ON d.entity_key=? AND s.source_url=d.source_url AND s.body_hash=d.body_hash AND s.excerpt=d.excerpt AND s.fetched_at=d.fetched_at WHERE s.entity_key=?`, next, evidenceEntity).Scan(&collisions).Error; err != nil {
			return err
		}
		for _, c := range collisions {
			drop := min(c.Source, c.Target)
			if err := protectArticleEvidence(db, drop); err != nil {
				return err
			}
			if err := db.Exec("DELETE FROM evidence_snapshots WHERE id=?", drop).Error; err != nil {
				return err
			}
		}
		if err := db.Exec("UPDATE evidence_snapshots SET entity_key=? WHERE entity_key=?", next, evidenceEntity).Error; err != nil {
			return err
		}
	}
	return nil
}
