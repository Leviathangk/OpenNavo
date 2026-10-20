package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotesEvidenceDoesNotCountFallbackOrInvalidTranslation(t *testing.T) {
	source, translated, status, hash, url := strings.Repeat("原", 160), strings.Repeat("譯", 160), "machine", strings.Repeat("a", 64), "https://example.com/releases"
	r := TimelineRelease{SourceLocale: "en-US", Locale: "zh-Hant", BodyMarkdown: &source, TranslatedBody: &translated, TranslationStatus: &status, SourceHash: &hash, SourceURL: &url}
	require.Equal(t, 320, releaseNotesFacts(r, "zh-Hant").Chars)
	require.True(t, releaseNotesFacts(r, "zh-Hant").HasSource)
	require.Zero(t, releaseNotesFacts(r, "fr-FR").Chars)
	r.SourceHash = nil
	require.Zero(t, releaseNotesFacts(r, "zh-Hant").Chars)
	require.Equal(t, 320, releaseNotesFacts(r, "en-US").Chars)
	r.SourceHash, r.SourceURL = &hash, nil
	require.False(t, releaseNotesFacts(r, "zh-Hant").HasSource)
	status = "pending"
	require.Zero(t, releaseNotesFacts(r, "zh-Hant").Chars)
	status = "skipped"
	require.Zero(t, releaseNotesFacts(r, "en-US").Chars)
	require.Equal(t, "Release ", visibleNotes("[Release](https://example.com/"+strings.Repeat("a", 400)+") <!-- hidden -->"))
}

func TestEditorialNotesEvidenceRequiresCurrentSourceHash(t *testing.T) {
	body, status, hash, url := strings.Repeat("Translated notes ", 30), "manual", strings.Repeat("a", 64), "https://vendor.example/releases"
	r := TimelineRelease{Source: "editorial", SourceLocale: "zh-CN", Locale: "en-US", TranslatedBody: &body, TranslationStatus: &status, SourceHash: &hash, CurrentSourceHash: hash, SourceURL: &url}
	require.Positive(t, releaseNotesFacts(r, "en-US").Chars)
	r.CurrentSourceHash = strings.Repeat("b", 64)
	require.Zero(t, releaseNotesFacts(r, "en-US").Chars, "well-formed but stale hash is not a translated note")
	r.CurrentSourceHash = hash
	status = "pending"
	require.Zero(t, releaseNotesFacts(r, "en-US").Chars)
}
