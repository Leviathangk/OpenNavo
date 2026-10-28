package store

import (
	"regexp"
	"slices"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var releaseNotesWikitextRules = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"navigation_template", regexp.MustCompile(`(?i)\{\{[ \t]*(TopMenu|Menu)\b`)},
	{"category_link", regexp.MustCompile(`(?i)\[\[[ \t]*Category[ \t]*:`)},
	{"media_link", regexp.MustCompile(`(?i)\[\[[ \t]*(File|Image)[ \t]*:`)},
	{"heading", regexp.MustCompile(`(?m)^[ \t]*={2,}[ \t]*[^=\r\n \t][^\r\n]*?={2,}[ \t]*\r?$`)},
	{"template_list", regexp.MustCompile(`(?m)^[ \t]*#+[ \t]*\{\{`)},
	{"nowiki_tag", regexp.MustCompile(`(?i)</?nowiki[ \t\r\n/>]`)},
	{"toc_directive", regexp.MustCompile(`(?i)__(NOTOC|TOC)__`)},
	{"table", regexp.MustCompile(`(?m)^[ \t]*\{\|`)},
}

// releaseNotesWikitext recognizes strong MediaWiki markers, not generic template,
// shell or identifier syntax. Release-note authoring paths share these rules.
func releaseNotesWikitext(input string) string {
	// Most release notes contain none of the markers, so avoid parsing those.
	candidate := false
	for _, rule := range releaseNotesWikitextRules {
		if rule.pattern.MatchString(input) {
			candidate = true
			break
		}
	}
	if !candidate {
		return ""
	}
	prose := releaseNotesWithoutCode(input)
	for _, rule := range releaseNotesWikitextRules {
		if rule.pattern.MatchString(prose) {
			return rule.kind
		}
	}
	return ""
}

// Mask code in place, preserving line boundaries and never joining text across
// a code span. Goldmark handles fence lengths, nesting and multiline code spans.
func releaseNotesWithoutCode(input string) string {
	source := []byte(input)
	doc := goldmark.New().Parser().Parse(text.NewReader(source))
	mask := func(segment text.Segment) {
		for i := segment.Start; i < segment.Stop; i++ {
			if source[i] != '\n' && source[i] != '\r' {
				source[i] = ' '
			}
		}
	}
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := node.(type) {
		case *ast.FencedCodeBlock:
			if node.Info != nil {
				mask(node.Info.Segment)
			}
		case *ast.CodeBlock:
		case *ast.CodeSpan:
			for child := node.FirstChild(); child != nil; child = child.NextSibling() {
				if text, ok := child.(*ast.Text); ok {
					mask(text.Segment)
				}
			}
			return ast.WalkSkipChildren, nil
		default:
			return ast.WalkContinue, nil
		}
		for i := 0; i < node.Lines().Len(); i++ {
			mask(node.Lines().At(i))
		}
		return ast.WalkSkipChildren, nil
	})
	return string(source)
}

// ValidateReleaseNotes checks API fields, database rows and section text leaves.
// Metadata is ignored; rejection reasons contain only a marker kind and field,
// never the submitted release text. Call before any direct SQL content write.
func ValidateReleaseNotes(fields map[string]any) error {
	textFields := map[string]any{}
	for _, key := range []string{"title", "summary", "sections", "bodyMarkdown", "body_markdown"} {
		textFields[key] = fields[key]
	}
	flat := content.Flatten(textFields)
	paths := make([]string, 0, len(flat))
	for path := range flat {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if kind := releaseNotesWikitext(flat[path]); kind != "" {
			return domain.RejectedDetail("release_notes_wikitext_convert_to_markdown", kind, path)
		}
	}
	return nil
}

func validateReleaseNotesHistory(state HistoryState) error {
	for _, table := range []string{"releases", "release_i18n"} {
		for _, row := range state[table] {
			if err := ValidateReleaseNotes(row); err != nil {
				return err
			}
		}
	}
	return nil
}
