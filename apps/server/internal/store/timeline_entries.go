package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
)

// TimelineEntries uses the same version grouping for public pagination and SEO page evidence.
func TimelineEntries(versions []TimelineVersion, releases []TimelineRelease) ([]publicapi.ReleaseEntry, error) {
	// Preserve source priority and build grouping while allowing unavailable
	// editorial translations to use this version's original upstream notes.
	type upstreamKey struct {
		packageID int64
		version   string
		buildID   int64
	}
	upstream := map[upstreamKey]TimelineRelease{}
	for _, r := range releases {
		if r.Source == "editorial" || r.BodyMarkdown == nil || strings.TrimSpace(*r.BodyMarkdown) == "" {
			continue
		}
		key := upstreamKey{packageID: r.PackageID, version: r.Version}
		if r.PackageVersionID != nil {
			key.buildID = *r.PackageVersionID
		}
		if _, exists := upstream[key]; !exists {
			upstream[key] = r.originalNotes()
		}
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
	earliestCommit := func(it *item, v TimelineVersion) {
		if v.BrewCommittedAt != nil && (it.entry.BrewCommittedAt == nil || v.BrewCommittedAt.Before(*it.entry.BrewCommittedAt)) {
			it.entry.BrewCommittedAt = v.BrewCommittedAt
		}
	}
	builds, byBase, noted := map[int64]TimelineVersion{}, map[string][]TimelineVersion{}, map[int64]bool{}
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
		var build *TimelineVersion
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
		if r.EditorialUnavailable {
			key := upstreamKey{packageID: r.PackageID, version: r.Version}
			if r.PackageVersionID != nil {
				key.buildID = *r.PackageVersionID
			}
			fallback, found := upstream[key]
			if !found && key.buildID != 0 {
				key.buildID = 0
				fallback, found = upstream[key]
			}
			if found {
				r = fallback
			}
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
			if r.Source == "editorial" {
				lang := publicapi.Locale(r.Locale)
				entry.Translation = publicapi.TranslationInfo{Status: publicapi.TranslationInfoStatusSkipped, Locale: &lang, SourceLocale: &source, MachineTranslated: &machine}
			}
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

// A fallback is upstream reference text, never another language's editorial summary.
func (r TimelineRelease) originalNotes() TimelineRelease {
	r.Locale = r.SourceLocale
	r.TranslatedTitle, r.TranslatedBody, r.Summary = nil, nil, nil
	r.Sections = nil
	r.MachineTranslated = false
	status := "source"
	r.TranslationStatus = &status
	return r
}

func (s *Store) TimelineEntries(ctx context.Context, id int64, locale string) ([]publicapi.ReleaseEntry, error) {
	versions, err := s.TimelineVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	releases, err := s.TimelineReleases(ctx, id, locale)
	if err != nil {
		return nil, err
	}
	return TimelineEntries(versions, releases)
}
