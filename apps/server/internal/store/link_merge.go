package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"gorm.io/gorm"
)

// Every live FK into packages has an explicit policy below. Audit snapshots also
// include colliding rows and overflow alternatives; history keeps its original IDs.
var linkMergeTables = append([]string{"package_meta", "package_i18n", "package_categories", "package_screenshots", "package_versions", "releases", "changelog_sources", "analytics_snapshots", "collection_items", "features", "feedback", "package_aliases", "tap_casks", "article_revision_packages", "article_test_artifacts", "package_source_facts", "package_facts", "package_alternatives", "link_apps", "update_feeds", "package_page_state", "package_page_dirty", "seo_urls", "seo_keyword_links", "seo_listing_baselines", "seo_listing_candidates"}, factRelationshipTables...)

func mergeLinkIntoCask(ctx context.Context, tx *gorm.DB, mutation CatalogMutation, now time.Time) error {
	if mutation.Previous == nil || mutation.RenamedFrom == "" {
		return nil
	}
	db := tx.WithContext(ctx)
	var source, target struct {
		ID          int64
		Kind, Token string
	}
	if err := db.Raw("SELECT id,kind,token FROM packages WHERE kind='link' AND token=? FOR UPDATE", mutation.Package.Token).Scan(&source).Error; err != nil {
		return err
	}
	if source.ID == 0 {
		return nil
	}
	if err := db.Raw("SELECT id,kind,token FROM packages WHERE id=? FOR UPDATE", mutation.Previous.ID).Scan(&target).Error; err != nil {
		return err
	}
	if target.Kind != "cask" || target.Token != mutation.RenamedFrom || source.ID == target.ID {
		return fmt.Errorf("link rename identity changed")
	}
	from, to := source.ID, target.ID
	// Fail closed if a future migration adds an unhandled FK, before deleting data.
	var tables []string
	if err := db.Raw("SELECT DISTINCT conrelid::regclass::text FROM pg_constraint WHERE contype='f' AND confrelid='packages'::regclass").Scan(&tables).Error; err != nil {
		return err
	}
	known := map[string]bool{}
	for _, table := range linkMergeTables {
		known[table] = true
	}
	for _, table := range tables {
		if !known[table] {
			return fmt.Errorf("unhandled package relationship: %s", table)
		}
	}
	archive := map[string]json.RawMessage{}
	if err := archiveSEOLink(db, archive, from, to); err != nil {
		return err
	}

	snapshots := map[string]string{"packages": "id IN (?,?)"}
	for _, table := range linkMergeTables {
		snapshots[table] = "package_id IN (?,?)"
	}
	snapshots["seo_listing_candidates"] = "package_id IN (?,?) OR baseline_package_id IN (?,?) OR payload->>'evidenceId' IN (SELECT id::text FROM seo_evidence_snapshots WHERE entity_key IN ('pkg:'||(?::bigint)::text,'pkg:'||(?::bigint)::text))"
	snapshots["seo_listing_baselines"] = "package_id IN (?,?) OR fact_id IN (SELECT id FROM package_fact_records WHERE package_id IN (?,?)) OR evidence_id IN (SELECT id FROM seo_evidence_snapshots WHERE entity_key IN ('pkg:'||(?::bigint)::text,'pkg:'||(?::bigint)::text))"
	snapshots["package_fact_records"] = "package_id IN (?,?) OR (kind='migration' AND value->>'targetPackageId' IN ((?::bigint)::text,(?::bigint)::text))"
	snapshots["package_alternatives"] = "package_id IN (?,?) OR alternative_id IN (?,?)"
	snapshots["release_i18n"] = "release_id IN (SELECT id FROM releases WHERE package_id IN (?,?))"
	snapshots["screenshot_i18n"] = "screenshot_id IN (SELECT id FROM package_screenshots WHERE package_id IN (?,?))"
	snapshots["collection_item_i18n"] = "package_id IN (?,?)"
	snapshots["content_translation_sources"] = "(ref->>'id')::bigint IN (?,?) AND entity='package' OR (ref->>'secondaryId')::bigint IN (?,?) AND entity='collection_item'"
	snapshots["job_outbox"] = "(payload->>'packageId')::bigint IN (?,?) OR (payload->>'sourceId')::bigint IN (SELECT id FROM changelog_sources WHERE package_id IN (?,?))"
	for table, where := range snapshots {
		args := []any{from, to}
		if table == "package_fact_records" || table == "package_alternatives" || table == "content_translation_sources" || table == "job_outbox" {
			args = append(args, from, to)
		}
		if table == "seo_listing_baselines" || table == "seo_listing_candidates" {
			args = append(args, from, to, from, to)
		}
		var raw string
		if err := db.Raw("SELECT coalesce(jsonb_agg(to_jsonb(r)),'[]'::jsonb)::text FROM "+table+" r WHERE "+where, args...).Scan(&raw).Error; err != nil {
			return err
		}
		archive[table] = json.RawMessage(raw)
	}
	for _, table := range []string{"content_translation_sources", "job_outbox"} {
		ref := "ref"
		if table == "job_outbox" {
			ref = "payload->'content'"
		}
		var raw string
		query := "SELECT coalesce(jsonb_agg(to_jsonb(r)),'[]'::jsonb)::text FROM " + table + " r WHERE (" + ref + "->>'entity'='release' AND (" + ref + "->>'id')::bigint IN (SELECT id FROM releases WHERE package_id IN (?,?))) OR (" + ref + "->>'entity'='collection_item' AND (" + ref + "->>'secondaryId')::bigint IN (?,?)) OR (" + ref + "->>'entity'='package' AND (" + ref + "->>'id')::bigint IN (?,?))"
		if err := db.Raw(query, from, to, from, to, from, to).Scan(&raw).Error; err != nil {
			return err
		}
		archive[table+"_content"] = json.RawMessage(raw)
	}
	before, err := json.Marshal(archive)
	if err != nil {
		return err
	}
	if err := db.Exec("INSERT INTO audit_logs(action,entity_type,entity_id,before,after) VALUES('link.adopt.rename','package',?,?::jsonb,jsonb_build_object('retiredId',?::bigint,'canonicalId',?::bigint,'policy','canonical wins equal priority; human before machine; hidden OR; overflow archived; SEO newest row wins (keyword xmin ordering); differing baseline identities rejected; original candidate baseline IDs and artifact payload/hash preserved; article package references remapped, evidence IDs preserved; page state recomputed; leases revoked'))", fmt.Sprint(to), string(before), from, to).Error; err != nil {
		return err
	}
	if err := mergeSEORelationships(ctx, tx, from, to); err != nil {
		return err
	}
	if err := mergeArticleRelationships(db, from, to); err != nil {
		return err
	}
	st := &Store{DB: tx}
	exec := func(query string, args ...any) error { return db.Exec(query, args...).Error }
	// Copy entire rows, including future nullable metadata, with named key overrides.
	copyRows := func(table, where, patch string, args ...any) error {
		return exec("INSERT INTO "+table+" SELECT (jsonb_populate_record(NULL::"+table+",to_jsonb(s)||"+patch+")).* FROM "+table+" s WHERE "+where+" ON CONFLICT DO NOTHING", args...)
	}

	mergeLocaleText := func(table, key string, fields []string, from, to int64, sameCollection bool) error {
		human := "l.status IN ('manual','source') AND c.status NOT IN ('manual','source')"
		assignments := []string{}
		for _, field := range fields {
			empty := "''"
			if field == "sections" {
				empty = "'[]'::jsonb"
			}
			preferred := "coalesce(nullif(l." + field + "," + empty + "),c." + field + ")"
			fallback := "coalesce(nullif(c." + field + "," + empty + "),l." + field + ")"
			assignments = append(assignments, field+"=CASE WHEN "+human+" THEN "+preferred+" ELSE "+fallback+" END")
		}
		assignments = append(assignments, "status=CASE WHEN "+human+" THEN 'manual' ELSE c.status END", "source_hash=NULL", "model=NULL")
		where := "c." + key + "=? AND l." + key + "=? AND c.locale=l.locale"
		if sameCollection {
			where += " AND c.collection_id=l.collection_id"
		}
		return exec("UPDATE "+table+" c SET "+strings.Join(assignments, ",")+" FROM "+table+" l WHERE "+where, to, from)
	}
	if err := exec("INSERT INTO package_meta(package_id) VALUES(?) ON CONFLICT DO NOTHING", to); err != nil {
		return err
	}
	if err := copyRows("package_meta", "s.package_id=?", "jsonb_build_object('package_id',?::bigint)", to, from); err != nil {
		return err
	}
	if err := exec(`UPDATE package_meta c SET hidden=c.hidden OR l.hidden OR p.removed_at IS NOT NULL,
 developer=coalesce(nullif(c.developer,''),l.developer),repo_url=coalesce(nullif(c.repo_url,''),l.repo_url),notes=coalesce(nullif(c.notes,''),l.notes),
 icon_source=CASE WHEN c.icon_asset_id IS NULL THEN l.icon_source ELSE c.icon_source END,icon_asset_id=coalesce(c.icon_asset_id,l.icon_asset_id),accent_color=coalesce(nullif(c.accent_color,''),l.accent_color),
 tags=ARRAY(SELECT DISTINCT unnest(c.tags||l.tags) ORDER BY 1),changelog_excluded=c.changelog_excluded OR l.changelog_excluded,
 editor_choice=c.editor_choice OR l.editor_choice,
 index_override=CASE WHEN c.index_override='noindex' OR l.index_override='noindex' THEN 'noindex' WHEN c.index_override='auto' THEN l.index_override ELSE c.index_override END,
 index_override_reason=CASE WHEN c.index_override='auto' OR (l.index_override='noindex' AND c.index_override<>'noindex') THEN l.index_override_reason ELSE c.index_override_reason END,
 index_override_at=CASE WHEN c.index_override='auto' OR (l.index_override='noindex' AND c.index_override<>'noindex') THEN l.index_override_at ELSE c.index_override_at END
 FROM package_meta l,packages p WHERE c.package_id=? AND l.package_id=? AND p.id=l.package_id`, to, from); err != nil {
		return err
	}
	// A removed source with no metadata must not become visible through adoption.
	if err := exec("UPDATE package_meta SET hidden=true WHERE package_id=? AND EXISTS(SELECT 1 FROM packages WHERE id=? AND removed_at IS NOT NULL)", to, from); err != nil {
		return err
	}
	if err := copyRows("package_facts", "s.package_id=?", "jsonb_build_object('package_id',?::bigint)", to, from); err != nil {
		return err
	}
	if err := exec("UPDATE package_facts c SET price=coalesce(c.price,l.price),macos=coalesce(c.macos,l.macos) FROM package_facts l WHERE c.package_id=? AND l.package_id=?", to, from); err != nil {
		return err
	}
	// Fill absent fields, prefer human text per locale, and discard obsolete
	// hashes before selecting one source document and rescheduling translations.
	if err := exec(`UPDATE package_i18n c SET
 display_name=CASE WHEN (l.source='human' OR l.status='manual') AND NOT (c.source='human' OR c.status='manual') THEN coalesce(nullif(l.display_name,''),c.display_name) ELSE coalesce(nullif(c.display_name,''),l.display_name) END,
 summary=CASE WHEN (l.source='human' OR l.status='manual') AND NOT (c.source='human' OR c.status='manual') THEN coalesce(nullif(l.summary,''),c.summary) ELSE coalesce(nullif(c.summary,''),l.summary) END,
 description=CASE WHEN (l.source='human' OR l.status='manual') AND NOT (c.source='human' OR c.status='manual') THEN coalesce(nullif(l.description,''),c.description) ELSE coalesce(nullif(c.description,''),l.description) END,
 source=CASE WHEN l.source='human' OR l.status='manual' THEN 'human' ELSE c.source END,
 status=CASE WHEN l.source='human' OR l.status='manual' THEN 'manual' ELSE c.status END,source_hash=NULL,model=NULL
 FROM package_i18n l WHERE c.package_id=? AND l.package_id=? AND c.locale=l.locale`, to, from); err != nil {
		return err
	}
	if err := copyRows("package_i18n", "s.package_id=?", "jsonb_build_object('package_id',?::bigint)", to, from); err != nil {
		return err
	}
	// Keep a nonempty canonical source, otherwise promote retained human text.
	if err := st.selectMergedContentSource(ctx, content.Ref{Entity: "package", ID: to}); err != nil {
		return err
	}
	if err := exec("UPDATE package_i18n i SET status=CASE WHEN i.locale=p.source_locale THEN 'source' WHEN i.status='source' THEN 'manual' ELSE i.status END,source_locale=p.source_locale,source_hash=NULL FROM packages p WHERE i.package_id=p.id AND p.id=?", to); err != nil {
		return err
	}
	if err := copyRows("package_categories", "s.package_id=?", "jsonb_build_object('package_id',?::bigint,'is_primary',false)", to, from); err != nil {
		return err
	}
	if err := exec(`UPDATE package_categories SET is_primary=true WHERE package_id=? AND category_id=(SELECT category_id FROM package_categories WHERE package_id=? AND is_primary) AND NOT EXISTS(SELECT 1 FROM package_categories WHERE package_id=? AND is_primary)`, to, from, to); err != nil {
		return err
	}
	for _, table := range []string{"package_source_facts", "analytics_snapshots", "article_revision_packages", "tap_casks", "link_apps"} {
		if err := copyRows(table, "s.package_id=?", "jsonb_build_object('package_id',?::bigint)", to, from); err != nil {
			return err
		}
	}
	if err := exec(`UPDATE link_apps c SET homepage=coalesce(nullif(c.homepage,''),l.homepage),download_url=coalesce(nullif(c.download_url,''),l.download_url),github_repo=coalesce(c.github_repo,l.github_repo),team_id=coalesce(c.team_id,l.team_id),sparkle_feed=coalesce(c.sparkle_feed,l.sparkle_feed),asset_rules=coalesce(c.asset_rules,l.asset_rules),notes=coalesce(c.notes,l.notes),bundle_ids=ARRAY(SELECT DISTINCT unnest(c.bundle_ids||l.bundle_ids) ORDER BY 1),adopted_by_cask_at=? FROM link_apps l WHERE c.package_id=? AND l.package_id=?`, now, to, from); err != nil {
		return err
	}
	for _, table := range []string{"package_screenshots", "features", "feedback", "package_aliases", "update_feeds"} {
		if err := exec("UPDATE "+table+" SET package_id=? WHERE package_id=?", to, from); err != nil {
			return err
		}
	}
	// A manually selected changelog source outranks an automatic resolver.
	if err := exec(`UPDATE changelog_sources c SET config=l.config,priority=l.priority,enabled=c.enabled AND l.enabled,resolved_by='manual',etag=l.etag,last_modified=l.last_modified,last_fetched_at=l.last_fetched_at,last_status=l.last_status,last_error=l.last_error,fail_count=l.fail_count,next_fetch_at=l.next_fetch_at,updated_at=now() FROM changelog_sources l WHERE c.package_id=? AND l.package_id=? AND c.type=l.type AND c.resolved_by='auto' AND l.resolved_by='manual'`, to, from); err != nil {
		return err
	}
	// Preserve source IDs for noncolliding changelog sources; collisions are audited.
	if err := exec("DELETE FROM job_outbox WHERE job_type='changelog:fetch' AND (payload->>'sourceId')::bigint IN (SELECT l.id FROM changelog_sources l JOIN changelog_sources c ON c.type=l.type AND c.package_id=? WHERE l.package_id=?)", to, from); err != nil {
		return err
	}
	if err := exec("DELETE FROM changelog_sources l USING changelog_sources c WHERE l.package_id=? AND c.package_id=? AND l.type=c.type", from, to); err != nil {
		return err
	}
	if err := exec("UPDATE changelog_sources SET package_id=? WHERE package_id=?", to, from); err != nil {
		return err
	}
	var collections []int64
	if err := db.Raw("SELECT collection_id FROM collection_items WHERE package_id=?", from).Scan(&collections).Error; err != nil {
		return err
	}
	if err := copyRows("collection_items", "s.package_id=?", "jsonb_build_object('package_id',?::bigint)", to, from); err != nil {
		return err
	}
	if err := mergeLocaleText("collection_item_i18n", "package_id", []string{"note"}, from, to, true); err != nil {
		return err
	}
	if err := copyRows("collection_item_i18n", "s.package_id=?", "jsonb_build_object('package_id',?::bigint)", to, from); err != nil {
		return err
	}
	for _, id := range collections {
		if err := st.selectMergedContentSource(ctx, content.Ref{Entity: "collection_item", ID: id, SecondaryID: to}); err != nil {
			return err
		}
		if err := exec("UPDATE collection_item_i18n i SET status=CASE WHEN i.locale=c.source_locale THEN 'source' WHEN i.status='source' THEN 'manual' ELSE i.status END,source_locale=c.source_locale,source_hash=NULL FROM collection_items c WHERE c.package_id=? AND c.collection_id=? AND i.collection_id=c.collection_id AND i.package_id=c.package_id", to, id); err != nil {
			return err
		}
	}
	// Establish target versions first, then rebind both columns of the release FK.
	if err := exec(`INSERT INTO package_versions(package_id,version,version_base,first_seen_at,brew_committed_at,brew_commit_sha,created_at) SELECT ?,version,version_base,first_seen_at,brew_committed_at,brew_commit_sha,created_at FROM package_versions WHERE package_id=? ON CONFLICT(package_id,version) DO UPDATE SET first_seen_at=coalesce(package_versions.first_seen_at,excluded.first_seen_at),brew_committed_at=coalesce(package_versions.brew_committed_at,excluded.brew_committed_at),brew_commit_sha=coalesce(package_versions.brew_commit_sha,excluded.brew_commit_sha)`, to, from); err != nil {
		return err
	}
	var releases []struct {
		ID                int64
		TargetVersion     *int64
		Source, SourceKey string
		Hidden            bool
	}
	if err := db.Raw(`SELECT r.id,r.source,r.source_key,r.hidden,v.id AS target_version FROM releases r LEFT JOIN package_versions old ON old.id=r.package_version_id LEFT JOIN package_versions v ON v.package_id=? AND v.version=old.version WHERE r.package_id=? ORDER BY r.id`, to, from).Scan(&releases).Error; err != nil {
		return err
	}
	for _, r := range releases {
		var destination int64
		if err := db.Raw(`SELECT id FROM releases WHERE package_id=? AND ((source=? AND source_key=?) OR (?::bigint IS NOT NULL AND package_version_id=? AND source='editorial' AND NOT hidden AND NOT ?)) ORDER BY (source=? AND source_key=?) DESC,id LIMIT 1`, to, r.Source, r.SourceKey, r.TargetVersion, r.TargetVersion, r.Hidden, r.Source, r.SourceKey).Scan(&destination).Error; err != nil {
			return err
		}
		if destination == 0 {
			if err := exec("UPDATE releases SET package_id=?,package_version_id=? WHERE id=?", to, r.TargetVersion, r.ID); err != nil {
				return err
			}
			destination = r.ID
		} else {
			if err := mergeLocaleText("release_i18n", "release_id", []string{"summary", "title", "body_markdown", "sections"}, r.ID, destination, false); err != nil {
				return err
			}
			if err := copyRows("release_i18n", "s.release_id=?", "jsonb_build_object('release_id',?::bigint)", destination, r.ID); err != nil {
				return err
			}
			if err := exec(`UPDATE releases c SET hidden=c.hidden OR l.hidden,title=coalesce(nullif(c.title,''),l.title),body_markdown=coalesce(nullif(c.body_markdown,''),l.body_markdown),source_url=coalesce(c.source_url,l.source_url),published_at=coalesce(c.published_at,l.published_at) FROM releases l WHERE c.id=? AND l.id=?`, destination, r.ID); err != nil {
				return err
			}
			if err := st.ForgetContentTranslation(ctx, content.Ref{Entity: "release", ID: r.ID}); err != nil {
				return err
			}
			if err := exec("DELETE FROM release_i18n WHERE release_id=?", r.ID); err != nil {
				return err
			}
			if err := exec("DELETE FROM releases WHERE id=?", r.ID); err != nil {
				return err
			}
		}
		if err := st.selectMergedContentSource(ctx, content.Ref{Entity: "release", ID: destination}); err != nil {
			return err
		}
		if err := exec("UPDATE release_i18n i SET status=CASE WHEN i.locale=r.source_locale THEN 'source' WHEN i.status='source' THEN 'manual' ELSE i.status END,source_locale=r.source_locale,source_hash=NULL FROM releases r WHERE i.release_id=r.id AND r.id=?", destination); err != nil {
			return err
		}
		if err := st.ForgetContentTranslation(ctx, content.Ref{Entity: "release", ID: destination}); err != nil {
			return err
		}
		if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "release", ID: destination}); err != nil {
			return err
		}
	}
	// Rebuild only affected lists, deduplicate both ends and remove new self edges.
	// Canonical rows sort first; overflow is retained in the audit, never aborts sync.
	if err := exec(`CREATE TEMP TABLE link_merge_alternatives ON COMMIT DROP AS WITH mapped AS (
 SELECT CASE WHEN package_id=? THEN ? ELSE package_id END AS owner,CASE WHEN alternative_id=? THEN ? ELSE alternative_id END AS target,a.*,
 CASE WHEN package_id=? OR alternative_id=? THEN 1 ELSE 0 END AS priority FROM package_alternatives a WHERE package_id IN (?,?) OR package_id IN (SELECT package_id FROM package_alternatives WHERE alternative_id=?)
 ), dedup AS (SELECT *,row_number() OVER(PARTITION BY owner,target ORDER BY priority,sort,alternative_id) AS duplicate FROM mapped WHERE owner<>target)
 SELECT *,row_number() OVER(PARTITION BY owner ORDER BY priority,sort,target)-1 AS position FROM dedup WHERE duplicate=1`, from, to, from, to, from, from, from, to, from); err != nil {
		return err
	}
	if err := exec("DELETE FROM package_alternatives WHERE package_id IN (?,?) OR package_id IN (SELECT package_id FROM package_alternatives WHERE alternative_id=?)", from, to, from); err != nil {
		return err
	}
	if err := exec("INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,limits,best_for,note,source) SELECT owner,target,position,reasons,limits,best_for,note,source FROM link_merge_alternatives WHERE position<8"); err != nil {
		return err
	}
	if err := exec("DROP TABLE link_merge_alternatives"); err != nil {
		return err
	}
	for _, id := range []int64{from, to} {
		if err := st.ForgetContentTranslation(ctx, content.Ref{Entity: "package", ID: id}); err != nil {
			return err
		}
	}
	for _, id := range collections {
		for _, packageID := range []int64{from, to} {
			if err := st.ForgetContentTranslation(ctx, content.Ref{Entity: "collection_item", ID: id, SecondaryID: packageID}); err != nil {
				return err
			}
		}
		if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "collection_item", ID: id, SecondaryID: to}); err != nil {
			return err
		}
	}
	if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "package", ID: to}); err != nil {
		return err
	}
	if err := exec("DELETE FROM job_outbox WHERE (payload->>'packageId')::bigint=?", from); err != nil {
		return err
	}
	if err := exec("INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(?,'link',?,'delete','link')", from, source.Token); err != nil {
		return err
	}
	if err := mergeFactRelationships(ctx, tx, from, to); err != nil {
		return err
	}
	// Remove explicitly migrated/archived sources; no relationship is left to CASCADE.
	for _, table := range []string{"collection_item_i18n", "collection_items", "package_versions", "package_meta", "package_i18n", "package_categories", "package_source_facts", "analytics_snapshots", "article_revision_packages", "tap_casks", "package_facts", "link_apps"} {
		if err := exec("DELETE FROM "+table+" WHERE package_id=?", from); err != nil {
			return err
		}
	}
	if err := mergePageStates(ctx, tx, from, to); err != nil {
		return err
	}
	for _, table := range linkMergeTables {
		var n int64
		condition, args := "package_id=?", []any{from}
		if table == "seo_listing_candidates" {
			condition += " OR baseline_package_id=?"
			args = append(args, from)
		}
		if err := db.Raw("SELECT count(*) FROM "+table+" WHERE "+condition, args...).Scan(&n).Error; err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("unmigrated link relationship: %s", table)
		}
	}
	return exec("DELETE FROM packages WHERE id=? AND kind='link'", from)
}

// Empty source rows can clear translations when rescheduled. Select from the
// retained documents before normalizing status or hashing, using the same text
// definition as the translation engine (including whitespace and sections).
func (s *Store) selectMergedContentSource(ctx context.Context, ref content.Ref) error {
	spec := content.Registry[ref.Entity]
	where, args := textWhere(ref)
	parentWhere, parentArgs := "id=?", []any{ref.ID}
	if ref.Entity == "collection_item" {
		parentWhere, parentArgs = "collection_id=? AND package_id=?", []any{ref.ID, ref.SecondaryID}
	}
	var locale string
	if err := s.DB.WithContext(ctx).Table(spec.Parent).Select("source_locale").Where(parentWhere, parentArgs...).Scan(&locale).Error; err != nil {
		return err
	}
	var kind string
	if ref.Entity == "release" {
		if err := s.DB.WithContext(ctx).Table(spec.Parent).Select("source").Where(parentWhere, parentArgs...).Scan(&kind).Error; err != nil {
			return err
		}
	}
	var rows []struct{ Document json.RawMessage }
	if err := s.DB.WithContext(ctx).Raw("SELECT to_jsonb(i) AS document FROM "+spec.Table+" i WHERE "+where+" ORDER BY (locale=?) DESC,(status IN ('manual','source')) DESC,locale", append(args, locale)...).Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var document map[string]any
		if err := json.Unmarshal(row.Document, &document); err != nil {
			return err
		}
		if ref.Entity == "package" && document["source"] == "homebrew" {
			continue
		}
		fields := map[string]any{}
		for key := range spec.Fields {
			if key == "body_markdown" && ref.Entity == "release" && kind != "editorial" {
				continue
			}
			fields[key] = document[key]
		}
		if len(content.Flatten(fields)) > 0 {
			return s.DB.WithContext(ctx).Table(spec.Parent).Where(parentWhere, parentArgs...).Update("source_locale", document["locale"]).Error
		}
	}
	return nil
}
