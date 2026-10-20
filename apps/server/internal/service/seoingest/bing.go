package seoingest

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"
)

func (s *Service) BingStats(ctx context.Context) (map[string]any, error) {
	client, err := s.Bing(ctx)
	if errors.Is(err, ErrDisabled) {
		return map[string]any{"disabled": true}, nil
	}
	if err != nil {
		return nil, err
	}
	fetched := s.now()
	result, err := client.Read(ctx)
	if err != nil {
		return nil, err
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSource(tx, "bing"); err != nil {
			return err
		}
		var previous struct{ UpdatedAt time.Time }
		if err := tx.Table("seo_data_watermarks").Where("source='bing'").Take(&previous).Error; err != nil {
			return err
		}
		if previous.UpdatedAt.After(fetched) {
			return nil
		}
		fresh := ""
		for _, r := range result.Rows {
			if _, err := parseDay(r.Day); err != nil || r.Value == "" || r.Clicks < 0 || r.Impressions < r.Clicks || r.Position < 0 || math.IsNaN(r.Position) || math.IsInf(r.Position, 0) {
				return ErrUpstream
			}
			if err := tx.Exec(`INSERT INTO seo_bing_daily(day,dimension,value,clicks,impressions,position_sum) VALUES(?,?,?,?,?,?) ON CONFLICT(day,dimension,value) DO UPDATE SET clicks=EXCLUDED.clicks,impressions=EXCLUDED.impressions,position_sum=EXCLUDED.position_sum`, r.Day, r.Dimension, r.Value, r.Clicks, r.Impressions, r.Position*float64(r.Impressions)).Error; err != nil {
				return err
			}
			if r.Dimension == "page" {
				if _, err := s.registerURL(tx, r.Value); err != nil {
					return err
				}
			}
			if r.Day > fresh {
				fresh = r.Day
			}
		}
		for d, stats := range result.Crawl {
			if _, err := parseDay(d); err != nil {
				return ErrUpstream
			}
			raw, err := json.Marshal(stats)
			if err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO seo_bing_crawl_daily(day,stats) VALUES(?,?::jsonb) ON CONFLICT(day) DO UPDATE SET stats=EXCLUDED.stats`, d, string(raw)).Error; err != nil {
				return err
			}
			if d > fresh {
				fresh = d
			}
		}
		var date *string
		if fresh != "" {
			date = &fresh
		}
		return tx.Exec(`UPDATE seo_data_watermarks SET complete_through=?,fresh_through=GREATEST(fresh_through,?::date),updated_at=? WHERE source='bing'`, result.CompleteThrough, date, fetched).Error
	})
	return map[string]any{"rows": len(result.Rows), "coverage": "provider_observations"}, err
}
