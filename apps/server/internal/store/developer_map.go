package store

import (
	"context"
	"time"
)

// DeveloperTop is one developer inside a city card.
type DeveloperTop struct {
	Name        string `json:"name" gorm:"column:name"`
	Apps        int    `json:"apps" gorm:"column:apps"`
	Installs30d int64  `json:"installs30d" gorm:"column:installs30d"`
}

// DeveloperPlace is a city (or country-only fallback) aggregate for the map.
type DeveloperPlace struct {
	Key           string         `json:"key"`
	Kind          string         `json:"kind"` // city | country
	Name          string         `json:"name"`
	CountryCode   string         `json:"countryCode"`
	Region        string         `json:"region"`
	Lat           float64        `json:"lat"`
	Lng           float64        `json:"lng"`
	Developers    int            `json:"developers"`
	Apps          int            `json:"apps"`
	Installs30d   int64          `json:"installs30d"`
	TopDevelopers []DeveloperTop `json:"topDevelopers"`
}

// DeveloperCountry is the country rollup used by the map's right rail.
type DeveloperCountry struct {
	Code        string `json:"code" gorm:"column:code"`
	Developers  int    `json:"developers" gorm:"column:developers"`
	Apps        int    `json:"apps" gorm:"column:apps"`
	Installs30d int64  `json:"installs30d" gorm:"column:installs30d"`
}

// DeveloperRecent is one recently approved location.
type DeveloperRecent struct {
	Name        string    `json:"name" gorm:"column:name"`
	CityKey     string    `json:"cityKey" gorm:"column:city_key"`
	City        string    `json:"city" gorm:"column:city"`
	CountryCode string    `json:"countryCode" gorm:"column:country_code"`
	Apps        int       `json:"apps" gorm:"column:apps"`
	UpdatedAt   time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

// DeveloperPick is the package + quote featured above Hot Apps.
type DeveloperPick struct {
	PackageID   int64     `gorm:"column:package_id"`
	Quote       string    `gorm:"column:quote"`
	PublishedAt time.Time `gorm:"column:published_at"`
}

// DeveloperTotals are the header numbers for /developer-map.
type DeveloperTotals struct {
	TotalDevelopers   int `json:"totalDevelopers" gorm:"column:total_developers"`
	LocatedDevelopers int `json:"locatedDevelopers" gorm:"column:located_developers"`
	Apps              int `json:"apps" gorm:"column:apps"`
	Places            int `json:"places" gorm:"column:places"`
	Countries         int `json:"countries" gorm:"column:countries"`
}

func developerKindFilter(includeLinks bool) string {
	if includeLinks {
		return "p.kind IN ('cask','link')"
	}
	return "p.kind='cask'"
}

// developerPlaceKey keeps same-named cities in different countries apart.
func developerPlaceKey(countryCode, cityKey string) string {
	return countryCode + "|" + cityKey
}

// DeveloperMapPlaces returns approved city aggregates with their top developers.
func (s *Store) DeveloperMapPlaces(ctx context.Context, includeLinks bool) ([]DeveloperPlace, error) {
	kindFilter := developerKindFilter(includeLinks)
	var rows []struct {
		CityKey     string  `gorm:"column:city_key"`
		City        string  `gorm:"column:city"`
		CountryCode string  `gorm:"column:country_code"`
		Region      string  `gorm:"column:region"`
		Lat         float64 `gorm:"column:lat"`
		Lng         float64 `gorm:"column:lng"`
		Apps        int     `gorm:"column:apps"`
		Developers  int     `gorm:"column:developers"`
		Installs30d int64   `gorm:"column:installs30d"`
	}
	err := s.DB.WithContext(ctx).Raw(`
		SELECT COALESCE(pl.city_key, '') AS city_key,
		       COALESCE(pl.city, '') AS city,
		       pl.country_code,
		       pl.region,
		       COALESCE(pl.lat, 0) AS lat,
		       COALESCE(pl.lng, 0) AS lng,
		       count(*) AS apps,
		       count(DISTINCT NULLIF(m.developer, '')) AS developers,
		       COALESCE(sum(p.installs_30d), 0)::bigint AS installs30d
		FROM package_locations pl
		JOIN packages p ON p.id = pl.package_id
		LEFT JOIN package_meta m ON m.package_id = p.id
		WHERE pl.status = 'approved'
		  AND p.removed_at IS NULL
		  AND NOT (p.disabled OR p.stale_disabled)
		  AND NOT COALESCE(m.hidden, false)
		  AND NOT COALESCE(m.private, false)
		  AND ` + kindFilter + `
		GROUP BY pl.city_key, pl.city, pl.country_code, pl.region, pl.lat, pl.lng
		ORDER BY developers DESC, installs30d DESC, city_key
	`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	var topRows []struct {
		CityKey     string `gorm:"column:city_key"`
		CountryCode string `gorm:"column:country_code"`
		Developer   string `gorm:"column:developer"`
		Apps        int    `gorm:"column:apps"`
		Installs30d int64  `gorm:"column:installs30d"`
	}
	err = s.DB.WithContext(ctx).Raw(`
		SELECT COALESCE(pl.city_key, '') AS city_key,
		       pl.country_code,
		       m.developer AS developer,
		       count(*) AS apps,
		       COALESCE(sum(p.installs_30d), 0)::bigint AS installs30d
		FROM package_locations pl
		JOIN packages p ON p.id = pl.package_id
		JOIN package_meta m ON m.package_id = p.id
		WHERE pl.status = 'approved'
		  AND p.removed_at IS NULL
		  AND NOT (p.disabled OR p.stale_disabled)
		  AND NOT COALESCE(m.hidden, false)
		  AND NOT COALESCE(m.private, false)
		  AND NULLIF(m.developer, '') IS NOT NULL
		  AND ` + kindFilter + `
		GROUP BY pl.country_code, pl.city_key, m.developer
		ORDER BY installs30d DESC, developer
	`).Scan(&topRows).Error
	if err != nil {
		return nil, err
	}
	topByCity := make(map[string][]DeveloperTop)
	for _, row := range topRows {
		key := developerPlaceKey(row.CountryCode, row.CityKey)
		items := topByCity[key]
		if len(items) >= 3 {
			continue
		}
		topByCity[key] = append(items, DeveloperTop{Name: row.Developer, Apps: row.Apps, Installs30d: row.Installs30d})
	}

	places := make([]DeveloperPlace, 0, len(rows))
	for _, row := range rows {
		kind := "city"
		key := row.CityKey
		name := row.City
		if key == "" {
			kind = "country"
			key = row.CountryCode
			name = row.CountryCode
		}
		top := topByCity[developerPlaceKey(row.CountryCode, row.CityKey)]
		if top == nil {
			top = []DeveloperTop{}
		}
		places = append(places, DeveloperPlace{
			Key:           key,
			Kind:          kind,
			Name:          name,
			CountryCode:   row.CountryCode,
			Region:        row.Region,
			Lat:           row.Lat,
			Lng:           row.Lng,
			Developers:    row.Developers,
			Apps:          row.Apps,
			Installs30d:   row.Installs30d,
			TopDevelopers: top,
		})
	}
	return places, nil
}

// DeveloperMapCountries returns the country rollup.
func (s *Store) DeveloperMapCountries(ctx context.Context, includeLinks bool) ([]DeveloperCountry, error) {
	kindFilter := developerKindFilter(includeLinks)
	var rows []DeveloperCountry
	err := s.DB.WithContext(ctx).Raw(`
		SELECT pl.country_code AS code,
		       count(DISTINCT NULLIF(m.developer, '')) AS developers,
		       count(*) AS apps,
		       COALESCE(sum(p.installs_30d), 0)::bigint AS installs30d
		FROM package_locations pl
		JOIN packages p ON p.id = pl.package_id
		LEFT JOIN package_meta m ON m.package_id = p.id
		WHERE pl.status = 'approved'
		  AND p.removed_at IS NULL
		  AND NOT (p.disabled OR p.stale_disabled)
		  AND NOT COALESCE(m.hidden, false)
		  AND NOT COALESCE(m.private, false)
		  AND ` + kindFilter + `
		GROUP BY pl.country_code
		ORDER BY developers DESC, installs30d DESC, code
	`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// DeveloperMapRecent returns the most recently approved locations.
func (s *Store) DeveloperMapRecent(ctx context.Context, limit int, includeLinks bool) ([]DeveloperRecent, error) {
	kindFilter := developerKindFilter(includeLinks)
	if limit < 1 || limit > 20 {
		limit = 4
	}
	var rows []DeveloperRecent
	err := s.DB.WithContext(ctx).Raw(`
		SELECT COALESCE(NULLIF(m.developer, ''), p.name) AS name,
		       COALESCE(pl.city_key, '') AS city_key,
		       COALESCE(pl.city, '') AS city,
		       pl.country_code,
		       count(*) AS apps,
		       max(pl.updated_at) AS updated_at
		FROM package_locations pl
		JOIN packages p ON p.id = pl.package_id
		LEFT JOIN package_meta m ON m.package_id = p.id
		WHERE pl.status = 'approved'
		  AND p.removed_at IS NULL
		  AND NOT (p.disabled OR p.stale_disabled)
		  AND NOT COALESCE(m.hidden, false)
		  AND NOT COALESCE(m.private, false)
		  AND `+kindFilter+`
		GROUP BY 1, 2, 3, 4
		ORDER BY updated_at DESC
		LIMIT ?
	`, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// DeveloperMapTotals returns the header numbers.
func (s *Store) DeveloperMapTotals(ctx context.Context, includeLinks bool) (DeveloperTotals, error) {
	kindFilter := developerKindFilter(includeLinks)
	var totals DeveloperTotals
	err := s.DB.WithContext(ctx).Raw(`
		SELECT
		  (SELECT count(DISTINCT NULLIF(m.developer, ''))
		     FROM packages p LEFT JOIN package_meta m ON m.package_id = p.id
		    WHERE p.removed_at IS NULL
		      AND NOT (p.disabled OR p.stale_disabled)
		      AND NOT COALESCE(m.hidden, false)
		      AND NOT COALESCE(m.private, false)
		      AND ` + kindFilter + `) AS total_developers,
		  (SELECT count(DISTINCT NULLIF(m.developer, ''))
		     FROM package_locations pl
		     JOIN packages p ON p.id = pl.package_id
		     LEFT JOIN package_meta m ON m.package_id = p.id
		    WHERE pl.status = 'approved'
		      AND p.removed_at IS NULL
		      AND NOT (p.disabled OR p.stale_disabled)
		      AND NOT COALESCE(m.hidden, false)
		      AND NOT COALESCE(m.private, false)
		      AND ` + kindFilter + `) AS located_developers,
		  (SELECT count(*)
		     FROM package_locations pl
		     JOIN packages p ON p.id = pl.package_id
		     LEFT JOIN package_meta m ON m.package_id = p.id
		    WHERE pl.status = 'approved'
		      AND p.removed_at IS NULL
		      AND NOT (p.disabled OR p.stale_disabled)
		      AND NOT COALESCE(m.hidden, false)
		      AND NOT COALESCE(m.private, false)
		      AND ` + kindFilter + `) AS apps,
		  (SELECT count(DISTINCT pl.city_key)
		     FROM package_locations pl
		     JOIN packages p ON p.id = pl.package_id
		     LEFT JOIN package_meta m ON m.package_id = p.id
		    WHERE pl.status = 'approved' AND pl.city_key IS NOT NULL AND ` + kindFilter + `) AS places,
		  (SELECT count(DISTINCT pl.country_code)
		     FROM package_locations pl
		     JOIN packages p ON p.id = pl.package_id
		     LEFT JOIN package_meta m ON m.package_id = p.id
		    WHERE pl.status = 'approved' AND ` + kindFilter + `) AS countries
	`).Scan(&totals).Error
	return totals, err
}

// DeveloperPicks returns active picks in display order.
func (s *Store) DeveloperPicks(ctx context.Context, limit int) ([]DeveloperPick, error) {
	if limit < 1 || limit > 12 {
		limit = 4
	}
	var rows []DeveloperPick
	err := s.DB.WithContext(ctx).Raw(`
		SELECT dp.package_id, COALESCE(dp.quote, '') AS quote, dp.published_at
		FROM developer_picks dp
		JOIN packages p ON p.id = dp.package_id
		LEFT JOIN package_meta m ON m.package_id = p.id
		WHERE dp.active
		  AND p.removed_at IS NULL
		  AND NOT (p.disabled OR p.stale_disabled)
		  AND NOT COALESCE(m.hidden, false)
		  AND NOT COALESCE(m.private, false)
		  AND p.kind IN ('cask', 'link')
		ORDER BY dp.sort, dp.published_at DESC
		LIMIT ?
	`, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
