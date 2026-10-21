package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Register package FKs in linkMergeTables and logical references here. New SEO
// tables must add both their archive and migration policy before they are used.
var seoLinkTables = []string{"seo_targets", "seo_tasks", "seo_entity_leases", "seo_actions", "seo_evidence_snapshots", "seo_gate_events", "seo_daily_entities", "article_revisions"}

// Restrict replacements to structured identities, never arbitrary URLs/prose or
// bare decimal substrings. Translation keys also contain collection_item:C:P.
var mergeEntityPattern = regexp.MustCompile(`(?:pkg|package):[0-9]+|collection_item:[0-9]+:[0-9]+`)

func mergedEntity(s string, from, to int64) string {
	old, next := strconv.FormatInt(from, 10), strconv.FormatInt(to, 10)
	indices := mergeEntityPattern.FindAllStringIndex(s, -1)
	for i := len(indices) - 1; i >= 0; i-- {
		a, b := indices[i][0], indices[i][1]
		if a > 0 && ((s[a-1] >= 'a' && s[a-1] <= 'z') || (s[a-1] >= 'A' && s[a-1] <= 'Z') || (s[a-1] >= '0' && s[a-1] <= '9') || s[a-1] == '_') {
			continue
		}
		if b < len(s) && ((s[b] >= 'a' && s[b] <= 'z') || (s[b] >= 'A' && s[b] <= 'Z') || s[b] == '_') {
			continue
		}
		value := s[a:b]
		pos := strings.LastIndexByte(value, ':')
		if value[pos+1:] == old {
			s = s[:a] + value[:pos+1] + next + s[b:]
		}
	}
	return s
}

func mergedPackageJSON(raw json.RawMessage, from, to int64) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return raw, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, false, err
	}
	changed := false
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, child := range x {
				visit(child)
			}
		case map[string]any:
			for key, child := range x {
				isID := key == "packageId" || key == "package_id" || key == "baselinePackageId" || key == "targetPackageId" || key == "alternativeId" || (key == "id" && (x["entity"] == "package" || x["entity"] == "pkg")) || (key == "secondaryId" && x["entity"] == "collection_item")
				switch {
				case isID && fmt.Sprint(child) == strconv.FormatInt(from, 10):
					if _, ok := child.(string); ok {
						x[key] = strconv.FormatInt(to, 10)
					} else {
						x[key] = json.Number(strconv.FormatInt(to, 10))
					}
					changed = true
				case key == "entityKey" || key == "entity_key":
					if s, ok := child.(string); ok {
						next := mergedEntity(s, from, to)
						x[key] = next
						changed = changed || s != next
					}
				default:
					visit(child)
				}
			}
		}
	}
	visit(value)
	if !changed {
		return raw, false, nil
	}
	out, err := json.Marshal(value)
	return out, true, err
}

// IDs here come from locked package rows; only fixed internal column names are
// accepted by callers. Typed JSON selectors avoid matching numbers in prose.
func seoIdentitySQL(column string, from, to int64) string {
	return fmt.Sprintf("%s ~ '(^|[^[:alnum:]_])(pkg:|package:|collection_item:[0-9]+:)(%d|%d)([^[:alnum:]_]|$)'", column, from, to)
}
func seoTaskReferencesSQL(from, to int64) string {
	ids := fmt.Sprintf("('%d','%d')", from, to)
	keys := fmt.Sprintf("('pkg:%d','pkg:%d','package:%d','package:%d')", from, to, from, to)
	return seoIdentitySQL("entity_key", from, to) + " OR " + seoIdentitySQL("dedupe_key", from, to) +
		` OR entity_key IN (SELECT 'release:'||id FROM releases WHERE package_id IN ` + ids + ` UNION ALL SELECT 'screenshot:'||id FROM package_screenshots WHERE package_id IN ` + ids + `) OR EXISTS(SELECT 1 FROM jsonb_path_query(evidence,'$.**') refs(v) WHERE
 v->>'packageId' IN ` + ids + ` OR v->>'package_id' IN ` + ids + ` OR v->>'baselinePackageId' IN ` + ids + ` OR v->>'targetPackageId' IN ` + ids + ` OR v->>'alternativeId' IN ` + ids + `
 OR (v->>'entity' IN ('package','pkg') AND v->>'id' IN ` + ids + `)
 OR (v->>'entity'='collection_item' AND v->>'secondaryId' IN ` + ids + `)
 OR v->>'entityKey' IN ` + keys + ` OR v->>'entity_key' IN ` + keys + `)`
}

// Serialize logical-reference inserts as well as FK writes until archive and
// migration commit together. Catalog merges are infrequent, bounded operations.
func archiveSEOLink(db *gorm.DB, archive map[string]json.RawMessage, from, to int64) error {
	if err := db.Exec("LOCK TABLE seo_listing_baselines,seo_listing_candidates,seo_urls,seo_keyword_links,article_test_artifacts," + strings.Join(seoLinkTables, ",") + " IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
		return err
	}
	tasks := "SELECT id FROM seo_tasks WHERE " + seoTaskReferencesSQL(from, to)
	evidence := "SELECT id FROM seo_evidence_snapshots WHERE " + seoIdentitySQL("entity_key", from, to)
	objects := fmt.Sprintf("(object IN ('package','pkg','link_app') AND object_id IN ('%d','%d')) OR (object='collection_item' AND split_part(object_id,':',2) IN ('%d','%d'))", from, to, from, to)
	where := map[string]string{
		"article_revisions":      articlePackageReferencesSQL(from, to),
		"seo_daily_entities":     seoIdentitySQL("entity_key", from, to),
		"seo_targets":            seoIdentitySQL("entity_key", from, to),
		"seo_tasks":              seoTaskReferencesSQL(from, to),
		"seo_entity_leases":      seoIdentitySQL("entity_key", from, to) + " OR task_id IN (" + tasks + ")",
		"seo_evidence_snapshots": seoIdentitySQL("entity_key", from, to),
		"seo_actions":            objects + " OR task_id IN (" + tasks + ") OR evidence_ids && ARRAY(" + evidence + ")",
		"seo_gate_events":        "task_id IN (" + tasks + ") OR evidence_ids && ARRAY(" + evidence + ")",
	}
	for _, table := range seoLinkTables {
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
	for index, q := range []string{
		"DELETE FROM " + table + " s USING " + table + " d WHERE s.package_id=? AND d.package_id=? AND (" + match + ") AND NOT (" + clock + ")",
		"DELETE FROM " + table + " d USING " + table + " s WHERE s.package_id=? AND d.package_id=? AND (" + match + ")",
		"UPDATE " + table + " SET package_id=? WHERE package_id=?",
	} {
		if table == "package_fact_records" && index < 2 {
			loser, winner, condition := "s", "d", " AND NOT ("+clock+")"
			if index == 1 {
				loser, winner, condition = "d", "s", ""
			}
			query := "UPDATE seo_listing_baselines b SET fact_id=m.keep,suspicious=true FROM (SELECT DISTINCT ON (" + loser + ".id) " + loser + ".id AS old," + winner + ".id AS keep FROM " + table + " s JOIN " + table + " d ON (" + match + ") WHERE s.package_id=? AND d.package_id=?" + condition + " ORDER BY " + loser + ".id," + winner + ".updated_at DESC," + winner + ".id DESC) m WHERE b.fact_id=m.old"
			if err := db.Exec(query, from, to).Error; err != nil {
				return err
			}
		}
		args := []any{from, to}
		if strings.HasPrefix(q, "UPDATE") {
			args = []any{to, from}
			if table == "seo_listing_candidates" {
				q = "UPDATE seo_listing_candidates SET package_id=?,revision=revision+1 WHERE package_id=?"
			}
		}
		if err := db.Exec(q, args...).Error; err != nil {
			return err
		}
	}
	return nil
}

func mergeSEORelationships(ctx context.Context, tx *gorm.DB, from, to int64) error {
	db := tx.WithContext(ctx)
	exec := func(q string, args ...any) error { return db.Exec(q, args...).Error }
	if err := exec("UPDATE seo_urls SET package_id=? WHERE package_id=?", to, from); err != nil {
		return err
	}
	// No business timestamp exists on keyword links; xmin measures transaction
	// write ordering, not wall-clock time. Same transaction chooses canonical.
	if err := mergeNewerPackageRows(db, "seo_keyword_links", "s.keyword_id=d.keyword_id AND s.intent=d.intent", "age(s.xmin)<age(d.xmin)", from, to); err != nil {
		return err
	}
	old, next := fmt.Sprintf("pkg:%d", from), fmt.Sprintf("pkg:%d", to)
	if err := exec(`DELETE FROM seo_targets s USING seo_targets d WHERE s.entity_key=? AND d.entity_key=? AND s.intent=d.intent AND s.locale=d.locale AND (s.created_at<d.created_at OR (s.created_at=d.created_at AND age(s.xmin)>=age(d.xmin)))`, old, next); err != nil {
		return err
	}
	if err := exec(`DELETE FROM seo_targets d USING seo_targets s WHERE s.entity_key=? AND d.entity_key=? AND s.intent=d.intent AND s.locale=d.locale`, old, next); err != nil {
		return err
	}
	if err := exec("UPDATE seo_targets SET entity_key=? WHERE entity_key=?", next, old); err != nil {
		return err
	}
	// A collision has identical fetched_at by definition; highest ID is the later
	// capture. Keep immutable capture/request keys and remap all live references.
	for _, evidenceEntity := range []string{old, fmt.Sprintf("package:%d", from), fmt.Sprintf("package:%d", to)} {
		var collisions []struct{ Source, Target int64 }
		if err := db.Raw(`SELECT s.id AS source,d.id AS target FROM seo_evidence_snapshots s JOIN seo_evidence_snapshots d ON d.entity_key=? AND s.source_url=d.source_url AND s.body_hash=d.body_hash AND s.excerpt=d.excerpt AND s.fetched_at=d.fetched_at WHERE s.entity_key=?`, next, evidenceEntity).Scan(&collisions).Error; err != nil {
			return err
		}
		for _, c := range collisions {
			keep, drop := max(c.Source, c.Target), min(c.Source, c.Target)
			if err := protectArticleEvidence(db, drop); err != nil {
				return err
			}
			for _, table := range []string{"seo_actions", "seo_gate_events"} {
				if err := exec("UPDATE "+table+" SET evidence_ids=ARRAY(SELECT DISTINCT unnest(array_replace(evidence_ids,?::bigint,?::bigint)) ORDER BY 1) WHERE ?::bigint=ANY(evidence_ids)", drop, keep, drop); err != nil {
					return err
				}
			}
			if err := exec("UPDATE seo_listing_baselines SET evidence_id=?,suspicious=true WHERE evidence_id=?", keep, drop); err != nil {
				return err
			}
			if err := exec("UPDATE seo_listing_candidates SET payload=jsonb_set(payload,'{evidenceId}',to_jsonb(?::bigint)) WHERE payload->>'evidenceId'=?", keep, fmt.Sprint(drop)); err != nil {
				return err
			}
			if err := exec("DELETE FROM seo_evidence_snapshots WHERE id=?", drop); err != nil {
				return err
			}
		}
		if err := exec("UPDATE seo_evidence_snapshots SET entity_key=? WHERE entity_key=?", next, evidenceEntity); err != nil {
			return err
		}
	}
	type taskRow struct {
		ID                                       int64
		Kind, EntityKey, DedupeKey, EvidenceHash string
		Evidence                                 json.RawMessage
		UpdatedAt                                time.Time
	}
	var tasks []taskRow
	if err := db.Raw("SELECT * FROM seo_tasks WHERE " + seoTaskReferencesSQL(from, to) + " ORDER BY updated_at DESC,id DESC").Scan(&tasks).Error; err != nil {
		return err
	}
	// Winner IDs are chosen before any timestamps or identities are modified.
	winners := map[string]int64{}
	drops := map[int64]int64{}
	for _, t := range tasks {
		key := mergedEntity(t.DedupeKey, from, to)
		var other taskRow
		if err := db.Raw("SELECT id,updated_at FROM seo_tasks WHERE dedupe_key=? AND id<>?", key, t.ID).Scan(&other).Error; err != nil {
			return err
		}
		if other.ID != 0 && (other.UpdatedAt.After(t.UpdatedAt) || (other.UpdatedAt.Equal(t.UpdatedAt) && other.ID > t.ID)) {
			drops[t.ID] = other.ID
			winners[key] = other.ID
			continue
		}
		if id, ok := winners[key]; ok && id != t.ID {
			drops[t.ID] = id
			continue
		}
		winners[key] = t.ID
		if other.ID != 0 {
			drops[other.ID] = t.ID
		}
	}
	for drop, keep := range drops {
		for drops[keep] != 0 {
			keep = drops[keep]
		}
		for _, table := range []string{"seo_actions", "seo_gate_events", "seo_entity_leases"} {
			if err := exec("UPDATE "+table+" SET task_id=? WHERE task_id=?", keep, drop); err != nil {
				return err
			}
		}
		if err := exec("DELETE FROM seo_tasks WHERE id=?", drop); err != nil {
			return err
		}
	}
	for _, t := range tasks {
		if drops[t.ID] != 0 {
			continue
		}
		evidence, changed, err := mergedPackageJSON(t.Evidence, from, to)
		if err != nil {
			return err
		}
		entity, key := mergedEntity(t.EntityKey, from, to), mergedEntity(t.DedupeKey, from, to)
		relevant := entity != t.EntityKey || key != t.DedupeKey || changed || mergedEntity(t.EntityKey, to, from) != t.EntityKey
		if !relevant {
			continue
		}
		if err := exec("UPDATE seo_entity_leases SET task_id=NULL,owner=NULL,lease_until=NULL,fencing=fencing+1 WHERE task_id=?", t.ID); err != nil {
			return err
		}
		hash := t.EvidenceHash
		// Translation evidence_hash is the source-content hash, not the JSON hash.
		if changed && t.Kind != "translate" {
			sum := sha256.Sum256(evidence)
			hash = hex.EncodeToString(sum[:])
		}
		// Evidence waiting for the old lease described the merged-away identity.
		if err := exec(`UPDATE seo_tasks SET entity_key=?,dedupe_key=?,evidence=?::jsonb,evidence_hash=?,pending_evidence=NULL,pending_evidence_hash=NULL,lease_owner=NULL,lease_until=NULL,fencing=fencing+1,state=CASE WHEN state='leased' THEN 'open' ELSE state END WHERE id=?`, entity, key, string(evidence), hash, t.ID); err != nil {
			return err
		}
	}
	var leases []struct{ EntityKey string }
	if err := db.Raw("SELECT entity_key FROM seo_entity_leases WHERE " + seoIdentitySQL("entity_key", from, to)).Scan(&leases).Error; err != nil {
		return err
	}
	for _, l := range leases {
		if mergedEntity(l.EntityKey, from, to) != l.EntityKey {
			if err := exec("DELETE FROM seo_entity_leases WHERE entity_key=?", l.EntityKey); err != nil {
				return err
			}
		} else if mergedEntity(l.EntityKey, to, from) != l.EntityKey {
			if err := exec("UPDATE seo_entity_leases SET task_id=NULL,owner=NULL,lease_until=NULL,fencing=fencing+1 WHERE entity_key=?", l.EntityKey); err != nil {
				return err
			}
		}
	}
	if err := exec("UPDATE seo_actions SET object_id=? WHERE object IN ('package','pkg','link_app') AND object_id=?", fmt.Sprint(to), fmt.Sprint(from)); err != nil {
		return err
	}
	// Collection item object IDs encode collection:package, matching content.Ref.
	if err := exec("UPDATE seo_actions SET object_id=split_part(object_id,':',1)||':'||? WHERE object='collection_item' AND split_part(object_id,':',2)=?", fmt.Sprint(to), fmt.Sprint(from)); err != nil {
		return err
	}
	return mergeListingAndDailyEntities(db, from, to)
}

func mergeListingAndDailyEntities(db *gorm.DB, from, to int64) error {
	var identityConflict bool
	if err := db.Raw(`SELECT EXISTS(SELECT 1 FROM seo_listing_baselines s JOIN seo_listing_baselines d
 ON s.package_id=? AND d.package_id=? WHERE (s.domain,s.developer,s.team_id) IS DISTINCT FROM (d.domain,d.developer,d.team_id))`, from, to).Scan(&identityConflict).Error; err != nil {
		return err
	}
	if identityConflict {
		return fmt.Errorf("link merge has conflicting listing baseline identities")
	}
	if err := mergeNewerPackageRows(db, "seo_listing_baselines", "true", "s.verified_at>d.verified_at", from, to); err != nil {
		return err
	}
	// Baselines must be rechecked after changing the package's identity/version.
	if err := db.Exec("UPDATE seo_listing_baselines SET suspicious=true WHERE package_id=?", to).Error; err != nil {
		return err
	}
	if err := mergeNewerPackageRows(db, "seo_listing_candidates", "true", "s.updated_at>d.updated_at", from, to); err != nil {
		return err
	}
	// Payload is the original submitted identity. Runtime ownership is represented
	// by the columns; preserve payload.baselinePackageId for provenance.
	if err := db.Exec("UPDATE seo_listing_candidates SET baseline_package_id=?,revision=revision+1 WHERE baseline_package_id=?", to, from).Error; err != nil {
		return err
	}
	var daily []struct {
		Site, Day, Action, EntityKey string
		CreatedAt                    time.Time
	}
	if err := db.Raw("SELECT site,day::text,action,entity_key,created_at FROM seo_daily_entities WHERE " + seoIdentitySQL("entity_key", from, to)).Scan(&daily).Error; err != nil {
		return err
	}
	for _, r := range daily {
		next := mergedEntity(r.EntityKey, from, to)
		if next == r.EntityKey {
			continue
		}
		if err := db.Exec(`INSERT INTO seo_daily_entities(site,day,action,entity_key,first_request_id,created_at)
 SELECT site,day,action,?,first_request_id,created_at FROM seo_daily_entities WHERE site=? AND day=?::date AND action=? AND entity_key=?
 ON CONFLICT(site,day,action,entity_key) DO UPDATE SET first_request_id=EXCLUDED.first_request_id,created_at=EXCLUDED.created_at WHERE EXCLUDED.created_at>seo_daily_entities.created_at`, next, r.Site, r.Day, r.Action, r.EntityKey).Error; err != nil {
			return err
		}
		if err := db.Exec("DELETE FROM seo_daily_entities WHERE site=? AND day=?::date AND action=? AND entity_key=?", r.Site, r.Day, r.Action, r.EntityKey).Error; err != nil {
			return err
		}
	}
	return nil
}
