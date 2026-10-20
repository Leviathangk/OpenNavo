package seokeywords

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

type Observation struct {
	Keyword      string    `json:"keyword"`
	Source       string    `json:"source"`
	Engine       string    `json:"engine"`
	Country      string    `json:"country"`
	Language     string    `json:"language"`
	Device       string    `json:"device"`
	Period       string    `json:"period"`
	SearchVolume *int64    `json:"searchVolume"`
	VolumeState  string    `json:"volumeState"`
	Difficulty   *float64  `json:"difficulty"`
	OurPosition  *float64  `json:"ourPosition"`
	SERPFeatures []string  `json:"serpFeatures"`
	VendorTaskID *string   `json:"vendorTaskId"`
	CostUSD      *string   `json:"costUsd"`
	ObservedAt   time.Time `json:"observedAt"`
}
type Target struct {
	EntityKey string `json:"entityKey"`
	Intent    string `json:"intent"`
	Locale    string `json:"locale"`
	TargetURL string `json:"targetUrl"`
}
type Request struct {
	Records []Observation `json:"records"`
	Targets []Target      `json:"targets"`
}
type Service struct {
	DB     *gorm.DB
	WebURL string
	Now    func() time.Time
}

var money = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})(\.[0-9]{1,4})?$`)
var entity = regexp.MustCompile(`^(pkg:[1-9][0-9]*|topic:[a-z0-9][a-z0-9-]*)$`)
var datePeriod = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}(-[0-9]{2})?$`)
var errPreview = errors.New("SEO keyword preview rollback")

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func validate(o *Observation, now time.Time) error {
	o.Keyword = strings.TrimSpace(o.Keyword)
	o.Country = strings.ToUpper(o.Country)
	if Normalize(o.Keyword) == "" {
		return domain.Validation()
	}
	if o.Keyword == "" || len(o.Keyword) > 1000 || strings.IndexFunc(o.Keyword, unicode.IsControl) >= 0 || !i18n.Valid(o.Language) || !slices.Contains([]string{"openseo:dataforseo", "gsc", "bing", "site_search", "homebrew"}, o.Source) || !slices.Contains([]string{"google", "bing", "site", "homebrew"}, o.Engine) || !slices.Contains([]string{"all", "desktop", "mobile", "tablet"}, o.Device) || !datePeriod.MatchString(o.Period) || len(o.Country) < 2 || len(o.Country) > 3 || o.ObservedAt.IsZero() || o.ObservedAt.After(now.Add(5*time.Minute)) {
		return domain.Validation()
	}
	for _, r := range o.Country {
		if r < 'A' || r > 'Z' {
			return domain.Validation()
		}
	}
	layout := "2006-01"
	if len(o.Period) == 10 {
		layout = "2006-01-02"
	}
	if _, err := time.Parse(layout, o.Period); err != nil {
		return domain.Validation()
	}
	if !slices.Contains([]string{"known", "unknown", "unsupported"}, o.VolumeState) {
		return domain.Validation()
	}
	if o.Source == "openseo:dataforseo" {
		if o.Country == "CN" || o.Country == "CHN" {
			return domain.Validation()
		}
		if o.Language == "ru-RU" || o.Country == "RU" || o.Country == "RUS" {
			o.VolumeState = "unsupported"
			o.SearchVolume = nil
		}
		if o.VolumeState != "unsupported" && (o.VendorTaskID == nil || strings.TrimSpace(*o.VendorTaskID) == "" || len(*o.VendorTaskID) > 200 || o.CostUSD == nil) {
			return domain.Validation()
		}
	}
	if (o.VolumeState == "known") != (o.SearchVolume != nil) || o.SearchVolume != nil && *o.SearchVolume < 0 || o.CostUSD != nil && !money.MatchString(*o.CostUSD) {
		return domain.Validation()
	}
	for _, v := range []*float64{o.Difficulty, o.OurPosition} {
		if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return domain.Validation()
		}
	}
	if o.Difficulty != nil && *o.Difficulty > 100 || len(o.SERPFeatures) > 30 {
		return domain.Validation()
	}
	for _, f := range o.SERPFeatures {
		if len(f) > 100 {
			return domain.Validation()
		}
	}
	return nil
}

func (s *Service) ReserveTargets(ctx context.Context, tx *gorm.DB, targets []Target) error {
	base, err := url.Parse(s.WebURL)
	if err != nil {
		return domain.Validation()
	}
	// Stable lock order prevents deadlocks when two batches reserve overlapping targets.
	slices.SortFunc(targets, func(a, b Target) int {
		return strings.Compare(a.EntityKey+":"+a.Intent+":"+a.Locale, b.EntityKey+":"+b.Intent+":"+b.Locale)
	})
	for _, t := range targets {
		u, err := url.Parse(t.TargetURL)
		if !entity.MatchString(t.EntityKey) || !i18n.Valid(t.Locale) || !slices.Contains([]string{"download", "install", "brew", "uninstall", "alternative", "price", "version", "requirements", "compare", "topic"}, t.Intent) || err != nil || u.Scheme != "https" || u.Host != base.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(t.TargetURL) > 2048 {
			return domain.Validation()
		}
		actualLocale := "en-US"
		for _, code := range i18n.SiteLocales {
			p := i18n.Metadata[code].Prefix
			if p != "" && strings.HasPrefix(u.Path, p+"/") {
				actualLocale = string(code)
				break
			}
		}
		_, blocked := Intent(t.EntityKey + " " + u.Path)
		if actualLocale != t.Locale || path.Clean(u.Path) != u.Path || blocked {
			return domain.Validation()
		}
		prefix := i18n.Metadata[i18n.LocaleCode(t.Locale)].Prefix + "/"
		if !strings.HasPrefix(u.Path, prefix) {
			return domain.Validation()
		}
		if strings.HasPrefix(t.EntityKey, "pkg:") {
			id, err := strconv.ParseInt(strings.TrimPrefix(t.EntityKey, "pkg:"), 10, 64)
			if err != nil {
				return domain.Validation()
			}
			var n int64
			if err := tx.WithContext(ctx).Table("packages").Where("id=? AND removed_at IS NULL", id).Count(&n).Error; err != nil {
				return err
			}
			if n != 1 {
				return domain.Validation()
			}
		}
		var saved string
		if err := tx.WithContext(ctx).Raw(`INSERT INTO seo_targets(entity_key,intent,locale,target_url) VALUES(?,?,?,?) ON CONFLICT(entity_key,intent,locale) DO NOTHING RETURNING target_url`, t.EntityKey, t.Intent, t.Locale, t.TargetURL).Scan(&saved).Error; err != nil {
			return err
		}
		if saved == "" {
			if err := tx.WithContext(ctx).Raw("SELECT target_url FROM seo_targets WHERE entity_key=? AND intent=? AND locale=?", t.EntityKey, t.Intent, t.Locale).Scan(&saved).Error; err != nil {
				return err
			}
		}
		if saved != t.TargetURL {
			return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: 409}
		}
	}
	return nil
}

func (s *Service) Upsert(ctx context.Context, in Request, dry bool, actor store.AuditActor) (map[string]any, error) {
	if len(in.Records) < 1 || len(in.Records) > 100 || len(in.Targets) > 100 {
		return nil, domain.Validation()
	}
	for i := range in.Records {
		if err := validate(&in.Records[i], s.now()); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"records": []any{}, "targets": in.Targets}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.ReserveTargets(ctx, tx, in.Targets); err != nil {
			return err
		}
		var entities []struct {
			ID   int64
			Name string
		}
		if err := tx.Raw(`SELECT id,name FROM packages WHERE kind IN ('cask','link') AND removed_at IS NULL UNION SELECT id,token FROM packages WHERE kind IN ('cask','link') AND removed_at IS NULL UNION SELECT p.id,unnest(p.names) FROM packages p WHERE p.kind IN ('cask','link') AND p.removed_at IS NULL UNION SELECT package_id,token FROM package_aliases UNION SELECT package_id,display_name FROM package_i18n WHERE display_name IS NOT NULL AND status IN ('source','machine','manual') UNION SELECT package_id,unnest(bundle_ids) FROM link_apps`).Scan(&entities).Error; err != nil {
			return err
		}
		for _, o := range in.Records {
			if o.Source == "openseo:dataforseo" && o.VendorTaskID != nil && o.CostUSD != nil {
				if err := seobudget.Record(ctx, tx, seobudget.Site(s.WebURL), seobudget.Receipt{VendorTaskID: *o.VendorTaskID, CostUSD: *o.CostUSD, IncurredAt: o.ObservedAt}); err != nil {
					return err
				}
			}
			intent, blocked := Intent(o.Keyword)
			anomaly := ""
			if blocked {
				anomaly = "piracy"
			}
			features, err := json.Marshal(o.SERPFeatures)
			if err != nil {
				return err
			}
			if string(features) == "null" {
				features = []byte("[]")
			}
			var id int64
			if err := tx.Raw(`INSERT INTO seo_keywords(keyword,normalized,source,engine,country,language,device,period,search_volume,volume_state,difficulty,our_position,serp_features,vendor_task_id,cost_usd,anomaly,observed_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?,?,nullif(?,''),?)
ON CONFLICT(normalized,source,engine,country,language,device,period) DO UPDATE SET keyword=EXCLUDED.keyword,search_volume=EXCLUDED.search_volume,volume_state=EXCLUDED.volume_state,difficulty=EXCLUDED.difficulty,our_position=EXCLUDED.our_position,serp_features=EXCLUDED.serp_features,vendor_task_id=EXCLUDED.vendor_task_id,cost_usd=EXCLUDED.cost_usd,anomaly=EXCLUDED.anomaly,observed_at=EXCLUDED.observed_at
WHERE seo_keywords.observed_at<=EXCLUDED.observed_at AND (seo_keywords.keyword,seo_keywords.search_volume,seo_keywords.volume_state,seo_keywords.difficulty,seo_keywords.our_position,seo_keywords.serp_features,seo_keywords.vendor_task_id,seo_keywords.cost_usd,seo_keywords.anomaly,seo_keywords.observed_at) IS DISTINCT FROM (EXCLUDED.keyword,EXCLUDED.search_volume,EXCLUDED.volume_state,EXCLUDED.difficulty,EXCLUDED.our_position,EXCLUDED.serp_features,EXCLUDED.vendor_task_id,EXCLUDED.cost_usd,EXCLUDED.anomaly,EXCLUDED.observed_at) RETURNING id`, o.Keyword, Normalize(o.Keyword), o.Source, o.Engine, o.Country, o.Language, o.Device, o.Period, o.SearchVolume, o.VolumeState, o.Difficulty, o.OurPosition, string(features), o.VendorTaskID, o.CostUSD, anomaly, o.ObservedAt).Scan(&id).Error; err != nil {
				return err
			}
			if id == 0 {
				if err := tx.Raw(`SELECT id FROM seo_keywords WHERE normalized=? AND source=? AND engine=? AND country=? AND language=? AND device=? AND period=?`, Normalize(o.Keyword), o.Source, o.Engine, o.Country, o.Language, o.Device, o.Period).Scan(&id).Error; err != nil {
					return err
				}
			}
			if !blocked {
				seen := map[int64]bool{}
				for _, e := range entities {
					if seen[e.ID] || len([]rune(Normalize(e.Name))) < 2 || !containsTerm(o.Keyword, e.Name) {
						continue
					}
					seen[e.ID] = true
					if err := tx.Exec(`INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES(?,?,?,1,'confirmed') ON CONFLICT DO NOTHING`, id, e.ID, intent).Error; err != nil {
						return err
					}
				}
			}
			record, err := readRecord(tx, id)
			if err != nil {
				return err
			}
			result["records"] = append(result["records"].([]any), record)
		}
		if dry {
			return errPreview
		}
		return store.Audit(ctx, tx, actor, "UpsertSeoKeywords", "seo_keyword", "batch", nil, result)
	})
	if errors.Is(err, errPreview) {
		urls := []string{}
		for _, target := range in.Targets {
			urls = append(urls, target.TargetURL)
		}
		return map[string]any{"dryRun": true, "requestId": requestID(ctx), "changes": []any{}, "affectedUrls": urls}, nil
	}
	return result, err
}
func requestID(ctx context.Context) string {
	if op := domain.CurrentOperation(ctx); op != nil {
		return op.RequestID
	}
	return ""
}
func readRecord(tx *gorm.DB, id int64) (map[string]any, error) {
	var raw string
	err := tx.Raw(`SELECT jsonb_build_object('id',id,'keyword',keyword,'normalized',normalized,'source',source,'engine',engine,'country',country,'language',language,'device',device,'period',period,'searchVolume',search_volume,'volumeState',volume_state,'difficulty',difficulty,'ourPosition',our_position,'serpFeatures',serp_features,'vendorTaskId',vendor_task_id,'costUsd',cost_usd::text,'anomaly',anomaly,'observedAt',observed_at,'links',coalesce((SELECT jsonb_agg(jsonb_build_object('packageId',package_id,'intent',intent,'confidence',confidence,'state',state)) FROM seo_keyword_links l WHERE l.keyword_id=k.id),'[]'::jsonb))::text FROM seo_keywords k WHERE id=?`, id).Scan(&raw).Error
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (s *Service) List(ctx context.Context, language, query string, current, size int) (map[string]any, error) {
	if current < 1 || current > 100000 || size < 1 || size > 100 || language != "" && !i18n.Valid(language) {
		return nil, domain.Validation()
	}
	q := s.DB.WithContext(ctx).Table("seo_keywords")
	if language != "" {
		q = q.Where("language=?", language)
	}
	if query != "" {
		q = q.Where("position(? in normalized)>0", Normalize(query))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var ids []int64
	if err := q.Order("observed_at DESC,id").Offset((current-1)*size).Limit(size).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	records := []any{}
	for _, id := range ids {
		r, err := readRecord(s.DB.WithContext(ctx), id)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return map[string]any{"current": current, "size": size, "total": total, "records": records}, nil
}
