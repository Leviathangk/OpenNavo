package seoexperiments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

var ErrSample = errors.New("SEO experiment needs a complete window and sufficient eligible impressions")
var ErrConflict = errors.New("SEO experiment scope is active or cooling down")

type Service struct {
	DB     *gorm.DB
	WebURL string
}
type Proposal struct {
	Variant     string   `json:"variant"`
	Slots       []string `json:"slots"`
	PageKind    string   `json:"pageKind"`
	Locale      string   `json:"locale"`
	BaselineCTR float64  `json:"baselineCtr"`
	TargetCTR   float64  `json:"targetCtr"`
}
type Experiment struct {
	ID                                                      int64 `json:"id"`
	Variant, PageKind, Locale, Salt, State, TemplateVersion string
	BaselineCTR, TargetCTR                                  float64
	RequiredImpressions                                     int64
	StartedAt                                               time.Time
	EndedAt, CooldownUntil                                  *time.Time
}

func (Experiment) TableName() string { return "seo_experiments" }

type candidate struct {
	ID, PackageID, Clicks, Impressions int64
	Locale, Device                     string
	Position                           float64
	Stratum                            string
	Arm                                int
}

func (s *Service) Start(ctx context.Context, tx *gorm.DB, p Proposal) (Experiment, error) {
	out := Experiment{}
	if p.PageKind != "overview" || agentgate.ValidateSnippet(p.Variant, p.Slots) != nil {
		return out, ErrSample
	}
	if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo:experiment:"+p.PageKind+":"+p.Locale).Error; err != nil {
		return out, err
	}
	var count int64
	if err := tx.Raw("SELECT count(*) FROM seo_experiments WHERE page_kind=? AND locale=? AND (state='running' OR cooldown_until>clock_timestamp())", p.PageKind, p.Locale).Scan(&count).Error; err != nil {
		return out, err
	}
	if count > 0 {
		return out, ErrConflict
	}
	var completeDays int
	if err := tx.Raw(`SELECT count(*) FROM seo_ingest_days d JOIN seo_data_watermarks w USING(source) WHERE d.source='gsc_page' AND d.day BETWEEN w.complete_through-27 AND w.complete_through AND d.temporal_complete AND d.retrieval_complete`).Scan(&completeDays).Error; err != nil {
		return out, err
	}
	if completeDays != 28 {
		return out, ErrSample
	}
	var candidates []candidate
	err := tx.Raw(`WITH metrics AS (SELECT p.url_id,p.device,sum(p.clicks) clicks,sum(p.impressions) impressions,sum(p.position_sum)/nullif(sum(p.impressions),0) position FROM seo_gsc_page_daily p JOIN seo_data_watermarks w ON w.source='gsc_page' WHERE day BETWEEN w.complete_through-27 AND w.complete_through GROUP BY p.url_id,p.device), totals AS (SELECT url_id,sum(clicks) clicks,sum(impressions) impressions,sum(position*impressions)/nullif(sum(impressions),0) position,(array_agg(device ORDER BY impressions DESC,device))[1] device FROM metrics GROUP BY url_id) SELECT u.id,u.package_id,u.locale,m.device,m.clicks,m.impressions,m.position FROM totals m JOIN seo_urls u ON u.id=m.url_id WHERE u.page_kind=? AND u.locale=? AND u.package_id IS NOT NULL AND m.impressions>=100 AND NOT EXISTS(SELECT 1 FROM seo_experiment_pages p WHERE p.url_id=u.id AND p.active) ORDER BY u.id LIMIT 5000`, p.PageKind, p.Locale).Scan(&candidates).Error
	if err != nil {
		return out, err
	}
	eligible := []candidate{}
	var clicks, impressions int64
	for _, c := range candidates {
		ok, err := s.eligible(ctx, tx, c.PackageID, p.Variant, p.Locale)
		if err != nil {
			return out, err
		}
		if !ok {
			continue
		}
		c.Stratum = fmt.Sprintf("%s:%d:%s", c.Locale, int(c.Position/5), c.Device)
		eligible = append(eligible, c)
		clicks += c.Clicks
		impressions += c.Impressions
	}
	if impressions == 0 || len(eligible) < 4 {
		return out, ErrSample
	}
	baseline := float64(clicks) / float64(impressions)
	if math.Abs(p.BaselineCTR-baseline) > 0.000001 {
		return out, ErrSample
	}
	// Reserve sufficient power for two predeclared looks, not just a single .05 test.
	required := seo.ImpressionsPerArm(baseline, p.TargetCTR)
	if required == 0 {
		return out, ErrSample
	}
	bar := (baseline + p.TargetCTR) / 2
	required = int64(math.Ceil(math.Pow(2.241403*math.Sqrt(2*bar*(1-bar))+.841621*math.Sqrt(baseline*(1-baseline)+p.TargetCTR*(1-p.TargetCTR)), 2) / ((p.TargetCTR - baseline) * (p.TargetCTR - baseline))))
	required = max(required, 1000)
	salt := uuid.NewString()
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].Stratum != eligible[j].Stratum {
			return eligible[i].Stratum < eligible[j].Stratum
		}
		return seo.ExperimentHash(salt, eligible[i].Stratum, eligible[i].ID) < seo.ExperimentHash(salt, eligible[j].Stratum, eligible[j].ID)
	})
	perStratum := map[string]int{}
	totals := [2]int64{}
	for i := range eligible {
		c := &eligible[i]
		c.Arm = perStratum[c.Stratum] % 2
		perStratum[c.Stratum]++
		totals[c.Arm] += c.Impressions
	}
	if totals[0] < required || totals[1] < required {
		return out, ErrSample
	}
	if err := tx.Raw(`INSERT INTO seo_experiments(variant,page_kind,locale,salt,baseline_ctr,target_ctr,required_impressions,state,template_version) VALUES(?,?,?,?,?,?,?,'running','snippet-v1') RETURNING *`, p.Variant, p.PageKind, p.Locale, salt, baseline, p.TargetCTR, required).Scan(&out).Error; err != nil {
		return out, err
	}
	for _, c := range eligible {
		if err := tx.Exec(`INSERT INTO seo_experiment_pages(experiment_id,url_id,stratum,arm,baseline_impressions) VALUES(?,?,?,?,?)`, out.ID, c.ID, c.Stratum, c.Arm, c.Impressions).Error; err != nil {
			return out, err
		}
	}
	return out, nil
}

// Eligibility is rechecked on every assignment read. An expired price must
// withdraw a Free title immediately even while its experiment is running.
func (s *Service) eligible(ctx context.Context, db *gorm.DB, id int64, variant, locale string) (bool, error) {
	var row struct {
		Token     string
		Version   string
		RemovedAt *time.Time
		Artifacts json.RawMessage
		Hidden    bool
	}
	r := db.WithContext(ctx).Raw(`SELECT p.token,p.version,p.removed_at,p.artifacts,coalesce(m.hidden,false) hidden FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.id=?`, id).Scan(&row)
	if r.Error != nil {
		return false, r.Error
	}
	if r.RowsAffected == 0 || row.RemovedAt != nil || row.Hidden {
		return false, nil
	}
	st := &store.Store{DB: db}
	index, found, err := st.IndexRowByToken(ctx, row.Token)
	if err != nil || !found {
		return false, err
	}
	policy, err := st.IndexPolicy(ctx)
	if err != nil {
		return false, err
	}
	decision := seo.DecidePage(index.Facts(), seo.PageRef{Kind: seo.PageKind("overview"), Locale: locale}, policy)
	if strings.Contains(decision.Robots, "noindex") {
		return false, nil
	}
	switch variant {
	case "current_version":
		return row.Version != "" && row.Version != "latest", nil
	case "verified_free":
		records, err := (&store.Store{DB: db}).FactRecords(ctx, id)
		if err != nil {
			return false, err
		}
		free := false
		for _, r := range records {
			if r.Kind != "price" {
				continue
			}
			var p facts.PriceObservation
			if json.Unmarshal(r.Value, &p) != nil || p.Validate() != nil || r.Evidence.At(time.Now().UTC()).Status != "verified" {
				return false, nil
			}
			if p.Usage != "free" || !p.DownloadFree {
				return false, nil
			}
			free = true
		}
		return free, nil
	case "uninstall_alternatives":
		var count int64
		if err := db.Raw(`SELECT count(*) FROM package_alternatives a JOIN packages p ON p.id=a.alternative_id WHERE a.package_id=? AND p.removed_at IS NULL`, id).Scan(&count).Error; err != nil {
			return false, err
		}
		return count > 0 && (strings.Contains(string(row.Artifacts), `"uninstall"`) || strings.Contains(string(row.Artifacts), `"zap"`)), nil
	}
	return false, nil
}

func (s *Service) Assignment(ctx context.Context, raw string) (map[string]any, error) {
	none := map[string]any{"experimentId": nil, "arm": "none", "variant": nil, "templateVersion": "snippet-v1", "eligible": false, "expiresAt": nil}
	u, err := url.Parse(raw)
	base, e := url.Parse(s.WebURL)
	if err != nil || e != nil || u.Scheme != "https" || u.Host != base.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return none, ErrSample
	}
	var row struct {
		ID, PackageID                    int64
		Arm                              int
		Variant, TemplateVersion, Locale string
		ExpiresAt                        time.Time
	}
	r := s.DB.WithContext(ctx).Raw(`SELECT e.id,u.package_id,p.arm,e.variant,e.template_version,u.locale,e.started_at+interval '42 days' expires_at FROM seo_experiment_pages p JOIN seo_experiments e ON e.id=p.experiment_id JOIN seo_urls u ON u.id=p.url_id WHERE u.url=? AND p.active AND e.state='running' AND e.started_at>clock_timestamp()-interval '42 days'`, raw).Scan(&row)
	if r.Error != nil {
		return nil, r.Error
	}
	if r.RowsAffected == 0 {
		return none, nil
	}
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return seobudget.Check(ctx, tx, seobudget.Site(s.WebURL), "snippet", false) }); err != nil {
		if errors.Is(err, seobudget.ErrFrozen) {
			return none, nil
		}
		return nil, err
	}
	ok, err := s.eligible(ctx, s.DB, row.PackageID, row.Variant, row.Locale)
	if err != nil {
		return nil, err
	}
	arm := "control"
	if row.Arm == 1 {
		arm = "treatment"
	}
	return map[string]any{"experimentId": row.ID, "arm": arm, "variant": row.Variant, "templateVersion": row.TemplateVersion, "eligible": ok, "expiresAt": row.ExpiresAt}, nil
}

func (s *Service) Checkpoints(ctx context.Context) (map[string]any, error) {
	count := 0
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := seobudget.Lock(ctx, tx, seobudget.Site(s.WebURL)); err != nil {
			return err
		}
		var experiments []Experiment
		if err := tx.Raw("SELECT * FROM seo_experiments WHERE state='running' ORDER BY id FOR UPDATE SKIP LOCKED").Scan(&experiments).Error; err != nil {
			return err
		}
		for _, e := range experiments {
			if err := s.checkpoint(tx, e); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return map[string]any{"checked": count}, err
}

func (s *Service) checkpoint(tx *gorm.DB, e Experiment) error {
	var now time.Time
	var watermark struct{ CompleteThrough *time.Time }
	if err := tx.Raw("SELECT clock_timestamp()").Scan(&now).Error; err != nil {
		return err
	}
	if err := tx.Raw("SELECT complete_through FROM seo_data_watermarks WHERE source='gsc_page'").Scan(&watermark).Error; err != nil {
		return err
	}
	age := int(now.Sub(e.StartedAt).Hours() / 24)
	look := 14
	if age >= 28 {
		look = 28
	}
	if age >= 42 {
		look = 42
	}
	if age < 14 {
		return nil
	}
	if look < 42 && (watermark.CompleteThrough == nil || watermark.CompleteThrough.Before(e.StartedAt.AddDate(0, 0, look).Truncate(24*time.Hour))) {
		return nil
	}
	var prior int64
	if err := tx.Raw("SELECT count(*) FROM seo_experiment_checkpoints WHERE experiment_id=? AND checkpoint=?", e.ID, look).Scan(&prior).Error; err != nil {
		return err
	}
	if prior > 0 {
		return nil
	}
	if err := tx.Exec(`UPDATE seo_experiment_pages p SET recrawled_at=s.last_crawl_at FROM seo_url_status s WHERE p.experiment_id=? AND p.url_id=s.url_id AND p.recrawled_at IS NULL AND s.last_crawl_at>=?`, e.ID, e.StartedAt).Error; err != nil {
		return err
	}
	var crawl struct {
		Missing int
		From    *time.Time
	}
	if err := tx.Raw("SELECT count(*) FILTER(WHERE recrawled_at IS NULL) missing,max(recrawled_at) AS \"from\" FROM seo_experiment_pages WHERE experiment_id=?", e.ID).Scan(&crawl).Error; err != nil {
		return err
	}
	type metric struct {
		Arm                 int
		Clicks, Impressions int64
	}
	metrics := []metric{}
	outcome := "continue"
	if look == 42 {
		outcome = "inconclusive"
	} else if crawl.Missing == 0 && crawl.From != nil {
		from := crawl.From.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
		through := *watermark.CompleteThrough
		expected := int(through.Sub(from).Hours()/24) + 1
		var complete int
		if err := tx.Raw("SELECT count(*) FROM seo_ingest_days WHERE source='gsc_page' AND day BETWEEN ?::date AND ?::date AND temporal_complete AND retrieval_complete", from, through).Scan(&complete).Error; err != nil {
			return err
		}
		if expected > 0 && complete == expected {
			if err := tx.Raw(`SELECT p.arm,coalesce(sum(g.clicks),0) clicks,coalesce(sum(g.impressions),0) impressions FROM seo_experiment_pages p LEFT JOIN seo_gsc_page_daily g ON g.url_id=p.url_id AND g.day BETWEEN ?::date AND ?::date WHERE p.experiment_id=? GROUP BY p.arm ORDER BY p.arm`, from, through, e.ID).Scan(&metrics).Error; err != nil {
				return err
			}
			if len(metrics) == 2 {
				outcome = seo.ExperimentOutcome(metrics[0].Clicks, metrics[0].Impressions, metrics[1].Clicks, metrics[1].Impressions, e.RequiredImpressions)
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"arms": metrics, "recrawlMissing": crawl.Missing, "measuredFrom": crawl.From})
	if err := tx.Exec("INSERT INTO seo_experiment_checkpoints(experiment_id,checkpoint,complete_through,metrics,outcome) VALUES(?,?,?,?::jsonb,?)", e.ID, look, watermark.CompleteThrough, string(raw), outcome).Error; err != nil {
		return err
	}
	if outcome == "continue" {
		return nil
	}
	if err := tx.Exec("UPDATE seo_experiments SET state=?,ended_at=clock_timestamp(),cooldown_until=clock_timestamp()+interval '28 days' WHERE id=?", outcome, e.ID).Error; err != nil {
		return err
	}
	if err := tx.Exec("UPDATE seo_experiment_pages SET active=false WHERE experiment_id=?", e.ID).Error; err != nil {
		return err
	}
	return nil
}
