package seoopportunities

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/seokeywords"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// Search observations are not search-volume estimates. Query impressions only
// establish observed demand; they never replace page totals or paid market data.
func (s *Service) measurements(ctx context.Context, tx *gorm.DB) error {
	var marker string
	if err := tx.Raw(`SELECT md5(coalesce((SELECT string_agg(source||coalesce(complete_through::text,''),',' ORDER BY source) FROM seo_data_watermarks WHERE source IN ('gsc_query','bing')),'')||coalesce((SELECT max(id)::text FROM search_queries),'')||coalesce((SELECT max(snapshot_date)::text||':'||count(*) FROM analytics_snapshots),''))`).Scan(&marker).Error; err != nil {
		return err
	}
	if err := tx.Exec("INSERT INTO seo_opportunity_cursors(source) VALUES('measurements') ON CONFLICT DO NOTHING").Error; err != nil {
		return err
	}
	var prior string
	if err := tx.Raw("SELECT signature FROM seo_opportunity_cursors WHERE source='measurements'").Scan(&prior).Error; err != nil {
		return err
	}
	if prior == marker {
		return nil
	}
	type measured struct {
		Keyword, Locale string
		Day             time.Time
		Position        *float64
		Volume          *int64
	}
	inputs := []struct{ source, engine, query string }{
		{"gsc", "google", `SELECT g.query keyword,u.locale,max(g.day) AS day,sum(g.position_sum)/nullif(sum(g.impressions),0) position FROM seo_gsc_query_daily g JOIN seo_urls u ON u.id=g.url_id JOIN seo_data_watermarks w ON w.source='gsc_query' JOIN seo_ingest_days d ON d.source=w.source AND d.day=g.day AND d.temporal_complete AND d.retrieval_complete WHERE g.day BETWEEN w.complete_through-27 AND w.complete_through AND g.anomaly IS NULL AND g.impressions>0 GROUP BY g.query,u.locale ORDER BY sum(g.impressions) DESC,g.query,u.locale LIMIT 1000`},
		{"bing", "bing", `SELECT value keyword,'en-US' locale,max(b.day) AS day,sum(position_sum)/nullif(sum(impressions),0) position FROM seo_bing_daily b JOIN seo_data_watermarks w ON w.source='bing' WHERE b.dimension='query' AND b.day BETWEEN w.complete_through-27 AND w.complete_through AND b.impressions>0 GROUP BY value ORDER BY sum(impressions) DESC,value LIMIT 1000`},
		{"site_search", "site", `SELECT query keyword,locale,max(created_at)::date AS day,count(*) volume FROM search_queries WHERE created_at>clock_timestamp()-interval '28 days' GROUP BY query,locale ORDER BY count(*) DESC,query,locale LIMIT 1000`},
		{"homebrew", "homebrew", `SELECT p.name||' mac' keyword,'en-US' locale,a.snapshot_date AS day FROM analytics_snapshots a JOIN packages p ON p.id=a.package_id WHERE a.snapshot_date=(SELECT max(snapshot_date) FROM analytics_snapshots) AND p.kind='cask' AND p.removed_at IS NULL AND a.installs_30d>0 ORDER BY a.installs_30d DESC,p.id LIMIT 1000`},
	}
	for _, input := range inputs {
		var rows []measured
		if err := tx.Raw(input.query).Scan(&rows).Error; err != nil {
			return err
		}
		observations := []seokeywords.Observation{}
		for _, row := range rows {
			if !i18n.Valid(row.Locale) || seokeywords.Normalize(row.Keyword) == "" || len(row.Keyword) > 1000 || strings.IndexFunc(row.Keyword, unicode.IsControl) >= 0 {
				continue
			}
			o := seokeywords.Observation{Keyword: row.Keyword, Source: input.source, Engine: input.engine, Country: "ALL", Language: row.Locale, Device: "all", Period: row.Day.Format("2006-01-02"), VolumeState: "unknown", OurPosition: row.Position, ObservedAt: row.Day.UTC()}
			if input.source == "site_search" {
				o.SearchVolume = row.Volume
				o.VolumeState = "known"
			}
			observations = append(observations, o)
		}
		for len(observations) > 0 {
			n := min(100, len(observations))
			if _, err := (&seokeywords.Service{DB: tx}).Upsert(ctx, seokeywords.Request{Records: observations[:n]}, false, store.AuditActor{}); err != nil {
				return fmt.Errorf("local SEO observation import: %w", err)
			}
			observations = observations[n:]
		}
	}
	return tx.Exec("UPDATE seo_opportunity_cursors SET signature=?,updated_at=clock_timestamp() WHERE source='measurements'", marker).Error
}
