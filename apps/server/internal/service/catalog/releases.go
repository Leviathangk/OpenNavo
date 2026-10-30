package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
)

func (s *PublicService) ReleasePage(ctx context.Context, kind, token, locale string, current, size int, onlyWithNotes bool) (publicapi.ReleasePage, error) {
	s = s.cacheScope(ctx)
	out := publicapi.ReleasePage{Current: current, Size: size, Records: []publicapi.ReleaseEntry{}}
	if !ValidPage(current, size, 100) {
		return out, domain.Validation()
	}
	p, err := s.Store.PublicPackage(ctx, kind, token, locale)
	if err != nil {
		return out, notFound(err)
	}
	all, err := cache.ReadThrough(ctx, s.Cache, cache.ReleaseTimelinePrefix+kind+":"+token+":"+locale, 10*time.Minute, func(ctx context.Context) ([]publicapi.ReleaseEntry, error) { return s.timeline(ctx, p.ID, locale) })
	if err != nil {
		return out, err
	}
	dates, brew := []*time.Time{}, []*time.Time{}
	for _, entry := range all {
		dates = append(dates, entry.PublishedAt)
		brew = append(brew, entry.BrewCommittedAt)
	}
	stats := pipeline.Stats(dates, brew, s.now())
	out.Stats.Count30d = stats.Count30d
	out.Stats.Cadence = publicapi.ReleaseStatsCadence(stats.Cadence)
	out.Stats.BrewLag.Compared = stats.Compared
	out.Stats.BrewLag.EarlierCount = stats.EarlierCount
	out.Stats.BrewLag.MedianMinutes = stats.MedianMinutes
	// Stats always describe the whole timeline; the notes filter only narrows the listed versions and their total.
	entries := all
	if onlyWithNotes {
		entries = make([]publicapi.ReleaseEntry, 0, len(all))
		for _, entry := range all {
			if entry.HasNotes {
				entries = append(entries, entry)
			}
		}
	}
	out.Total = len(entries)
	start, found := domain.PageOffset(current, size, int64(len(entries)))
	if found {
		out.Records = entries[start:min(start+size, len(entries))]
	}
	for i := range out.Records {
		entry := &out.Records[i]
		rows, err := s.Store.VersionRequirements(ctx, p.ID, entry.Version, s.now())
		if err != nil {
			return out, err
		}
		value := facts.SummarizeCompatibility(entry.Version, rows, s.now())
		raw, err := json.Marshal(value)
		if err != nil {
			return out, err
		}
		entry.Compatibility = &publicapi.VersionCompatibility{}
		if err := json.Unmarshal(raw, entry.Compatibility); err != nil {
			return out, err
		}
	}
	out.Indexing, err = s.releaseDecision(ctx, token, locale, current, size, onlyWithNotes)
	return out, err
}

func (s *PublicService) timeline(ctx context.Context, id int64, locale string) ([]publicapi.ReleaseEntry, error) {
	return s.Store.TimelineEntries(ctx, id, locale)
}
