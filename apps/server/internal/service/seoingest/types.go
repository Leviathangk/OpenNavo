// Package seoingest collects measurement data. It never changes content or submits URLs.
package seoingest

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrDisabled = errors.New("SEO provider disabled")
var ErrUpstream = errors.New("SEO provider unavailable")

const dateLayout = "2006-01-02"
const pageLimit = 25000
const maxDailyRows = 50000

type Query struct {
	StartDate  string   `json:"startDate"`
	EndDate    string   `json:"endDate"`
	Dimensions []string `json:"dimensions"`
	DataState  string   `json:"dataState"`
	Type       string   `json:"type"`
	RowLimit   int      `json:"rowLimit"`
	StartRow   int      `json:"startRow"`
}
type SearchRow struct {
	Keys        []string `json:"keys"`
	Clicks      int64    `json:"clicks"`
	Impressions int64    `json:"impressions"`
	Position    float64  `json:"position"`
}
type SearchResult struct {
	Rows     []SearchRow `json:"rows"`
	Metadata struct {
		FirstIncompleteDate string `json:"first_incomplete_date"`
	} `json:"metadata"`
}
type Inspection struct {
	CoverageState   string     `json:"coverageState"`
	IndexingState   string     `json:"indexingState"`
	GoogleCanonical string     `json:"googleCanonical"`
	LastCrawlTime   *time.Time `json:"lastCrawlTime"`
}
type Google interface {
	Analytics(context.Context, Query) (SearchResult, error)
	Inspect(context.Context, string) (Inspection, error)
}
type BingRow struct {
	Day                 string
	Dimension           string
	Value               string
	Clicks, Impressions int64
	Position            float64
}
type BingResult struct {
	Rows  []BingRow
	Crawl map[string]map[string]int64
	// Bing exposes observations, not an explicit final-data marker. Nil stays unknown.
	CompleteThrough *time.Time
}
type Bing interface {
	Read(context.Context) (BingResult, error)
}
type URL struct {
	ID        int64
	URL       string
	PageKind  string
	Locale    string
	PackageID *int64
	Expected  string
}
type Service struct {
	DB     *gorm.DB
	Google func(context.Context) (Google, error)
	Bing   func(context.Context) (Bing, error)
	Audit  func(context.Context) (Auditor, error)
	WebURL string
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func day(t time.Time) string               { return t.Format(dateLayout) }
func parseDay(s string) (time.Time, error) { return time.Parse(dateLayout, s) }
