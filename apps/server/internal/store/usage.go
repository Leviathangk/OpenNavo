package store

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

// The reporting clock is fixed and shared with the clients, independent of server TZ.
var usageZone = time.FixedZone("Asia/Shanghai", 8*60*60)
var usageVersion = regexp.MustCompile(`^[0-9A-Za-z.+_-]{0,64}$`)
var usageCountry = regexp.MustCompile(`^[A-Z]{2}$`)
var usageLocale = regexp.MustCompile(`^[A-Za-z0-9-]{2,35}$`)

type UsageActivity struct {
	AnonymousID string
	Platform    string
	OccurredAt  time.Time
	Version     string
	Locale      string
	Country     string
}

func (a UsageActivity) Validate(now time.Time) error {
	id, err := uuid.Parse(a.AnonymousID)
	if err != nil || id == uuid.Nil || len(a.AnonymousID) != 36 || (a.Platform != "web" && a.Platform != "desktop") || !usageVersion.MatchString(a.Version) || !usageLocale.MatchString(a.Locale) || a.OccurredAt.Before(now.Add(-7*24*time.Hour)) || a.OccurredAt.After(now.Add(5*time.Minute)) || (a.Platform == "desktop" && a.Version == "") {
		return domain.Validation()
	}
	return nil
}

// ReportingTime is the timestamp actually persisted. Clients must acknowledge its
// Shanghai date, not the requested date, when their clock runs across midnight.
func (a UsageActivity) ReportingTime(now time.Time) time.Time {
	if a.OccurredAt.After(now) {
		return now
	}
	return a.OccurredAt
}

func (a UsageActivity) ReportingDay(now time.Time) string {
	return a.ReportingTime(now).In(usageZone).Format("2006-01-02")
}

func (s *Store) RecordUsage(ctx context.Context, a UsageActivity, now time.Time) error {
	if err := a.Validate(now); err != nil {
		return err
	}
	// Allow small clock skew, but never create a future reporting day.
	at := a.ReportingTime(now)
	if a.Country == "" {
		a.Country = "ZZ"
	}
	if !usageCountry.MatchString(a.Country) {
		return domain.Validation()
	}
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO usage_identities(platform,anonymous_id,first_seen_at,last_seen_at) VALUES(?,?,?,?)
   ON CONFLICT(platform,anonymous_id) DO UPDATE SET first_seen_at=LEAST(usage_identities.first_seen_at,EXCLUDED.first_seen_at),last_seen_at=GREATEST(usage_identities.last_seen_at,EXCLUDED.last_seen_at)`, a.Platform, a.AnonymousID, at, at).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO usage_daily(day,platform,anonymous_id,last_active_at,version,locale,country) VALUES(?::date,?,?,?,?,?,?)
   ON CONFLICT(day,platform,anonymous_id) DO UPDATE SET last_active_at=EXCLUDED.last_active_at,version=EXCLUDED.version,locale=EXCLUDED.locale,country=EXCLUDED.country
   WHERE EXCLUDED.last_active_at>usage_daily.last_active_at`, at.In(usageZone).Format("2006-01-02"), a.Platform, a.AnonymousID, at, a.Version, a.Locale, a.Country).Error
	})
}

type UsageDay struct {
	Date       string `json:"date"`
	Web        int64  `json:"web"`
	Desktop    int64  `json:"desktop"`
	NewWeb     int64  `json:"newWeb"`
	NewDesktop int64  `json:"newDesktop"`
}
type UsageVersion struct {
	Version string `json:"version"`
	Count   int64  `json:"count"`
}
type UsageCountry struct {
	Country string `json:"country"`
	Web     int64  `json:"web"`
	Desktop int64  `json:"desktop"`
}
type UsageOverview struct {
	Countries []UsageCountry `json:"countries"`
	TimeZone  string         `json:"timeZone"`
	AsOf      time.Time      `json:"asOf"`
	Days      []UsageDay     `json:"days"`
	Versions  []UsageVersion `json:"versions"`
}

func (s *Store) UsageOverview(ctx context.Context, now time.Time) (UsageOverview, error) {
	today := now.In(usageZone).Format("2006-01-02")
	result := UsageOverview{TimeZone: "Asia/Shanghai", AsOf: now.UTC(), Days: []UsageDay{}, Versions: []UsageVersion{}, Countries: []UsageCountry{}}
	err := s.DB.WithContext(ctx).Raw(`WITH days AS (SELECT generate_series(?::date-29,?::date,interval '1 day')::date AS day),
 active AS (SELECT day,count(*) FILTER(WHERE platform='web') web,count(*) FILTER(WHERE platform='desktop') desktop FROM usage_daily WHERE day BETWEEN ?::date-29 AND ?::date GROUP BY day),
 fresh AS (SELECT (first_seen_at AT TIME ZONE 'Asia/Shanghai')::date AS day,count(*) FILTER(WHERE platform='web') new_web,count(*) FILTER(WHERE platform='desktop') new_desktop FROM usage_identities WHERE first_seen_at >= ((?::date-29)::timestamp AT TIME ZONE 'Asia/Shanghai') AND first_seen_at < ((?::date+1)::timestamp AT TIME ZONE 'Asia/Shanghai') GROUP BY 1)
 SELECT to_char(d.day,'YYYY-MM-DD') AS date,COALESCE(a.web,0) web,COALESCE(a.desktop,0) desktop,COALESCE(f.new_web,0) new_web,COALESCE(f.new_desktop,0) new_desktop FROM days d LEFT JOIN active a USING(day) LEFT JOIN fresh f USING(day) ORDER BY d.day`, today, today, today, today, today, today).Scan(&result.Days).Error
	if err != nil {
		return result, err
	}
	err = s.DB.WithContext(ctx).Raw(`SELECT version,count(*) count FROM usage_daily WHERE day=?::date AND platform='desktop' GROUP BY version ORDER BY count DESC,version`, today).Scan(&result.Versions).Error
	if err != nil {
		return result, err
	}
	err = s.DB.WithContext(ctx).Raw(`SELECT country,count(*) FILTER(WHERE platform='web') web,count(*) FILTER(WHERE platform='desktop') desktop FROM usage_daily WHERE day=?::date GROUP BY country ORDER BY count(*) DESC,country`, today).Scan(&result.Countries).Error
	return result, err
}
