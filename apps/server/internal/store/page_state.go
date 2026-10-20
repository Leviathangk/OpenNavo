package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/seo"
	"gorm.io/gorm"
)

type PageState struct {
	PackageID                     int64
	Locale, PageKind, ContentHash string
	PageNumber                    int
	ContentModifiedAt             time.Time
}

// stableContent strips persistence metadata, never public evidence dates or version dates.
func stableContent(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"updated_at", "created_at", "updated_by", "created_by", "reviewed_by", "reviewed_at", "translated_at", "fetched_at", "synced_at", "etag", "last_error", "fail_reason", "source_hash", "body_hash", "revision", "revision_id", "next_check_at", "fail_count"} {
			delete(v, key)
		}
		for key, child := range v {
			if key == "evidence" {
				raw, _ := json.Marshal(child)
				var proof facts.Proof
				if json.Unmarshal(raw, &proof) == nil {
					child = proof.At(time.Now())
				}
			}
			v[key] = stableContent(child)
		}
	case []any:
		for i, child := range v {
			v[i] = stableContent(child)
		}
	}
	return value
}
func pageContentHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	raw, err = json.Marshal(stableContent(decoded))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// PageStateInputs contains only page-visible values. Localized text follows the public fallback
// order, so editing French cannot advance an already translated German page's timestamp.
func (s *Store) pageStateInputs(ctx context.Context, id int64, locale string) (json.RawMessage, json.RawMessage, error) {
	var row struct{ Overview, Dependencies string }
	err := s.DB.WithContext(ctx).Raw(`SELECT jsonb_build_object(
 'app',jsonb_build_object('kind',p.kind,'token',p.token,'name',p.name,'names',p.names,'version',p.version,
 'description',p.desc_en,'homepage',p.homepage,'download',p.download_url,'size',p.download_size,'license',p.license,
 'artifacts',p.artifacts,'caveats',p.caveats,'dependencies',p.dependencies,'conflicts',p.conflicts_with,
 'platforms',p.raw->'variations','sha256',p.raw->'sha256','macos',p.min_macos,'arm64',p.supports_arm64,
 'x86',p.supports_x86_64,'disabled',p.disabled,'deprecated',p.deprecated,'stale',p.stale_disabled,
 'disableReason',p.disable_reason,'deprecationReason',p.deprecation_reason,'removed',p.removed_at IS NOT NULL),
 'meta',(SELECT to_jsonb(m)-ARRAY['notes','index_override_reason','index_override_at'] FROM package_meta m WHERE m.package_id=p.id),
 'text',(SELECT to_jsonb(t) FROM package_i18n t WHERE t.package_id=p.id AND t.locale IN (lang.requested,p.source_locale,'en-US') AND COALESCE(NULLIF(t.display_name,''),NULLIF(t.summary,''),NULLIF(t.description,'')) IS NOT NULL ORDER BY `+publicLocaleOrder+` LIMIT 1),
 'facts',(SELECT to_jsonb(f) FROM package_facts f WHERE f.package_id=p.id),
 'link',(SELECT to_jsonb(l) FROM link_apps l WHERE l.package_id=p.id),
 'security',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.product_cpe) FROM package_cve_checks c WHERE c.package_id=p.id),
 'records',(SELECT jsonb_agg(to_jsonb(f) ORDER BY f.kind,f.key) FROM package_fact_records f WHERE f.package_id=p.id),
 'sources',(SELECT jsonb_agg(to_jsonb(f) ORDER BY f.source) FROM package_source_facts f WHERE f.package_id=p.id AND f.status='found'),
 'alternatives',(SELECT jsonb_agg(jsonb_build_object('relation',to_jsonb(a),'name',q.name,'token',q.token,'version',q.version,'disabled',q.disabled,'facts',(SELECT to_jsonb(af) FROM package_facts af WHERE af.package_id=q.id),'records',(SELECT jsonb_agg(to_jsonb(ar) ORDER BY ar.kind,ar.key) FROM package_fact_records ar WHERE ar.package_id=q.id)) ORDER BY a.sort,a.alternative_id) FROM package_alternatives a JOIN packages q ON q.id=a.alternative_id WHERE a.package_id=p.id AND q.removed_at IS NULL),
 'aliases',(SELECT jsonb_agg(a.token ORDER BY a.token) FROM package_aliases a WHERE a.package_id=p.id),
 'categories',(SELECT jsonb_agg(jsonb_build_object('slug',c.slug,'primary',pc.is_primary,'text',(SELECT to_jsonb(t) FROM category_i18n t WHERE t.category_id=c.id AND t.locale IN (lang.requested,c.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1)) ORDER BY c.slug) FROM package_categories pc JOIN categories c ON c.id=pc.category_id WHERE pc.package_id=p.id AND c.visible),
 'collections',(SELECT jsonb_agg(jsonb_build_object('slug',c.slug,'text',(SELECT to_jsonb(t) FROM collection_i18n t WHERE t.collection_id=c.id AND t.locale IN (lang.requested,c.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1)) ORDER BY c.slug) FROM collection_items ci JOIN collections c ON c.id=ci.collection_id WHERE ci.package_id=p.id AND c.status='published'),
 'articles',(SELECT jsonb_agg(jsonb_build_object('slug',a.slug,'type',r.type,'text',(SELECT to_jsonb(t) FROM article_revision_i18n t WHERE t.revision_id=r.id AND t.locale IN (lang.requested,r.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1)) ORDER BY a.slug) FROM article_revision_packages ap JOIN article_revisions r ON r.id=ap.revision_id JOIN articles a ON a.published_revision_id=r.id WHERE ap.package_id=p.id AND a.status='published')
 )::text AS overview,
 jsonb_build_object('dependencies',p.dependencies,'conflicts',p.conflicts_with,
 'reverse',(SELECT jsonb_agg(jsonb_build_object('token',q.token,'name',q.name,'version',q.version) ORDER BY q.token) FROM packages q WHERE q.removed_at IS NULL AND q.dependencies->'dependsOn'->'cask' @> jsonb_build_array(p.token)))::text AS dependencies
 FROM packages p CROSS JOIN (SELECT ?::text requested) lang WHERE p.id=?`, locale, id).Scan(&row).Error
	return json.RawMessage(row.Overview), json.RawMessage(row.Dependencies), err
}

// RefreshPackagePageStates locks only dirty queue rows. New writes wait on that row and then
// enqueue again, so a change concurrent with hashing is never acknowledged by an older snapshot.
func (s *Store) RefreshPackagePageStates(ctx context.Context, limit int) (int, error) {
	// Expired evidence changes public output without a write. Queue its owners and referring apps.
	if err := s.DB.WithContext(ctx).Exec(`WITH expired AS (
      SELECT f.package_id,max((f.evidence->>'validUntil')::timestamptz) AS at FROM package_fact_records f
      WHERE f.evidence->>'status'='verified' AND (f.evidence->>'validUntil')::timestamptz<=now()
      GROUP BY f.package_id
    ), affected AS (SELECT package_id,at FROM expired UNION SELECT a.package_id,e.at FROM expired e JOIN package_alternatives a ON a.alternative_id=e.package_id)
    INSERT INTO package_page_dirty(package_id,changed_at)
    SELECT x.package_id,max(x.at) FROM affected x WHERE x.at>COALESCE((SELECT max(content_checked_at) FROM package_page_state WHERE package_id=x.package_id),'-infinity'::timestamptz)
    GROUP BY x.package_id ON CONFLICT(package_id) DO UPDATE SET changed_at=GREATEST(package_page_dirty.changed_at,EXCLUDED.changed_at)`).Error; err != nil {
		return 0, err
	}
	count := 0
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var dirty []struct {
			PackageID int64
			ChangedAt time.Time
		}
		if err := tx.Raw(`SELECT package_id,changed_at FROM package_page_dirty ORDER BY changed_at,package_id LIMIT ? FOR UPDATE SKIP LOCKED`, min(max(limit, 1), 100)).Scan(&dirty).Error; err != nil {
			return err
		}
		st := &Store{DB: tx}
		for _, job := range dirty {
			row, found, err := st.IndexRowByID(ctx, job.PackageID)
			if err != nil {
				return err
			}
			if found {
				compatibilities := map[string]*publicapi.VersionCompatibility{}
				now := time.Now()
				for _, locale := range seo.Locales {
					overview, deps, err := st.pageStateInputs(ctx, row.ID, locale)
					if err != nil {
						return err
					}
					shots, err := st.PublicScreenshots(ctx, row.ID, locale)
					if err != nil {
						return err
					}
					entries, err := st.TimelineEntries(ctx, row.ID, locale)
					if err != nil {
						return err
					}
					for i := range entries {
						version := entries[i].Version
						compatibility, ok := compatibilities[version]
						if !ok {
							requirements, err := st.VersionRequirements(ctx, row.ID, version, now)
							if err != nil {
								return err
							}
							raw, err := json.Marshal(facts.SummarizeCompatibility(version, requirements, now))
							if err != nil {
								return err
							}
							compatibility = &publicapi.VersionCompatibility{}
							if err := json.Unmarshal(raw, compatibility); err != nil {
								return err
							}
							compatibilities[version] = compatibility
						}
						entries[i].Compatibility = compatibility
					}
					baseline := row.Lastmod(locale)
					if baseline == nil {
						value := row.FirstSeenAt
						baseline = &value
					}
					save := func(kind string, page int, input any) error {
						hash, err := pageContentHash(input)
						if err != nil {
							return err
						}
						return tx.Exec(`INSERT INTO package_page_state(package_id,locale,page_kind,page_number,content_hash,content_modified_at)
       VALUES(?,?,?,?,?,?) ON CONFLICT(package_id,locale,page_kind,page_number) DO UPDATE SET
       content_hash=EXCLUDED.content_hash,content_checked_at=now(),
       content_modified_at=CASE WHEN package_page_state.content_hash<>EXCLUDED.content_hash
       THEN GREATEST(package_page_state.content_modified_at+interval '1 microsecond',?)
       ELSE package_page_state.content_modified_at END`, row.ID, locale, kind, page, hash, baseline, job.ChangedAt).Error
					}
					latest := entries[:min(1, len(entries))]
					if err := save("overview", 1, []any{overview, shots, latest}); err != nil {
						return err
					}
					if err := save("details", 1, overview); err != nil {
						return err
					}
					if err := save("dependencies", 1, deps); err != nil {
						return err
					}
					pages := max(1, (len(entries)+seo.VersionsPageSize-1)/seo.VersionsPageSize)
					for page := 1; page <= pages; page++ {
						start := min(len(entries), (page-1)*seo.VersionsPageSize)
						if err := save("versions", page, entries[start:min(len(entries), start+seo.VersionsPageSize)]); err != nil {
							return err
						}
					}
					var previousPages int
					if err := tx.Raw("SELECT COALESCE(max(page_number),0) FROM package_page_state WHERE package_id=? AND locale=? AND page_kind='versions'", row.ID, locale).Scan(&previousPages).Error; err != nil {
						return err
					}
					for page := pages + 1; page <= previousPages; page++ {
						if err := save("versions", page, []any{}); err != nil {
							return err
						}
					}
				}
			}
			if err := tx.Exec("DELETE FROM package_page_dirty WHERE package_id=?", job.PackageID).Error; err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (s *Store) PageStates(ctx context.Context, ids []int64) ([]PageState, error) {
	rows := []PageState{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := s.DB.WithContext(ctx).Raw("SELECT * FROM package_page_state WHERE package_id IN ? ORDER BY package_id,locale,page_kind,page_number", ids).Scan(&rows).Error
	return rows, err
}

// Page hashes describe pre-merge content and must be recomputed for the survivor.
// Call after relationship deletes, whose triggers can requeue the retiring package.
func mergePageStates(ctx context.Context, tx *gorm.DB, from, to int64) error {
	db := tx.WithContext(ctx)
	if err := db.Exec("DELETE FROM package_page_state WHERE package_id IN (?,?)", from, to).Error; err != nil {
		return err
	}
	if err := db.Exec("DELETE FROM package_page_dirty WHERE package_id=?", from).Error; err != nil {
		return err
	}
	return db.Exec(`INSERT INTO package_page_dirty(package_id) VALUES(?) ON CONFLICT(package_id)
 DO UPDATE SET changed_at=GREATEST(package_page_dirty.changed_at,now())`, to).Error
}
