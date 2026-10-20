package store

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

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
	if r.Source == "editorial" && (r.EditorialUnavailable || !r.editorialTranslationReady()) {
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
		if r.TranslatedBody != nil && strings.TrimSpace(*r.TranslatedBody) != "" {
			body = *r.TranslatedBody
		} else if len(r.Sections) > 0 && string(r.Sections) != "[]" {
			var sections []struct {
				Items []string `json:"items"`
			}
			if json.Unmarshal(r.Sections, &sections) == nil {
				for _, section := range sections {
					body += "\n" + strings.Join(section.Items, "\n")
				}
			}
		} else if r.Summary != nil {
			body += "\n" + *r.Summary
		}
	}
	chars := seo.ReadableChars(visibleNotes(body))
	return seo.NotesFacts{Chars: chars, HasSource: chars > 0 && r.SourceURL != nil && strings.TrimSpace(*r.SourceURL) != ""}
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
				if notes.HasSource {
					metrics.Chars += notes.Chars
					metrics.HasSource = true
				}
			}
			rows[i].NotesPages[page][locale] = metrics
		}
	}
	return nil
}
