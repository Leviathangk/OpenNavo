// Package seoopportunities turns durable observations into deduplicated tasks.
// Polling is only delivery: demand rules run when complete data or ledger inputs change.
package seoopportunities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

const RuleVersion = "opportunities-v1"

// Catalog changes are read in windows of rows that can open maintenance. Content
// and asset rows never do, so bulk cache invalidations (thousands of rows each)
// no longer hold back version updates.
const catalogWindow = 20000

// Each run also re-checks this many maintenance tasks against their package, so
// task evidence converges on the current version even if an event was missed.
const reconcileBatch = 2000

type Service struct{ DB *gorm.DB }
type signal struct {
	Kind, EntityKey, Locale, DedupeKey string
	Priority                           float64
	Evidence                           json.RawMessage
}

// maintenanceEvidence is shared by event delivery and reconciliation so both
// produce the same evidence hash for the same package state.
const maintenanceEvidence = `jsonb_build_object('source','catalog_changes','version',p.version,'token',p.token,'homepage',p.homepage)`

func (s *Service) Run(ctx context.Context) (map[string]any, error) {
	stats := map[string]any{"ruleVersion": RuleVersion}
	// Catalog writers allocate seq under LockCatalogWrites and commit before they
	// release it, so once the lock is free every row up to the newest seq is
	// visible. Taking it briefly in its own transaction keeps the cursor from
	// passing a sync that is still committing, without stalling writers for the
	// whole run.
	var head int64
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		return tx.Raw("SELECT coalesce(max(seq),0) FROM catalog_changes").Scan(&head).Error
	}); err != nil {
		return stats, err
	}
	count, stashed := 0, 0
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended('seo:opportunities',0))").Error; err != nil {
			return err
		}
		emit := func(query string, args ...any) (int, error) {
			var signals []signal
			if err := tx.Raw(query, args...).Scan(&signals).Error; err != nil {
				return 0, err
			}
			for _, v := range signals {
				held, err := save(ctx, tx, v)
				if err != nil {
					return 0, err
				}
				if held {
					stashed++
				}
				count++
			}
			return len(signals), nil
		}
		var cursor int64
		if err := tx.Raw("SELECT cursor FROM seo_opportunity_cursors WHERE source='catalog_changes' FOR UPDATE").Scan(&cursor).Error; err != nil {
			return err
		}
		// Only upstream changes open maintenance; the agent's own editorial and
		// asset writes are content/asset rows and do not re-open it.
		var changes []struct{ Seq, PackageID int64 }
		if err := tx.Raw("SELECT seq,package_id FROM catalog_changes WHERE seq>? AND seq<=? AND op='upsert' AND reason IN ('homebrew','link','tap') ORDER BY seq LIMIT ?", cursor, head, catalogWindow).Scan(&changes).Error; err != nil {
			return err
		}
		through := max(cursor, head)
		if len(changes) == catalogWindow {
			through = changes[len(changes)-1].Seq
		}
		ids, seen := []int64{}, map[int64]bool{}
		for _, c := range changes {
			if !seen[c.PackageID] {
				seen[c.PackageID] = true
				ids = append(ids, c.PackageID)
			}
		}
		maintenance := 0
		if len(ids) > 0 {
			n, err := emit(`SELECT 'maintenance' kind,'pkg:'||p.id entity_key,'en-US' locale,'catalog:pkg:'||p.id dedupe_key,10 priority,`+maintenanceEvidence+` evidence
   FROM packages p WHERE p.removed_at IS NULL AND p.kind IN ('cask','link') AND p.id IN ? ORDER BY p.id`, ids)
			if err != nil {
				return err
			}
			maintenance = n
		}
		if err := tx.Exec("UPDATE seo_opportunity_cursors SET cursor=?,updated_at=clock_timestamp() WHERE source='catalog_changes'", through).Error; err != nil {
			return err
		}
		// Events alone leave a task on an old version when one was missed or arrived
		// while the task was held. Compare maintenance tasks with their package.
		reconciled, err := emit(`SELECT 'maintenance' kind,'pkg:'||p.id entity_key,'en-US' locale,t.dedupe_key,10 priority,`+maintenanceEvidence+` evidence
   FROM seo_tasks t JOIN packages p ON p.id=split_part(t.dedupe_key,':',3)::bigint
   WHERE t.kind='maintenance' AND t.dedupe_key ~ '^catalog:pkg:[0-9]+$' AND t.state<>'expired' AND NOT (t.state='leased' AND t.lease_until>clock_timestamp())
   AND p.removed_at IS NULL AND p.kind IN ('cask','link')
   AND (t.evidence->>'version' IS DISTINCT FROM p.version OR t.evidence->>'token' IS DISTINCT FROM p.token OR t.evidence->>'homepage' IS DISTINCT FROM p.homepage)
   ORDER BY t.id LIMIT ?`, reconcileBatch)
		if err != nil {
			return err
		}
		stats["maintenance"], stats["reconciled"], stats["cursor"], stats["head"] = maintenance, reconciled, through, head
		// Receipt/target bookkeeping has a server task even before GSC is configured.
		if _, err := emit(`SELECT 'research' kind,'site:keywords' entity_key,'en-US' locale,'keyword-ledger:'||(clock_timestamp() AT TIME ZONE 'UTC')::date dedupe_key,5 priority,jsonb_build_object('source','ledger','day',(clock_timestamp() AT TIME ZONE 'UTC')::date) evidence`); err != nil {
			return err
		}
		// Safety signals are independent from measurement freshness and paid budgets.
		if _, err := emit(`SELECT 'tech_alert' kind,CASE WHEN u.package_id IS NULL THEN 'url:'||u.id ELSE 'pkg:'||u.package_id END entity_key,u.locale,
   'audit:'||a.id||':'||a.first_seen dedupe_key,CASE a.severity WHEN 'p0' THEN 100000 ELSE 1000 END priority,
   jsonb_build_object('source','audit','issueId',a.id,'rule',a.rule,'layer',a.layer,'url',u.url,'detail',a.detail) evidence
   FROM seo_audit_issues a JOIN seo_urls u ON u.id=a.url_id WHERE a.resolved_at IS NULL ORDER BY a.id LIMIT 2000`); err != nil {
			return err
		}
		if _, err := emit(`SELECT 'fix_fact' kind,'pkg:'||f.package_id entity_key,'en-US' locale,'expired-fact:'||f.id dedupe_key,10000 priority,
   jsonb_build_object('source','fact_expiry','factId',f.id,'kind',f.kind,'key',f.key,'evidence',f.evidence) evidence
   FROM package_fact_records f WHERE f.evidence->>'status'='verified' AND (f.evidence->>'validUntil')::timestamptz<=clock_timestamp() ORDER BY f.id LIMIT 2000`); err != nil {
			return err
		}
		// Articles published before the claim gate have no evidence receipts and the
		// editorial byline; the article job adds both and republishes. A skipped
		// refresh is retried a week later while the article still lacks receipts;
		// a quarantined one waits for a person.
		if err := tx.Exec(`UPDATE seo_tasks t SET state='open',next_retry_at=NULL,updated_at=clock_timestamp() FROM articles a JOIN article_revisions r ON r.id=a.published_revision_id
   WHERE t.kind='article_refresh' AND t.state='skipped' AND t.updated_at<clock_timestamp()-interval '7 days' AND t.entity_key='article:'||a.id AND t.evidence->>'revisionId'=r.id::text AND a.status='published' AND r.claim_evidence='[]'::jsonb AND r.editorial_reviewed_at IS NULL`).Error; err != nil {
			return err
		}
		if _, err := emit(`SELECT 'article_refresh' kind,'article:'||a.id entity_key,r.source_locale locale,'article-refresh:'||a.id||':'||r.id dedupe_key,8 priority,
   jsonb_build_object('source','legacy_article','articleId',a.id,'slug',a.slug,'revisionId',r.id,'authorId',r.author_id) evidence
   FROM articles a JOIN article_revisions r ON r.id=a.published_revision_id WHERE a.status='published' AND r.claim_evidence='[]'::jsonb AND r.editorial_reviewed_at IS NULL ORDER BY a.id`); err != nil {
			return err
		}
		if err := s.measurements(ctx, tx); err != nil {
			return err
		}
		// Homebrew install observations affect demand priority, never search volume. Only
		// raised priorities are written, so queued work is not rewritten every run.
		if err := tx.Exec(`UPDATE seo_tasks t SET priority=greatest(t.priority,10+ln(1+a.installs_30d)) FROM analytics_snapshots a WHERE a.snapshot_date=(SELECT max(snapshot_date) FROM analytics_snapshots) AND t.entity_key IN ('pkg:'||a.package_id,'package:'||a.package_id) AND t.kind IN ('maintenance','translate') AND t.state IN ('open','deferred') AND t.priority<10+ln(1+a.installs_30d)`).Error; err != nil {
			return err
		}
		var signature string
		if err := tx.Raw(`SELECT md5(coalesce((SELECT string_agg(source||':'||coalesce(complete_through::text,''),',' ORDER BY source) FROM seo_data_watermarks),'')||coalesce((SELECT revision::text FROM seo_entity_revisions WHERE entity_key='site:keywords'),''))`).Scan(&signature).Error; err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO seo_opportunity_cursors(source) VALUES('demand') ON CONFLICT DO NOTHING").Error; err != nil {
			return err
		}
		var prior string
		if err := tx.Raw("SELECT signature FROM seo_opportunity_cursors WHERE source='demand' FOR UPDATE").Scan(&prior).Error; err != nil {
			return err
		}
		if prior != signature {
			if err := s.demand(tx, emit); err != nil {
				return err
			}
			if err := tx.Exec("UPDATE seo_opportunity_cursors SET signature=?,updated_at=clock_timestamp() WHERE source='demand'", signature).Error; err != nil {
				return err
			}
		}
		return tx.Exec("UPDATE seo_watermark_events SET consumed_at=clock_timestamp() WHERE consumed_at IS NULL").Error
	})
	stats["signals"], stats["stashed"] = count, stashed
	return stats, err
}

func (s *Service) demand(tx *gorm.DB, emit func(string, ...any) (int, error)) error {
	// Both the window marker and each day must be complete. Query totals never stand in for page totals.
	const window = `SELECT w.complete_through FROM seo_data_watermarks w WHERE source='gsc_page' AND (SELECT count(*) FROM seo_ingest_days d WHERE d.source=w.source AND d.day BETWEEN w.complete_through-27 AND w.complete_through AND temporal_complete AND retrieval_complete)=28`
	if _, err := emit(`WITH demand AS (SELECT l.package_id,k.language locale,l.intent,sum(coalesce(k.search_volume,CASE WHEN k.source IN ('gsc','bing') THEN 1 ELSE 0 END)) volume,jsonb_agg(k.id ORDER BY k.id) keyword_ids FROM seo_keyword_links l JOIN seo_keywords k ON k.id=l.keyword_id WHERE l.state='confirmed' AND k.anomaly IS NULL AND k.observed_at>clock_timestamp()-interval '60 days' AND l.intent IN ('price','requirements','uninstall','alternative') GROUP BY l.package_id,k.language,l.intent)
  SELECT 'fill_fact' kind,'pkg:'||d.package_id entity_key,d.locale,'fill:'||d.package_id||':'||d.intent||':'||d.locale dedupe_key,20+ln(1+d.volume) priority,jsonb_build_object('source','keywords','intent',d.intent,'keywordIds',d.keyword_ids) evidence
  FROM demand d WHERE d.volume>0 AND NOT EXISTS(SELECT 1 FROM package_fact_records f WHERE f.package_id=d.package_id AND f.kind=CASE d.intent WHEN 'requirements' THEN 'compatibility' ELSE d.intent END AND f.evidence->>'status'='verified' AND (f.evidence->>'validUntil')::timestamptz>clock_timestamp()) AND (d.intent!='alternative' OR NOT EXISTS(SELECT 1 FROM package_alternatives a WHERE a.package_id=d.package_id)) ORDER BY d.volume DESC LIMIT 2000`); err != nil {
		return err
	}
	if err := tx.Exec(`WITH metrics AS(SELECT u.package_id,u.locale,sum(p.impressions) impressions FROM seo_gsc_page_daily p JOIN seo_urls u ON u.id=p.url_id CROSS JOIN (` + window + `) w WHERE p.day BETWEEN w.complete_through-27 AND w.complete_through AND u.package_id IS NOT NULL GROUP BY u.package_id,u.locale) UPDATE seo_tasks t SET priority=greatest(t.priority,20+ln(1+m.impressions)) FROM metrics m WHERE t.kind='translate' AND t.entity_key IN ('pkg:'||m.package_id,'package:'||m.package_id) AND t.locale=m.locale AND t.state IN ('open','deferred') AND t.priority<20+ln(1+m.impressions)`).Error; err != nil {
		return err
	}
	if _, err := emit(`SELECT 'discover_fix' kind,CASE WHEN u.package_id IS NULL THEN 'url:'||u.id ELSE 'pkg:'||u.package_id END entity_key,u.locale,'discover:'||u.id dedupe_key,30 priority,jsonb_build_object('source','inspection','url',u.url,'coverageState',s.coverage_state,'canonical',s.google_canonical) evidence FROM seo_url_status s JOIN seo_urls u ON u.id=s.url_id WHERE s.checked_at>clock_timestamp()-interval '14 days' AND (s.coverage_state ILIKE '%not indexed%' OR s.coverage_state ILIKE '%未编入索引%') ORDER BY u.id LIMIT 1000`); err != nil {
		return err
	}
	if _, err := emit(`WITH candidates AS(SELECT k.*,regexp_replace(trim(k.normalized),'[^a-z0-9]+','-','g') slug FROM seo_keywords k WHERE (k.search_volume>0 OR k.source IN ('gsc','bing')) AND k.anomaly IS NULL AND k.observed_at>clock_timestamp()-interval '60 days' AND k.normalized~'(^| )(mac|macos)( |$)' AND k.normalized!~'(crack|torrent|pirate|破解|盗版)' AND NOT EXISTS(SELECT 1 FROM seo_keyword_links l WHERE l.keyword_id=k.id AND l.state='confirmed'))
  SELECT CASE WHEN normalized~'(^| )(best|how|guide|tools)( |$)' THEN 'topic' ELSE 'listing' END kind,
  CASE WHEN normalized~'(^| )(best|how|guide|tools)( |$)' THEN 'article:new:' ELSE 'listing:' END||slug entity_key,language locale,'demand:'||id dedupe_key,10+ln(1+coalesce(search_volume,1)) priority,jsonb_build_object('source','keywords','keywordId',id,'keyword',keyword,'targetSlug',slug) evidence
  FROM candidates WHERE length(slug) BETWEEN 1 AND 100 AND slug~'^[a-z0-9].*[a-z0-9]$' AND NOT EXISTS(SELECT 1 FROM seo_targets t WHERE t.entity_key='topic:'||slug AND t.locale=candidates.language) ORDER BY search_volume DESC LIMIT 500`); err != nil {
		return err
	}
	if _, err := emit(`SELECT 'alias' kind,'pkg:'||l.package_id entity_key,k.language locale,'alias:'||k.id||':'||l.package_id dedupe_key,10+ln(1+coalesce(k.search_volume,0)) priority,jsonb_build_object('source','confirmed_link','keywordId',k.id,'alias',k.keyword) evidence FROM seo_keyword_links l JOIN seo_keywords k ON k.id=l.keyword_id JOIN packages p ON p.id=l.package_id WHERE l.state='confirmed' AND l.confidence>=0.95 AND l.intent IN ('download','install','brew') AND k.anomaly IS NULL AND k.normalized<>lower(p.token) AND k.normalized NOT LIKE '%'||lower(p.token)||'%' ORDER BY k.id LIMIT 500`); err != nil {
		return err
	}
	_, err := emit(`WITH m AS(SELECT u.page_kind,u.locale,floor(p.position_sum/nullif(p.impressions,0)/5) bucket,sum(p.clicks) clicks,sum(p.impressions) impressions FROM seo_gsc_page_daily p JOIN seo_urls u ON u.id=p.url_id CROSS JOIN (` + window + `) w WHERE p.day BETWEEN w.complete_through-27 AND w.complete_through GROUP BY u.page_kind,u.locale,bucket), b AS(SELECT bucket,sum(clicks)::numeric/nullif(sum(impressions),0) ctr FROM m GROUP BY bucket)
  SELECT 'snippet_template' kind,'template:'||m.page_kind||':'||m.locale entity_key,m.locale,'snippet:'||m.page_kind||':'||m.locale||':'||m.bucket dedupe_key,15 priority,jsonb_build_object('source','complete_gsc','positionBucket',m.bucket,'ctr',m.clicks::numeric/nullif(m.impressions,0),'baselineCtr',b.ctr) evidence FROM m JOIN b USING(bucket) WHERE m.page_kind='overview' AND m.impressions>=1000 AND m.clicks::numeric/nullif(m.impressions,0)<b.ctr*0.7`)
	return err
}

// save upserts a signal and reports whether its evidence had to wait for an
// active lease. A held task keeps the evidence its owner is working from; the new
// evidence waits in pending_evidence and replaces it when the lease ends, so the
// run never fails or rolls back delivery because of a lease. An expired lease no
// longer protects the task. Unchanged signals do not touch the row, so updated_at
// keeps meaning "last real change".
func save(ctx context.Context, tx *gorm.DB, s signal) (bool, error) {
	if s.EntityKey == "" || !json.Valid(s.Evidence) {
		return false, fmt.Errorf("invalid opportunity signal")
	}
	s.EntityKey = strings.Replace(s.EntityKey, "package:", "pkg:", 1)
	const held = `(seo_tasks.state='leased' AND seo_tasks.lease_until>clock_timestamp())`
	const changed = `seo_tasks.evidence_hash<>EXCLUDED.evidence_hash`
	var out struct{ Stashed bool }
	err := tx.WithContext(ctx).Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version) VALUES(?,?,NULLIF(?,''),?,?,?::jsonb,?,?) ON CONFLICT(dedupe_key) DO UPDATE SET priority=greatest(seo_tasks.priority,EXCLUDED.priority),updated_at=clock_timestamp(),
  evidence=CASE WHEN `+held+` THEN seo_tasks.evidence ELSE EXCLUDED.evidence END,
  evidence_hash=CASE WHEN `+held+` THEN seo_tasks.evidence_hash ELSE EXCLUDED.evidence_hash END,
  pending_evidence=CASE WHEN `+held+` AND `+changed+` THEN EXCLUDED.evidence END,
  pending_evidence_hash=CASE WHEN `+held+` AND `+changed+` THEN EXCLUDED.evidence_hash END,
  state=CASE WHEN NOT `+held+` AND `+changed+` AND seo_tasks.state NOT IN ('quarantined','expired') THEN 'open' ELSE seo_tasks.state END,
  lease_owner=CASE WHEN NOT `+held+` AND `+changed+` AND seo_tasks.state='leased' THEN NULL ELSE seo_tasks.lease_owner END,
  lease_until=CASE WHEN NOT `+held+` AND `+changed+` AND seo_tasks.state='leased' THEN NULL ELSE seo_tasks.lease_until END,
  next_retry_at=CASE WHEN NOT `+held+` AND `+changed+` THEN NULL ELSE seo_tasks.next_retry_at END
  WHERE seo_tasks.priority<EXCLUDED.priority OR (NOT `+held+` AND `+changed+`)
   OR seo_tasks.pending_evidence_hash IS DISTINCT FROM (CASE WHEN `+held+` AND `+changed+` THEN EXCLUDED.evidence_hash END)
  RETURNING pending_evidence_hash IS NOT NULL AS stashed`, s.Kind, s.EntityKey, s.Locale, s.DedupeKey, s.Priority, string(s.Evidence), seotasks.Hash(s.Evidence), RuleVersion).Scan(&out).Error
	return out.Stashed, err
}
