package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/stretchr/testify/require"
)

// An editorial note that only says the changes are not known is never published, in its source language or a
// translation, and wherever its translation stands: the version stays listed with its dates and links, without text.
func TestNoInformationNotesAreListedWithoutTheirText(t *testing.T) {
	text := func(value string) *string { return &value }
	published := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	committed := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	placeholder := "Official release notes are unavailable."
	versions := []TimelineVersion{
		{PackageID: 1, ID: 20, Version: "2.0", VersionBase: "2.0", BrewCommittedAt: &committed},
		{PackageID: 1, ID: 10, Version: "1.0", VersionBase: "1.0", BrewCommittedAt: &committed},
	}
	for _, tc := range []struct {
		name   string
		locale string
		note   func(r *TimelineRelease)
	}{
		{name: "source", locale: "en-US", note: func(r *TimelineRelease) {
			r.Title, r.TranslatedTitle, r.BodyMarkdown, r.TranslatedBody = text("Version 2.0"), text("Version 2.0"), text(placeholder), text(placeholder)
			r.Summary, r.Sections, r.TranslationStatus = text(placeholder), json.RawMessage(`[{"area":"Changes","items":["Not provided."]}]`), text("source")
		}},
		{name: "machine translation", locale: "ja-JP", note: func(r *TimelineRelease) {
			r.TranslatedTitle, r.TranslatedBody, r.Summary = text("バージョン 2.0"), text("このバージョンの変更内容は提供されていません。"), text("変更内容は不明です。")
			r.TranslationStatus, r.MachineTranslated = text("machine"), true
		}},
		{name: "pending translation", locale: "de-DE", note: func(r *TimelineRelease) {
			r.EditorialUnavailable, r.TranslationStatus = true, text("pending")
		}},
		{name: "skipped translation", locale: "fr-FR", note: func(r *TimelineRelease) {
			r.TranslationStatus = text("skipped")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			empty := TimelineRelease{ID: 200, PackageID: 1, Version: "2.0", Source: "editorial", SourceLocale: "en-US", Locale: tc.locale,
				SourceURL: text("https://vendor.example/releases/2.0"), PublishedAt: &published, IsPrerelease: true, NoInformation: true}
			tc.note(&empty)
			noted := TimelineRelease{ID: 100, PackageID: 1, Version: "1.0", Source: "editorial", SourceLocale: "en-US", Locale: tc.locale,
				Summary: text("Adds tabs"), TranslatedBody: text("Adds tabs to the main window."), TranslationStatus: text("machine")}
			entries, err := TimelineEntries(versions, []TimelineRelease{empty, noted})
			require.NoError(t, err)
			require.Len(t, entries, 2, "the version stays listed")
			entry := entries[0]
			require.Equal(t, "2.0", entry.Version)
			require.True(t, entry.IsLatest)
			require.Equal(t, int64(200), *entry.Id)
			require.Equal(t, publicapi.ReleaseEntrySourceEditorial, entry.Source)
			require.Equal(t, published, *entry.PublishedAt)
			require.Equal(t, committed, *entry.BrewCommittedAt)
			require.Equal(t, "https://vendor.example/releases/2.0", *entry.SourceUrl)
			require.True(t, entry.IsPrerelease)
			require.False(t, entry.HasNotes)
			require.Nil(t, entry.Title)
			require.Nil(t, entry.Summary)
			require.Nil(t, entry.BodyMarkdown)
			require.Empty(t, entry.Sections)
			require.False(t, *entry.MachineTranslated)
			require.Equal(t, publicapi.TranslationInfo{Status: publicapi.TranslationInfoStatusNone}, entry.Translation)
			raw, err := json.Marshal(entry)
			require.NoError(t, err)
			for _, fragment := range []string{"unavailable", "Not provided", "Version 2.0", "バージョン", "変更内容"} {
				require.NotContains(t, string(raw), fragment)
			}
			// Notes with information are untouched, and the empty note still counts as no notes on the page.
			require.True(t, entries[1].HasNotes)
			require.Equal(t, "Adds tabs to the main window.", *entries[1].BodyMarkdown)
			byID := map[int64]TimelineRelease{empty.ID: empty, noted.ID: noted}
			require.Equal(t, seo.NotesFacts{}, pageNotesFacts(entries[:1], byID, 1, tc.locale))
		})
	}
}

// Like an editorial note this language cannot show, a note without information gives way to the original upstream
// notes of the same version (or of its base version, for a build's own note), and never to another version's.
func TestNoInformationNotesFallBackToUpstreamNotes(t *testing.T) {
	text := func(value string) *string { return &value }
	build := int64(21)
	versions := []TimelineVersion{
		{PackageID: 1, ID: build, Version: "2.0,49", VersionBase: "2.0"},
		{PackageID: 1, ID: 10, Version: "1.0", VersionBase: "1.0"},
	}
	upstream := TimelineRelease{ID: 300, PackageID: 1, Version: "2.0", Source: "github_release", SourceLocale: "en-US",
		Title: text("v2.0"), BodyMarkdown: text("- Fixed a crash when importing large files."), Priority: 20}
	other := TimelineRelease{ID: 301, PackageID: 1, Version: "1.0", Source: "github_release", SourceLocale: "en-US",
		BodyMarkdown: text("- Another version's notes."), Priority: 20}
	for _, tc := range []struct {
		name, version string
		build         *int64
	}{{name: "base note", version: "2.0"}, {name: "build note", version: "2.0,49", build: &build}} {
		t.Run(tc.name, func(t *testing.T) {
			status := "source"
			empty := TimelineRelease{ID: 200, PackageID: 1, PackageVersionID: tc.build, Version: "2.0", Source: "editorial", SourceLocale: "en-US",
				Locale: "en-US", Summary: text("Official release notes are unavailable."), TranslationStatus: &status, NoInformation: true}
			entries, err := TimelineEntries(versions, []TimelineRelease{empty, upstream, other})
			require.NoError(t, err)
			var entry publicapi.ReleaseEntry
			for _, candidate := range entries {
				if candidate.Version == tc.version {
					entry = candidate
				}
			}
			require.NotNil(t, entry.Id, "the note's own entry is listed")
			require.Equal(t, int64(300), *entry.Id)
			require.Equal(t, publicapi.ReleaseEntrySourceGithubRelease, entry.Source)
			require.True(t, entry.HasNotes)
			require.Equal(t, "- Fixed a crash when importing large files.", *entry.BodyMarkdown)
			require.Nil(t, entry.Summary)
			require.Equal(t, publicapi.TranslationInfoStatusSource, entry.Translation.Status)
		})
	}
	// Without notes for its own version, the release is listed without notes.
	status := "source"
	empty := TimelineRelease{ID: 200, PackageID: 1, Version: "2.0", Source: "editorial", SourceLocale: "en-US", Locale: "en-US",
		Summary: text("Official release notes are unavailable."), TranslationStatus: &status, NoInformation: true}
	entries, err := TimelineEntries(versions, []TimelineRelease{empty, other})
	require.NoError(t, err)
	require.Equal(t, int64(200), *entries[0].Id)
	require.False(t, entries[0].HasNotes)
	require.Nil(t, entries[0].Summary)
}
