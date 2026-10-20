package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/service/seotune"
	"gorm.io/gorm"
)

type geoTrend struct {
	PreviousID     int64   `json:"previousId"`
	CitedRateDelta float64 `json:"citedRateDelta"`
}
type GeoSummary struct {
	ID            int64     `json:"id"`
	Site          string    `json:"site"`
	Mode          string    `json:"mode"`
	PanelID       string    `json:"panelId"`
	PanelVersion  string    `json:"panelVersion"`
	PeriodStart   time.Time `json:"periodStart"`
	PeriodEnd     time.Time `json:"periodEnd"`
	ImportedAt    time.Time `json:"importedAt"`
	PayloadHash   string    `json:"payloadHash"`
	ComparisonKey string    `json:"comparisonKey"`
	Supersedes    *int64    `json:"supersedes"`
	Counts        geoCounts `json:"counts" gorm:"serializer:json"`
	Trend         *geoTrend `json:"trend" gorm:"-"`
}
type geoRecord struct {
	GeoSummary       `gorm:"embedded"`
	ImportedBy       string               `json:"importedBy"`
	Snapshot         geoSnapshot          `json:"snapshot" gorm:"serializer:json"`
	ObservationLinks []geoObservationLink `json:"observationLinks" gorm:"-"`
}
type geoObservationLink struct {
	ObservationID string  `json:"observationId"`
	URL           *string `json:"url"`
}

// A comparison key includes the actual entry, market/languages, prompt wording and
// planned cells; merely reusing a panel name can never turn a different test into a trend.
func geoComparison(v geoSnapshot) string {
	prompts := append([]geoPrompt{}, v.Prompts...)
	sort.Slice(prompts, func(i, j int) bool { return prompts[i].ID < prompts[j].ID })
	engines := append([]geoEngine{}, v.Engines...)
	sort.Slice(engines, func(i, j int) bool { return engines[i].Key < engines[j].Key })
	cells := []string{}
	for _, c := range v.Cells {
		cells = append(cells, c.ID+"/"+c.PromptID+"/"+c.EngineKey)
	}
	sort.Strings(cells)
	raw, _ := json.Marshal([]any{v.Provider, v.SourceProject, v.PanelID, v.PanelVersion, v.Context, prompts, engines, cells, v.HashNormalization})
	return seotasks.Hash(raw)
}
func (s *Service) recordSeoGeo(ctx context.Context, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:task:handle"); err != nil {
		return nil, err
	}
	site := seobudget.Site(s.Config.WebBaseURL)
	raw, err := json.Marshal(in.Body)
	if err != nil {
		return nil, domain.Validation()
	}
	r, err := decodeGeo(raw, site, s.now())
	if err != nil {
		return nil, domain.Validation()
	}
	data, _ := json.Marshal(r.Snapshot)
	hash := seotasks.Hash(data)
	counts, _ := json.Marshal(geoAggregate(r.Snapshot))
	result, err := seotune.Receipt(ctx, s.Store.DB, seoOwner(ctx), "RecordSeoGeo", r.IdempotencyKey, "GEO observation import", in.Body, domain.CurrentOperation(ctx).DryRun, func(tx *gorm.DB) (any, any, any, error) {
		// Reuse the shared site lock before run/charge validation, including over-budget invoices.
		if err := seobudget.Lock(ctx, tx, site); err != nil {
			return nil, nil, nil, err
		}
		var old struct {
			ID          int64
			PayloadHash string
		}
		q := tx.Raw(`SELECT id,payload_hash FROM seo_geo_snapshots WHERE site=? AND provider=? AND source_project=? AND provider_run_key=? ORDER BY id DESC LIMIT 1`, site, r.Snapshot.Provider, r.Snapshot.SourceProject, r.Snapshot.ProviderRunKey).Scan(&old)
		if q.Error != nil {
			return nil, nil, nil, q.Error
		}
		var before any
		if old.ID != 0 {
			if old.PayloadHash == hash {
				result := map[string]any{"id": old.ID, "payloadHash": hash}
				return nil, result, result, nil
			}
			if r.Supersedes == nil || *r.Supersedes != old.ID {
				return nil, nil, nil, seotasks.ErrConflict
			}
			before = map[string]any{"id": old.ID, "payloadHash": old.PayloadHash}
		} else if r.Supersedes != nil {
			return nil, nil, nil, seotasks.ErrConflict
		}
		for _, charge := range r.Snapshot.Charges {
			if charge.CostUSD == nil {
				continue
			}
			if err := seobudget.Record(ctx, tx, site, seobudget.Receipt{VendorTaskID: charge.VendorTaskID, CostUSD: *charge.CostUSD, IncurredAt: charge.IncurredAt}); err != nil {
				return nil, nil, nil, err
			}
		}
		var id int64
		v := r.Snapshot
		err := tx.Raw(`INSERT INTO seo_geo_snapshots(site,provider,source_project,provider_run_key,mode,panel_id,panel_version,period_start,period_end,imported_by,payload_hash,comparison_key,supersedes,snapshot,counts)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?::jsonb,?::jsonb) RETURNING id`, site, v.Provider, v.SourceProject, v.ProviderRunKey, v.Mode, v.PanelID, v.PanelVersion, v.PeriodStart, v.PeriodEnd, seoOwner(ctx), hash, geoComparison(v), r.Supersedes, string(data), string(counts)).Scan(&id).Error
		result := map[string]any{"id": id, "payloadHash": hash}
		return before, result, result, err
	})
	if errors.Is(err, seobudget.ErrReceipt) {
		return nil, domain.Validation()
	}
	return result, mapTaskError(err)
}

const geoColumns = `id,site,mode,panel_id,panel_version,period_start,period_end,imported_at,payload_hash,comparison_key,supersedes,counts`

func (s *Service) geoTrend(ctx context.Context, v *GeoSummary) error {
	if v.Mode != "imported_observation" || v.Counts.Answered == nil || *v.Counts.Answered != v.Counts.Planned || v.Counts.Cited.Known != v.Counts.Planned {
		return nil
	}
	var prev GeoSummary
	r := s.Store.DB.WithContext(ctx).Raw(`SELECT `+geoColumns+` FROM seo_geo_snapshots g WHERE site=? AND comparison_key=? AND mode='imported_observation' AND period_end<? AND NOT EXISTS(SELECT 1 FROM seo_geo_snapshots n WHERE n.supersedes=g.id) ORDER BY period_end DESC,id DESC LIMIT 1`, v.Site, v.ComparisonKey, v.PeriodStart).Scan(&prev)
	if r.Error != nil {
		return r.Error
	}
	if prev.ID > 0 && prev.Counts.Answered != nil && *prev.Counts.Answered == prev.Counts.Planned && prev.Counts.Cited.Known == prev.Counts.Planned {
		v.Trend = &geoTrend{PreviousID: prev.ID, CitedRateDelta: float64(v.Counts.Cited.Positive)/float64(v.Counts.Cited.Known) - float64(prev.Counts.Cited.Positive)/float64(prev.Counts.Cited.Known)}
	}
	return nil
}
func (s *Service) geoOverview(ctx context.Context) (*GeoSummary, error) {
	var v GeoSummary
	r := s.Store.DB.WithContext(ctx).Raw(`SELECT `+geoColumns+` FROM seo_geo_snapshots g WHERE site=? AND NOT EXISTS(SELECT 1 FROM seo_geo_snapshots n WHERE n.supersedes=g.id) ORDER BY period_end DESC,id DESC LIMIT 1`, seobudget.Site(s.Config.WebBaseURL)).Scan(&v)
	if r.Error != nil || v.ID == 0 {
		return nil, r.Error
	}
	return &v, s.geoTrend(ctx, &v)
}
func (s *Service) readSeoGeo(ctx context.Context, name string, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:insight:view"); err != nil {
		return nil, err
	}
	site := seobudget.Site(s.Config.WebBaseURL)
	if name == "GetSeoGeo" {
		var v geoRecord
		r := s.Store.DB.WithContext(ctx).Raw(`SELECT `+geoColumns+`,imported_by,snapshot FROM seo_geo_snapshots WHERE site=? AND id=?`, site, in.ID).Scan(&v)
		if r.Error != nil {
			return nil, r.Error
		}
		if v.ID == 0 {
			return nil, fail(domain.CodeNotFound)
		}
		if err := s.geoTrend(ctx, &v.GeoSummary); err != nil {
			return nil, err
		}
		v.ObservationLinks = []geoObservationLink{}
		for _, c := range v.Snapshot.Cells {
			if c.ObservationID != nil {
				v.ObservationLinks = append(v.ObservationLinks, geoObservationLink{ObservationID: *c.ObservationID, URL: geoObservationURL(s.Config.GEOObservationBaseURL, *c.ObservationID)})
			}
		}
		return v, nil
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	records := []GeoSummary{}
	var total int64
	// A read-only repeatable-read snapshot keeps pagination totals and rows coherent.
	err = s.Store.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY").Error; err != nil {
			return err
		}
		if err := tx.Table("seo_geo_snapshots").Where("site=?", site).Count(&total).Error; err != nil {
			return err
		}
		return tx.Raw(`SELECT `+geoColumns+` FROM seo_geo_snapshots WHERE site=? ORDER BY period_end DESC,id DESC LIMIT ? OFFSET ?`, site, size, (current-1)*size).Scan(&records).Error
	})
	if err != nil {
		return nil, err
	}
	for i := range records {
		if err := s.geoTrend(ctx, &records[i]); err != nil {
			return nil, err
		}
	}
	return map[string]any{"current": current, "size": size, "total": total, "records": records}, nil
}
func geoObservationURL(base, id string) *string {
	if !geoID.MatchString(id) || !geoURL(base, "") {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" {
		return nil
	}
	result := u.JoinPath("observations", id).String()
	return &result
}
