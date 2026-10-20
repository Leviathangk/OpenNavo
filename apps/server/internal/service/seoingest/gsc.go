package seoingest

import (
	"context"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"gorm.io/gorm"
)

var errOutsideSite = errors.New("SEO URL outside configured site")

var digitSuffix = regexp.MustCompile(`[[:alpha:]][ _-]*[0-9]{6,}$`)

// fetchDay never represents an API row cap as a fully retrieved day.
func fetchDay(ctx context.Context, client Google, date, source string) ([]SearchRow, string, bool, error) {
	dimensions := []string{"date", "page", "country", "device"}
	if source == "gsc_query" {
		dimensions = []string{"date", "page", "query"}
	}
	var rows []SearchRow
	firstIncomplete := ""
	seen := map[string]bool{}
	for offset := 0; offset < maxDailyRows; offset += pageLimit {
		result, err := client.Analytics(ctx, Query{StartDate: date, EndDate: date, Dimensions: dimensions, DataState: "all", Type: "web", RowLimit: pageLimit, StartRow: offset})
		if err != nil {
			return nil, "", false, err
		}
		if len(result.Rows) > pageLimit {
			return nil, "", false, ErrUpstream
		}
		if d := result.Metadata.FirstIncompleteDate; d != "" {
			if _, err := parseDay(d); err != nil {
				return nil, "", false, ErrUpstream
			}
			if firstIncomplete == "" || d < firstIncomplete {
				firstIncomplete = d
			}
		}
		for _, r := range result.Rows {
			if len(r.Keys) != len(dimensions) || r.Keys[0] != date || r.Clicks < 0 || r.Impressions < r.Clicks || r.Position < 0 || math.IsNaN(r.Position) || math.IsInf(r.Position, 0) {
				return nil, "", false, ErrUpstream
			}
			key := strings.Join(r.Keys, "\x00")
			// A changing result order while paginating cannot be declared complete.
			if seen[key] {
				return nil, "", false, ErrUpstream
			}
			seen[key] = true
		}
		rows = append(rows, result.Rows...)
		if len(result.Rows) < pageLimit {
			return rows, firstIncomplete, true, nil
		}
	}
	return rows, firstIncomplete, false, nil
}

func (s *Service) GSC(ctx context.Context) (map[string]any, error) {
	client, err := s.Google(ctx)
	if errors.Is(err, ErrDisabled) {
		return map[string]any{"disabled": true}, nil
	}
	if err != nil {
		return nil, err
	}
	// Search Console dates are Pacific dates, not scheduler/UTC dates.
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return nil, err
	}
	end, err := parseDay(day(s.now().In(loc).AddDate(0, 0, -1)))
	if err != nil {
		return nil, err
	}
	count := 0
	for _, source := range []string{"gsc_page", "gsc_query"} {
		var watermark struct{ CompleteThrough *time.Time }
		if err := s.DB.WithContext(ctx).Table("seo_data_watermarks").Where("source=?", source).Take(&watermark).Error; err != nil {
			return nil, err
		}
		start := end.AddDate(0, 0, -27)
		if watermark.CompleteThrough != nil {
			start = watermark.CompleteThrough.AddDate(0, 0, 1)
			if overlap := end.AddDate(0, 0, -6); start.After(overlap) {
				start = overlap
			}
		}
		// Bounded catch-up. A later run continues from the contiguous watermark.
		for d, n := start, 0; !d.After(end) && n < 28; d, n = d.AddDate(0, 0, 1), n+1 {
			fetched := s.now()
			rows, incomplete, retrieved, err := fetchDay(ctx, client, day(d), source)
			if err != nil {
				return nil, err
			}
			err = s.saveGSC(ctx, source, day(d), rows, incomplete, retrieved, fetched)
			if err != nil {
				return nil, err
			}
			count += len(rows)
		}
	}
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return seobudget.AssessBusiness(ctx, tx, seobudget.Site(s.WebURL)) }); err != nil {
		return nil, err
	}
	return map[string]any{"rows": count}, nil
}

func (s *Service) registerURL(tx *gorm.DB, raw string) (int64, error) {
	u, err := url.Parse(raw)
	base, baseErr := url.Parse(s.WebURL)
	if err != nil || baseErr != nil || base.Host == "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return 0, ErrUpstream
	}
	if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) {
		return 0, errOutsideSite
	}
	route := parseAuditRoute(u.EscapedPath())
	var packageID *int64
	if route.Package && route.Valid {
		var id int64
		if err := tx.Raw(packageURLLookup, route.Token, route.Token, route.Token).Scan(&id).Error; err != nil {
			return 0, err
		}
		if id > 0 {
			packageID = &id
		}
	}
	var id int64
	err = tx.Raw(`INSERT INTO seo_urls(url,page_kind,locale,package_id) VALUES(?,?,?,?) ON CONFLICT(url) DO UPDATE SET package_id=EXCLUDED.package_id,page_kind=EXCLUDED.page_kind,locale=EXCLUDED.locale RETURNING id`, raw, route.Kind, route.Locale, packageID).Scan(&id).Error
	return id, err
}

func (s *Service) saveGSC(ctx context.Context, source, date string, rows []SearchRow, incomplete string, retrieved bool, fetched time.Time) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSource(tx, source); err != nil {
			return err
		}
		var previous struct{ FetchedAt time.Time }
		if err := tx.Raw("SELECT fetched_at FROM seo_ingest_days WHERE source=? AND day=?", source, date).Scan(&previous).Error; err != nil {
			return err
		}
		if previous.FetchedAt.After(fetched) {
			return nil
		}
		table := "seo_gsc_page_daily"
		if source == "gsc_query" {
			table = "seo_gsc_query_daily"
		}
		if err := tx.Exec("DELETE FROM "+table+" WHERE day=?", date).Error; err != nil {
			return err
		}
		for _, r := range rows {
			id, err := s.registerURL(tx, r.Keys[1])
			// Domain properties include HTTP and other origins. They are outside
			// this site registry, but do not invalidate the fetched day.
			if errors.Is(err, errOutsideSite) {
				continue
			}
			if err != nil {
				return err
			}
			if source == "gsc_page" {
				err = tx.Exec(`INSERT INTO seo_gsc_page_daily(day,url_id,country,device,clicks,impressions,position_sum) VALUES(?,?,?,?,?,?,?)`, date, id, r.Keys[2], r.Keys[3], r.Clicks, r.Impressions, r.Position*float64(r.Impressions)).Error
			} else {
				var anomaly *string
				if digitSuffix.MatchString(r.Keys[2]) {
					v := "digit_suffix_burst"
					anomaly = &v
				}
				err = tx.Exec(`INSERT INTO seo_gsc_query_daily(day,url_id,query,clicks,impressions,position_sum,anomaly) VALUES(?,?,?,?,?,?,?)`, date, id, r.Keys[2], r.Clicks, r.Impressions, r.Position*float64(r.Impressions), anomaly).Error
			}
			if err != nil {
				return err
			}
		}
		var first *string
		if incomplete != "" {
			first = &incomplete
		}
		if err := tx.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,first_incomplete_date,row_count,coverage,fetched_at) VALUES(?,?,?,?,?,?,'api_top_rows',?) ON CONFLICT(source,day) DO UPDATE SET temporal_complete=EXCLUDED.temporal_complete,retrieval_complete=EXCLUDED.retrieval_complete,first_incomplete_date=EXCLUDED.first_incomplete_date,row_count=EXCLUDED.row_count,fetched_at=EXCLUDED.fetched_at`, source, date, incomplete == "" || date < incomplete, retrieved, first, len(rows), fetched).Error; err != nil {
			return err
		}
		return advanceWatermark(tx, source, fetched)
	})
}

func lockSource(tx *gorm.DB, source string) error {
	return tx.Exec("SELECT source FROM seo_data_watermarks WHERE source=? FOR UPDATE", source).Error
}

// Compute the contiguous complete prefix, including empty days. No gap can be skipped.
func advanceWatermark(tx *gorm.DB, source string, now time.Time) error {
	var rows []struct {
		Day                                 time.Time
		TemporalComplete, RetrievalComplete bool
	}
	if err := tx.Raw("SELECT day,temporal_complete,retrieval_complete FROM seo_ingest_days WHERE source=? ORDER BY day", source).Scan(&rows).Error; err != nil {
		return err
	}
	var complete, fresh *time.Time
	for i, r := range rows {
		d := r.Day
		fresh = &d
		if i == 0 && r.TemporalComplete && r.RetrievalComplete {
			complete = &d
			continue
		}
		if complete != nil && r.Day.Equal(complete.AddDate(0, 0, 1)) && r.TemporalComplete && r.RetrievalComplete {
			complete = &d
		}
	}
	// A provider revision can revoke completeness; consumers always read the current watermark.
	if err := tx.Exec("UPDATE seo_data_watermarks SET complete_through=?,fresh_through=?,updated_at=? WHERE source=?", complete, fresh, now, source).Error; err != nil {
		return err
	}
	if complete == nil {
		return nil
	}
	return tx.Exec("INSERT INTO seo_watermark_events(source,complete_through) VALUES(?,?) ON CONFLICT DO NOTHING", source, *complete).Error
}

// Public app routes resolve live casks/links before aliases and removed packages.
// Formula tokens occupy a different namespace and must never own these URLs.
const packageURLLookup = `SELECT p.id FROM (
 SELECT id,1 rank FROM packages WHERE kind IN ('cask','link') AND token=? AND removed_at IS NULL
 UNION ALL SELECT package_id,2 FROM package_aliases WHERE kind IN ('cask','link') AND token=?
 UNION ALL SELECT id,3 FROM packages WHERE kind IN ('cask','link') AND token=? AND removed_at IS NOT NULL
) c JOIN packages p ON p.id=c.id WHERE p.kind IN ('cask','link') ORDER BY c.rank,CASE p.kind WHEN 'cask' THEN 0 ELSE 1 END,p.id LIMIT 1`
