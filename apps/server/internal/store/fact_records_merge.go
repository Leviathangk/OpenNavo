package store

import (
	"context"
	"strconv"

	"gorm.io/gorm"
)

var factRelationshipTables = []string{"package_fact_records", "app_store_price_snapshots", "package_cve_checks", "package_version_requirements", "background_item_packages"}

// The caller archives both sides. Newest observations win; ties choose the
// survivor. Noncolliding stable IDs and all evidence dates remain unchanged.
func mergeFactRelationships(ctx context.Context, tx *gorm.DB, from, to int64) error {
	db := tx.WithContext(ctx)
	if err := mergeNewerPackageRows(db, "package_fact_records", `d.kind=s.kind AND
 (d.key=s.key OR (s.kind='price' AND d.value->>'region'=s.value->>'region' AND d.value->>'edition'=s.value->>'edition' AND d.value->>'period'=s.value->>'period' AND d.value->>'channel'=s.value->>'channel'))`, "s.updated_at>d.updated_at", from, to); err != nil {
		return err
	}
	// Re-key targets but require re-verification after the app identity changes.
	if err := db.Exec(`UPDATE package_fact_records SET value=jsonb_set(value,'{targetPackageId}',to_jsonb(?::bigint)),evidence=jsonb_set(evidence,'{status}','"needs_review"'::jsonb) WHERE kind='migration' AND value->>'targetPackageId'=?`, to, strconv.FormatInt(from, 10)).Error; err != nil {
		return err
	}
	for _, policy := range []struct{ table, match string }{
		{"app_store_price_snapshots", "s.observed_on=d.observed_on AND s.region=d.region AND s.bundle_id=d.bundle_id"},
		{"package_cve_checks", "s.product_cpe=d.product_cpe"},
		{"package_version_requirements", "s.app_version=d.app_version"},
		{"background_item_packages", "s.label=d.label AND s.kind=d.kind AND s.phase=d.phase"},
	} {
		if err := mergeNewerPackageRows(db, policy.table, policy.match, "s.checked_at>d.checked_at", from, to); err != nil {
			return err
		}
	}
	return nil
}
