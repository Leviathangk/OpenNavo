package store

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

var wikitextExamples = []struct {
	name, text, kind string
}{
	{"top menu", "{{TopMenu}}", "navigation_template"},
	{"menu", "{{Menu|ReleaseNotes}}", "navigation_template"},
	{"menu case and spacing", "{{ topmenu|ReleaseNotes}}", "navigation_template"},
	{"category", "[[Category:Release|24.8]]", "category_link"},
	{"category case", "[[category:Release]]", "category_link"},
	{"file", "[[File:Screenshot.png|thumb]]", "media_link"},
	{"image", "[[Image:Screenshot.png]]", "media_link"},
	{"heading", "== Changes ==", "heading"},
	{"deep heading", "=== 改进 ===", "heading"},
	{"compact heading", "==Changes==", "heading"},
	{"heading after paragraph", "Release notes\r\n  == Fixes ==\r\n", "heading"},
	{"template list", "# {{tdf|123456}}", "template_list"},
	{"nested template list", "## {{tdf|123456}}", "template_list"},
	{"nowiki", "<nowiki>literal text</nowiki>", "nowiki_tag"},
	{"nowiki self closing", "<NOWIKI />", "nowiki_tag"},
	{"no toc", "__NOTOC__", "toc_directive"},
	{"toc", "__TOC__", "toc_directive"},
	{"table", "{| class=\"wikitable\"", "table"},
}

func TestReleaseNotesRejectWikitext(t *testing.T) {
	for _, tc := range wikitextExamples {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{"bodyMarkdown": tc.text}
			err := ValidateContentFields("release", fields)
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, domain.CodeValidation, app.Code)
			require.Equal(t, "rejected: release_notes_wikitext_convert_to_markdown:"+tc.kind+":bodyMarkdown", app.Msg)
			require.NotContains(t, app.Msg, tc.text)
			require.EqualError(t, ValidateLocalizedFields("release", "de-DE", fields), err.Error())
		})
	}
}

func TestReleaseNotesAllowMarkdownAndProgrammingSyntax(t *testing.T) {
	for _, text := range []string{
		"Handlebars supports {{faker 'x'}} and {{{token}}} templates.",
		"Mockoon supports {{faker 'person.firstName'}} templates.",
		"Use {{faker random.number min=1 max=10}} in a response.",
		"Fixed zsh [[ -f x ]] checks.",
		"Fixed zsh [[ -n $CLAUDE_CODE ]] checks.",
		"[[ $value == *pattern* ]] && print ok",
		"C++ supports [[deprecated]] attributes.",
		"See logseq [[Fixed issues]] for details.",
		"Julia supports @__FUNCTION__ inside macros.",
		"# Heading",
		"## Changes\n\n- Fixed a crash.\n\n1. Install the update.",
		"Changes\n=======\n\nMarkdown setext headings work.",
		"| Feature | Status |\n| --- | --- |\n| Export | Fixed |",
		"Fixed [release links](https://wiki.example/ReleaseNotes).",
		"[[Download]](https://example.com/download)",
		"Use {{variable}} and [[condition]] in templates and scripts.",
		"# Markdown heading with {{faker 'person.firstName'}}",
		"== comparison operators do not form a heading",
	} {
		t.Run(text, func(t *testing.T) {
			require.NoError(t, ValidateContentFields("release", map[string]any{"bodyMarkdown": text}))
		})
	}
}

func TestReleaseNotesIgnoreCode(t *testing.T) {
	for _, tc := range wikitextExamples {
		t.Run(tc.name, func(t *testing.T) {
			for _, wrapped := range []string{
				"```wikitext\n" + tc.text + "\n```",
				"~~~~wikitext\n" + tc.text + "\n~~~~",
				"````markdown\n```wiki\n" + tc.text + "\n```\n````",
				"```wiki\n" + tc.text,
				"`" + tc.text + "`",
				"`` literal ` and " + tc.text + " ``",
				"- Example:\n\n  ```wiki\n  " + strings.ReplaceAll(tc.text, "\n", "\n  ") + "\n  ```",
				"    " + strings.ReplaceAll(tc.text, "\n", "\n    "),
			} {
				require.NoError(t, ValidateReleaseNotes(map[string]any{"body_markdown": wrapped}), wrapped)
			}
		})
	}
	// Fence info strings and multiline code spans are code too.
	for _, text := range []string{
		"```{{TopMenu}}\nexample\n```",
		"Before `example\n{{TopMenu}}\nmore` after.",
	} {
		require.NoError(t, ValidateReleaseNotes(map[string]any{"bodyMarkdown": text}))
	}
}

func TestReleaseNotesStillDetectMarkupOutsideCode(t *testing.T) {
	for _, text := range []string{
		"`{{TopMenu}}` and [[Category:Release]]",
		"```wiki\n{{TopMenu}}\n```\n\n__TOC__",
		"````wiki\n```\n{{TopMenu}}\n````\n\n== Changes ==",
		"An unmatched ` followed by {{TopMenu}}",
		"An escaped \\`{{TopMenu}}\\` is not code.",
		"== `Changes` ==",
	} {
		require.Error(t, ValidateReleaseNotes(map[string]any{"bodyMarkdown": text}), text)
	}
}

func TestReleaseNotesCheckEveryTextField(t *testing.T) {
	for _, tc := range []struct {
		path   string
		fields map[string]any
	}{
		{"bodyMarkdown", map[string]any{"bodyMarkdown": "{{TopMenu}}"}},
		{"body_markdown", map[string]any{"body_markdown": "{{TopMenu}}"}},
		{"summary", map[string]any{"summary": "{{TopMenu}}"}},
		{"title", map[string]any{"title": "{{TopMenu}}"}},
		{"sections.0.area", map[string]any{"sections": []any{map[string]any{"area": "{{TopMenu}}", "items": []any{}}}}},
		{"sections.0.items.0", map[string]any{"sections": []any{map[string]any{"area": "Fixes", "items": []any{"[[Category:Release]]"}}}}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, err := range []error{ValidateContentFields("release", tc.fields), ValidateLocalizedFields("release", "ja-JP", tc.fields), validateContentFields("release", "", tc.fields, false)} {
				require.ErrorContains(t, err, "convert_to_markdown")
				require.ErrorContains(t, err, tc.path)
			}
		})
	}
	// This guard is specific to releases and checks text, not source URLs/metadata.
	require.NoError(t, ValidateContentFields("package", map[string]any{"description": "{{TopMenu}}"}))
	require.NoError(t, ValidateReleaseNotes(map[string]any{"sourceUrl": "[[Category:Release]]"}))
	require.NoError(t, ValidateReleaseNotes(map[string]any{"bodyMarkdown": nil, "sections": []any{}}))
}

func TestReleaseNotesRejectBeforeStoreWrites(t *testing.T) {
	ctx := context.Background()
	st := &Store{} // No database: rejected input must never reach a write.
	ref := content.Ref{Entity: "release", ID: 42}
	fields := map[string]any{"body_markdown": "{{TopMenu}}"}
	for _, source := range []bool{true, false} {
		require.ErrorContains(t, st.WriteContentText(ctx, ref, "en-US", fields, source), "navigation_template")
	}
	for _, table := range []string{"releases", "release_i18n"} {
		require.ErrorContains(t, st.RestoreHistoryState(ctx, ref, HistoryState{table: {fields}}), "navigation_template")
	}
}
