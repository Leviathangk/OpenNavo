package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

// DeveloperMapTotals is the header of the developer map page.
type DeveloperMapTotals struct {
	Developers  int `json:"developers"`
	Located     int `json:"located"`
	Apps        int `json:"apps"`
	LocatedApps int `json:"locatedApps"`
	Places      int `json:"places"`
	Countries   int `json:"countries"`
}

// DeveloperMapData is the public read model for /developer-map.
type DeveloperMapData struct {
	GeneratedAt      time.Time                `json:"generatedAt"`
	Totals           DeveloperMapTotals       `json:"totals"`
	Places           []store.DeveloperPlace   `json:"places"`
	Countries        []store.DeveloperCountry `json:"countries"`
	RecentDevelopers []store.DeveloperRecent  `json:"recentDevelopers"`
}

// DeveloperPickView is one Discover developer pick.
type DeveloperPickView struct {
	Package     publicapi.PackageSummary `json:"package"`
	Quote       string                   `json:"quote"`
	PublishedAt time.Time                `json:"publishedAt"`
}

// Country-only locations use a representative point so they still appear on the map.
// City locations carry their own coordinates.
var countryCentroids = map[string][2]float64{
	"US": {39.8, -98.6}, "CN": {35.9, 104.2}, "GB": {54.0, -2.0}, "JP": {36.2, 138.3},
	"AU": {-25.3, 133.8}, "CZ": {49.8, 15.5}, "UA": {48.4, 31.2}, "NL": {52.1, 5.3},
	"DE": {51.2, 10.4}, "CA": {56.1, -106.3}, "FR": {46.2, 2.2}, "AT": {47.5, 14.6},
	"KR": {35.9, 127.8}, "IN": {20.6, 78.9}, "IL": {31.0, 34.9}, "IT": {41.9, 12.6},
	"SE": {60.1, 18.6},
}

// DeveloperMap returns the map data. It is intentionally a separate public read from
// /home so the map can cache and ship without changing the homepage contract.
func (s *PublicService) DeveloperMap(ctx context.Context, locale string) (DeveloperMapData, error) {
	s = s.cacheScope(ctx)
	includeLinks := domain.LinksAllowed(ctx)
	return cache.ReadThrough(ctx, s.Cache, fmt.Sprintf("c:developer-map:v2:%s:%t", locale, includeLinks), 5*time.Minute, func(ctx context.Context) (DeveloperMapData, error) {
		places, err := s.Store.DeveloperMapPlaces(ctx, includeLinks)
		if err != nil {
			return DeveloperMapData{}, err
		}
		countries, err := s.Store.DeveloperMapCountries(ctx, includeLinks)
		if err != nil {
			return DeveloperMapData{}, err
		}
		recent, err := s.Store.DeveloperMapRecent(ctx, 4, includeLinks)
		if err != nil {
			return DeveloperMapData{}, err
		}
		totals, err := s.Store.DeveloperMapTotals(ctx, includeLinks)
		if err != nil {
			return DeveloperMapData{}, err
		}
		stats, err := s.Store.PublicStats(ctx)
		if err != nil {
			return DeveloperMapData{}, err
		}
		for index := range places {
			if places[index].Lat == 0 && places[index].Lng == 0 {
				if point, ok := countryCentroids[places[index].CountryCode]; ok {
					places[index].Lat = point[0]
					places[index].Lng = point[1]
				}
			}
			if places[index].TopDevelopers == nil {
				places[index].TopDevelopers = []store.DeveloperTop{}
			}
		}
		if places == nil {
			places = []store.DeveloperPlace{}
		}
		if countries == nil {
			countries = []store.DeveloperCountry{}
		}
		if recent == nil {
			recent = []store.DeveloperRecent{}
		}
		now := time.Now().UTC()
		if s.Now != nil {
			now = s.Now().UTC()
		}
		return DeveloperMapData{
			GeneratedAt: now,
			Totals: DeveloperMapTotals{
				Developers:  totals.TotalDevelopers,
				Located:     totals.LocatedDevelopers,
				Apps:        stats["casks"],
				LocatedApps: totals.Apps,
				Places:      len(places),
				Countries:   len(countries),
			},
			Places:           places,
			Countries:        countries,
			RecentDevelopers: recent,
		}, nil
	})
}

// DeveloperPicks returns the active developer picks for Discover, above Hot Apps.
func (s *PublicService) DeveloperPicks(ctx context.Context, locale string, limit int) ([]DeveloperPickView, error) {
	s = s.cacheScope(ctx)
	if limit < 1 || limit > 12 {
		limit = 4
	}
	return cache.ReadThrough(ctx, s.Cache, fmt.Sprintf("c:developer-picks:v1:%s:%d:%t", locale, limit, domain.LinksAllowed(ctx)), 5*time.Minute, func(ctx context.Context) ([]DeveloperPickView, error) {
		picks, err := s.Store.DeveloperPicks(ctx, limit)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(picks))
		for _, pick := range picks {
			ids = append(ids, pick.PackageID)
		}
		packages, err := s.Store.PublicPackagesByID(ctx, ids, locale)
		if err != nil {
			return nil, err
		}
		byID := make(map[int64]store.PublicPackage, len(packages))
		for _, pkg := range packages {
			byID[pkg.ID] = pkg
		}
		out := make([]DeveloperPickView, 0, len(picks))
		for _, pick := range picks {
			pkg, ok := byID[pick.PackageID]
			if !ok {
				continue
			}
			out = append(out, DeveloperPickView{
				Package:     summary(pkg),
				Quote:       pick.Quote,
				PublishedAt: pick.PublishedAt.UTC(),
			})
		}
		return out, nil
	})
}
