package catalog

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
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

// timeline lists one entry per base version plus one per build with an editorial note of its own:
// casks such as Amore keep 0.1 while every build ships new notes. Builds without their own note
// share the entry of their base version, dated by the first of them as before.
func (s *PublicService) timeline(ctx context.Context, id int64, locale string) ([]publicapi.ReleaseEntry, error) {
	versions, err := s.Store.TimelineVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	releases, err := s.Store.TimelineReleases(ctx, id, locale)
	if err != nil {
		return nil, err
	}
	type item struct {
		entry  publicapi.ReleaseEntry
		base   string
		recent time.Time
	}
	newItem := func(version, base string) *item {
		return &item{base: base, entry: publicapi.ReleaseEntry{Version: version, VersionBase: &base, Source: publicapi.ReleaseEntrySourceHomebrew, Sections: []publicapi.ReleaseSection{}, Translation: publicapi.TranslationInfo{Status: publicapi.TranslationInfoStatusNone}}}
	}
	// The first local observation is not an upstream commit timestamp; using it would fabricate ingestion latency.
	earliestCommit := func(it *item, v store.TimelineVersion) {
		if v.BrewCommittedAt != nil && (it.entry.BrewCommittedAt == nil || v.BrewCommittedAt.Before(*it.entry.BrewCommittedAt)) {
			it.entry.BrewCommittedAt = v.BrewCommittedAt
		}
	}
	builds, byBase, noted := map[int64]store.TimelineVersion{}, map[string][]store.TimelineVersion{}, map[int64]bool{}
	for _, v := range versions {
		builds[v.ID] = v
		byBase[v.VersionBase] = append(byBase[v.VersionBase], v)
	}
	for _, r := range releases {
		if r.PackageVersionID != nil {
			noted[*r.PackageVersionID] = true
		}
	}
	items := map[string]*item{}
	for _, v := range versions {
		if noted[v.ID] {
			continue
		}
		it := items[v.VersionBase]
		if it == nil {
			it = newItem(v.VersionBase, v.VersionBase)
			items[v.VersionBase] = it
		}
		earliestCommit(it, v)
		if seen := v.Seen(); seen.After(it.recent) {
			it.recent = seen
		}
	}
	chosen := map[string]bool{}
	for _, r := range releases {
		key := r.Version
		var build *store.TimelineVersion
		if r.PackageVersionID != nil {
			v, ok := builds[*r.PackageVersionID]
			if !ok {
				continue
			}
			build, key = &v, v.Version
		}
		if chosen[key] {
			continue
		}
		chosen[key] = true
		it := items[key]
		if it == nil {
			it = newItem(key, r.Version)
			if build != nil {
				_, tail, _ := strings.Cut(build.Version, ",")
				it.entry.Build = &tail
				earliestCommit(it, *build)
				it.recent = build.Seen()
			} else {
				// Every build of this base has its own note: the shared note belongs before all of them.
				for _, v := range byBase[key] {
					earliestCommit(it, v)
					if seen := v.Seen(); it.recent.IsZero() || seen.Before(it.recent) {
						it.recent = seen
					}
				}
			}
			items[key] = it
		}
		entry := &it.entry
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
	}
	list := make([]*item, 0, len(items))
	for _, it := range items {
		list = append(list, it)
	}
	// Newest base version first; within one, the newest builds first.
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.base != b.base {
			return pipeline.VersionLess(a.base, b.base)
		}
		if !a.recent.Equal(b.recent) {
			return a.recent.After(b.recent)
		}
		return pipeline.VersionLess(a.entry.Version, b.entry.Version)
	})
	out := make([]publicapi.ReleaseEntry, 0, len(list))
	for _, it := range list {
		out = append(out, it.entry)
	}
	if len(out) > 0 {
		out[0].IsLatest = true
	}
	return out, nil
}
