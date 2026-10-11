package catalog

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (s *PublicService) ReleasePage(ctx context.Context, kind, token, locale string, current, size int, onlyWithNotes bool) (publicapi.ReleasePage, error) {
	out := publicapi.ReleasePage{Current: current, Size: size, Records: []publicapi.ReleaseEntry{}}
	if !ValidPage(current, size, 100) {
		return out, domain.Validation()
	}
	p, err := s.Store.PublicPackage(ctx, kind, token, locale)
	if err != nil {
		return out, notFound(err)
	}
	all, err := cache.ReadThrough(ctx, s.Cache, "c:rel:c2:"+kind+":"+token+":"+locale, 10*time.Minute, func(ctx context.Context) ([]publicapi.ReleaseEntry, error) { return s.timeline(ctx, p.ID, locale) })
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
	return out, nil
}
func (s *PublicService) timeline(ctx context.Context, id int64, locale string) ([]publicapi.ReleaseEntry, error) {
	versions, err := s.Store.TimelineVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	releases, err := s.Store.TimelineReleases(ctx, id, locale)
	if err != nil {
		return nil, err
	}
	items := map[string]publicapi.ReleaseEntry{}
	for _, v := range versions {
		// The first local observation is not an upstream commit timestamp; using it would fabricate ingestion latency.
		items[v.VersionBase] = publicapi.ReleaseEntry{Version: v.VersionBase, Source: publicapi.ReleaseEntrySourceHomebrew, BrewCommittedAt: v.BrewCommittedAt, Sections: []publicapi.ReleaseSection{}, Translation: publicapi.TranslationInfo{Status: publicapi.TranslationInfoStatusNone}}
	}
	chosen := map[string]bool{}
	for _, r := range releases {
		if chosen[r.Version] {
			continue
		}
		chosen[r.Version] = true
		entry := items[r.Version]
		entry.Version = r.Version
		entry.Id = &r.ID
		entry.Title = r.Title
		if r.TranslatedTitle != nil {
			entry.Title = r.TranslatedTitle
		}
		source := publicapi.Locale(r.SourceLocale)
		machine := r.MachineTranslated
		entry.SourceLocale = &source
		entry.MachineTranslated = &machine
		entry.Source = publicapi.ReleaseEntrySource(r.Source)
		entry.SourceUrl = r.SourceURL
		entry.PublishedAt = r.PublishedAt
		entry.IsPrerelease = r.IsPrerelease
		entry.BodyMarkdown = r.BodyMarkdown
		entry.HasNotes = r.BodyMarkdown != nil && *r.BodyMarkdown != ""
		entry.Sections = []publicapi.ReleaseSection{}
		entry.Translation = publicapi.TranslationInfo{Status: publicapi.TranslationInfoStatusNone}
		if r.TranslationStatus != nil && *r.TranslationStatus == "skipped" {
			entry.HasNotes = false
			entry.BodyMarkdown = nil
		}
		if r.TranslationStatus != nil && (*r.TranslationStatus == "machine" || *r.TranslationStatus == "manual" || *r.TranslationStatus == "source" || *r.TranslationStatus == "pending" || *r.TranslationStatus == "failed") {
			if r.TranslatedBody != nil && *r.TranslatedBody != "" {
				entry.BodyMarkdown = r.TranslatedBody
			}
			entry.Summary = r.Summary
			entry.Translation.Status = publicapi.TranslationInfoStatus(*r.TranslationStatus)
			lang := publicapi.Locale(r.Locale)
			entry.Translation.Locale = &lang
			entry.Translation.SourceLocale = &source
			entry.Translation.MachineTranslated = &machine
			if len(r.Sections) > 0 {
				if err := json.Unmarshal(r.Sections, &entry.Sections); err != nil {
					return nil, err
				}
			}
			entry.HasNotes = entry.HasNotes || (entry.BodyMarkdown != nil && *entry.BodyMarkdown != "") || (entry.Summary != nil && *entry.Summary != "") || len(entry.Sections) > 0
		}
		items[r.Version] = entry
	}
	out := make([]publicapi.ReleaseEntry, 0, len(items))
	for _, entry := range items {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return pipeline.VersionLess(out[i].Version, out[j].Version) })
	if len(out) > 0 {
		out[0].IsLatest = true
	}
	return out, nil
}
