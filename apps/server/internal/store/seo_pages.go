package store

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/seo"
)

var notesHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var notesLinks = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
var notesHTML = regexp.MustCompile(`(?s)<!--.*?-->|<[^>]+>`)
var notesURLs = regexp.MustCompile(`https?://\S+`)

func visibleNotes(text string) string {
	return notesURLs.ReplaceAllString(notesHTML.ReplaceAllString(notesLinks.ReplaceAllString(text, "$1"), ""), "")
}

// Count only text published in the requested language; an English fallback is never a translation.
func releaseNotesFacts(r TimelineRelease, locale string) seo.NotesFacts {
	if r.Source == "editorial" && (r.EditorialUnavailable || r.NoInformation || !r.editorialTranslationReady()) {
		return seo.NotesFacts{}
	}
	if r.TranslationStatus != nil && *r.TranslationStatus == "skipped" {
		return seo.NotesFacts{}
	}
	body := ""
	if locale == r.SourceLocale && r.BodyMarkdown != nil {
		body = *r.BodyMarkdown
	}
	if r.Locale == locale && r.TranslationStatus != nil &&
		(*r.TranslationStatus == "source" || *r.TranslationStatus == "manual" || *r.TranslationStatus == "machine") &&
		(locale == r.SourceLocale || r.SourceHash != nil && notesHash.MatchString(*r.SourceHash)) {
		switch {
		case r.TranslatedBody != nil && strings.TrimSpace(*r.TranslatedBody) != "":
			body = *r.TranslatedBody
		case len(r.Sections) > 0 && string(r.Sections) != "[]":
			var sections []struct {
				Items []string `json:"items"`
			}
			if json.Unmarshal(r.Sections, &sections) == nil {
				for _, section := range sections {
					body += "\n" + strings.Join(section.Items, "\n")
				}
			}
		case r.Summary != nil:
			body += "\n" + *r.Summary
		}
	}
	chars := seo.ReadableChars(visibleNotes(body))
	if chars == 0 {
		return seo.NotesFacts{}
	}
	return seo.NotesFacts{Chars: chars, Entries: 1}
}

// PopulatePageNotes batches database reads across entities. Version grouping and ordering are
// shared with the public release timeline, so page N means exactly the same notes everywhere.
func (s *Store) PopulatePageNotes(ctx context.Context, rows []IndexRow, page int) error {
	if len(rows) == 0 {
		return nil
	}
	page = max(1, page)
	ids := make([]int64, len(rows))
	positions := map[int64]int{}
	for i := range rows {
		ids[i], positions[rows[i].ID] = rows[i].ID, i
		if rows[i].NotesPages == nil {
			rows[i].NotesPages = map[int]map[string]seo.NotesFacts{}
		}
		rows[i].NotesPages[page] = map[string]seo.NotesFacts{}
	}
	versions, err := s.timelineVersionsFor(ctx, ids)
	if err != nil {
		return err
	}
	byVersion := map[int64][]TimelineVersion{}
	for _, v := range versions {
		byVersion[v.PackageID] = append(byVersion[v.PackageID], v)
	}
	for _, locale := range seo.Locales {
		releases, err := s.timelineReleasesFor(ctx, ids, locale)
		if err != nil {
			return err
		}
		byPackage := map[int64][]TimelineRelease{}
		byID := map[int64]TimelineRelease{}
		for _, r := range releases {
			byPackage[r.PackageID] = append(byPackage[r.PackageID], r)
			byID[r.ID] = r
		}
		for id, i := range positions {
			entries, err := TimelineEntries(byVersion[id], byPackage[id])
			if err != nil {
				return err
			}
			rows[i].NotesPages[page][locale] = pageNotesFacts(entries, byID, page, locale)
		}
	}
	return nil
}

// pageNotesFacts counts the notes on one versions page (seo.VersionsPageSize timeline entries) the way the page
// shows them. byID holds the timeline's releases in the same language.
func pageNotesFacts(entries []publicapi.ReleaseEntry, byID map[int64]TimelineRelease, page int, locale string) seo.NotesFacts {
	metrics := seo.NotesFacts{}
	start := len(entries)
	if page-1 <= len(entries)/seo.VersionsPageSize {
		start = min(len(entries), (page-1)*seo.VersionsPageSize)
	}
	for _, entry := range entries[start:min(len(entries), start+seo.VersionsPageSize)] {
		if entry.Id == nil {
			continue
		}
		r := byID[*entry.Id]
		if r.Source != "editorial" && entry.Translation.Status == "source" {
			r = r.originalNotes()
		}
		notes := releaseNotesFacts(r, locale)
		metrics.Chars += notes.Chars
		metrics.Entries += notes.Entries
	}
	return metrics
}

// timelineWithNotes is one package's timeline in one language plus the notes facts of every versions page, so
// the page-state job can store what PopulatePageNotes would otherwise recompute for whole sitemap pages.
func (s *Store) timelineWithNotes(ctx context.Context, id int64, locale string) ([]publicapi.ReleaseEntry, []seo.NotesFacts, error) {
	versions, err := s.TimelineVersions(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	releases, err := s.TimelineReleases(ctx, id, locale)
	if err != nil {
		return nil, nil, err
	}
	entries, err := TimelineEntries(versions, releases)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[int64]TimelineRelease, len(releases))
	for _, r := range releases {
		byID[r.ID] = r
	}
	pages := make([]seo.NotesFacts, max(1, (len(entries)+seo.VersionsPageSize-1)/seo.VersionsPageSize))
	for i := range pages {
		pages[i] = pageNotesFacts(entries, byID, i+1, locale)
	}
	return entries, pages, nil
}
