package llm

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	lingua "github.com/pemistahl/lingua-go"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

type ContentInput struct {
	RefID        int64                        `json:"-"`
	SourceLocale string                       `json:"sourceLocale"`
	Targets      []string                     `json:"targets"`
	Fields       map[string]string            `json:"fields"`
	Limits       map[string]int               `json:"limits"`
	Protected    []string                     `json:"protected"`
	Glossary     map[string]map[string]string `json:"glossary"`
	Strict       bool                         `json:"strict"`
}
type ContentOutput map[string]map[string]string

var literals = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`\\n]+`|https?://[^\\s<>\\)\\]，。；！？、\\p{Han}\\p{Hiragana}\\p{Katakana}\\x{3000}-\\x{303F}\\x{FF00}-\\x{FFEF}]+|(?:[A-Za-z]:)?/[^\\s<>\\)\\]，。；！？、\\p{Han}\\p{Hiragana}\\p{Katakana}\\x{3000}-\\x{303F}\\x{FF00}-\\x{FFEF}]+|\\b[A-Z][A-Z0-9_]*_[A-Z0-9_]+\\b|\\bv?\\d+\\.\\d+(?:\\.[A-Za-z0-9]+)*(?:[-+][A-Za-z0-9.]+)?\\b|\\$\\{[^}]+\\}|\\{[A-Za-z_][A-Za-z0-9_]*\\}")
var markdownLinks = regexp.MustCompile(`!?\[[^\]]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)`)
var commandLines = regexp.MustCompile(`(?m)^\s*(?:\$ |brew |sudo |curl |git |npm |pnpm |uv |python3 |docker ).+$`)

// A slash part attached to a word stays a literal only if it looks like a path:
// a second slash, a file extension or path punctuation. "src/a.ts", "docs/help.md"
// and "/Applications/Orbit.app" are paths; "desktop/web", "and/or" and "X/Y" are
// prose a translator may rephrase.
var pathExtension = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)

func literalIndexes(s string) [][]int {
	out := [][]int{}
	for _, m := range literals.FindAllStringIndex(s, -1) {
		v := s[m[0]:m[1]]
		if strings.HasPrefix(v, "/") && m[0] > 0 {
			r, _ := utf8.DecodeLastRuneInString(s[:m[0]])
			attached := unicode.IsLetter(r) || unicode.IsDigit(r)
			if attached && strings.Count(v, "/") < 2 && !pathExtension.MatchString(strings.TrimRight(v, ".,，。；;！!")) && !strings.ContainsAny(v, "{}_=?#@:%\\") {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

func LiteralSpans(s string) []string {
	out := []string{}
	for _, m := range literalIndexes(s) {
		out = append(out, s[m[0]:m[1]])
	}
	out = append(out, commandLines.FindAllString(s, -1)...)
	for _, link := range markdownLinks.FindAllStringSubmatch(s, -1) {
		out = append(out, "link:"+link[1])
	}
	for i, v := range out {
		if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "http") {
			out[i] = strings.TrimRight(v, ".,，。；;！!")
		}
	}
	sort.Strings(out)
	return out
}
func stripLiterals(s string, protected []string) string {
	var b strings.Builder
	last := 0
	for _, m := range literalIndexes(s) {
		b.WriteString(s[last:m[0]])
		last = m[1]
	}
	b.WriteString(s[last:])
	s = b.String()
	s = commandLines.ReplaceAllString(s, "")
	for _, p := range protected {
		spans := protectedTermSpans(s, p)
		for i := len(spans) - 1; i >= 0; i-- {
			s = s[:spans[i][0]] + s[spans[i][1]:]
		}
	}
	return strings.TrimSpace(s)
}

// Match complete brand names, not substrings such as "arc" in "search".
// Han and kana prose can adjoin a brand without spaces. Preserve exact case.
func protectedTermSpans(text, term string) [][2]int {
	var spans [][2]int
	if term == "" {
		return spans
	}
	wordRune := func(r rune) bool {
		return r == '_' || unicode.IsDigit(r) || unicode.IsMark(r) ||
			(unicode.IsLetter(r) && !unicode.Is(unicode.Han, r) &&
				!unicode.Is(unicode.Hiragana, r) && !unicode.Is(unicode.Katakana, r))
	}
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], term)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(term)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if (start == 0 || !wordRune(before)) && (end == len(text) || !wordRune(after)) {
			spans = append(spans, [2]int{start, end})
		}
		offset = end
	}
	return spans
}

func contentFailure(reason string) error { return &Failure{Reason: reason} }

// ContentViolation names one failed check, so a translator can correct exactly
// that field. Spans and terms are bounded copies of text the caller already holds:
// the source it read and the target it wrote.
type ContentViolation struct {
	Rule          string   `json:"rule"`
	Field         string   `json:"field,omitempty"`
	Missing       []string `json:"missing,omitempty"`
	Extra         []string `json:"extra,omitempty"`
	Term          string   `json:"term,omitempty"`
	Expected      string   `json:"expected,omitempty"`
	ExpectedCount *int     `json:"expectedCount,omitempty"`
	ActualCount   *int     `json:"actualCount,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	Length        int      `json:"length,omitempty"`
	SourceLength  int      `json:"sourceLength,omitempty"`
}

const (
	maxViolations     = 20
	maxViolationSpans = 10
	maxViolationText  = 200
)

// spanDiff compares two multisets of spans: what the source has that the target
// lacks, and what the target added.
func spanDiff(source, target []string) (missing, extra []string) {
	counts := map[string]int{}
	for _, v := range source {
		counts[v]++
	}
	for _, v := range target {
		counts[v]--
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for n := counts[k]; n > 0 && len(missing) < maxViolationSpans; n-- {
			missing = append(missing, Truncate(k, maxViolationText))
		}
		for n := counts[k]; n < 0 && len(extra) < maxViolationSpans; n++ {
			extra = append(extra, Truncate(k, maxViolationText))
		}
	}
	return missing, extra
}

// ValidateContent checks every target locale and field and reports all
// violations. Fields are checked in key order, so the reported Reason (the first
// violation) is stable between attempts.
func ValidateContent(out ContentOutput, in ContentInput) error {
	if !i18n.Valid(in.SourceLocale) || len(out) != len(in.Targets) {
		return contentFailure("invalid_schema")
	}
	keys := make([]string, 0, len(in.Fields))
	for key := range in.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var found []ContentViolation
	add := func(v ContentViolation) {
		if len(found) < maxViolations {
			found = append(found, v)
		}
	}
	for _, locale := range in.Targets {
		fields, ok := out[locale]
		if !ok || !i18n.Valid(locale) || locale == in.SourceLocale || len(fields) != len(in.Fields) {
			return contentFailure("invalid_schema")
		}
		var sourceText, targetText strings.Builder
		for _, key := range keys {
			source := in.Fields[key]
			target, ok := fields[key]
			if !ok || strings.TrimSpace(target) == "" {
				add(ContentViolation{Rule: "missing_field", Field: key})
				continue
			}
			if maximum := in.Limits[key]; maximum > 0 && utf8.RuneCountInString(target) > maximum {
				add(ContentViolation{Rule: "too_long", Field: key, Limit: maximum, Length: utf8.RuneCountInString(target)})
			}
			if want, got := LiteralSpans(source), LiteralSpans(target); !reflect.DeepEqual(want, got) {
				missing, extra := spanDiff(want, got)
				add(ContentViolation{Rule: "changed_literal", Field: key, Missing: missing, Extra: extra})
			}
			if in.Strict {
				if want, got := markdownStructure(source), markdownStructure(target); !reflect.DeepEqual(want, got) {
					missing, extra := spanDiff(want, got)
					add(ContentViolation{Rule: "changed_structure", Field: key, Missing: missing, Extra: extra})
				}
				n, m := utf8.RuneCountInString(stripLiterals(source, in.Protected)), utf8.RuneCountInString(stripLiterals(target, in.Protected))
				if n >= 20 && (m*5 < n || m > n*5) {
					add(ContentViolation{Rule: "length_ratio", Field: key, SourceLength: n, Length: m})
				}
			}
			for _, term := range in.Protected {
				if want, got := len(protectedTermSpans(source, term)), len(protectedTermSpans(target, term)); term != "" && want != got {
					add(ContentViolation{Rule: "changed_brand", Field: key, Term: Truncate(term, maxViolationText), ExpectedCount: &want, ActualCount: &got})
				}
			}
			terms := make([]string, 0, len(in.Glossary))
			for term := range in.Glossary {
				terms = append(terms, term)
			}
			sort.Strings(terms)
			for _, term := range terms {
				if translated := in.Glossary[term][locale]; translated != "" && strings.Contains(source, term) && !strings.Contains(target, translated) {
					add(ContentViolation{Rule: "missing_glossary", Field: key, Term: Truncate(term, maxViolationText), Expected: Truncate(translated, maxViolationText)})
				}
			}
			sourceText.WriteString(stripLiterals(source, in.Protected))
			sourceText.WriteByte(' ')
			targetText.WriteString(stripLiterals(target, in.Protected))
			targetText.WriteByte(' ')
		}
		src, dst := strings.TrimSpace(sourceText.String()), strings.TrimSpace(targetText.String())
		if src != "" && src == dst {
			add(ContentViolation{Rule: "untranslated"})
		} else if src != "" && !MatchesLanguage(dst, locale) {
			add(ContentViolation{Rule: "wrong_language"})
		}
	}
	if len(found) > 0 {
		return &Failure{Reason: found[0].Rule, Violations: found}
	}
	return nil
}

var latinDetector = lingua.NewLanguageDetectorBuilder().FromLanguages(lingua.English, lingua.Spanish, lingua.Portuguese, lingua.French, lingua.German, lingua.Italian).WithMinimumRelativeDistance(0.1).Build()

// Validate character scripts first, then distinguish Latin languages with the offline model; reject uncertainty and retry strictly.
func MatchesLanguage(text, locale string) bool {
	han, kana, cyrillic, latin := 0, 0, 0, 0
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	switch locale {
	case "zh-CN", "zh-Hant":
		return han > 0 && kana == 0 && cyrillic == 0
	case "ja-JP":
		return (kana > 0 || han > 0) && cyrillic == 0
	case "ru-RU":
		return cyrillic > 0 && han == 0 && kana == 0
	}
	if han+kana+cyrillic > 0 || latin == 0 {
		return false
	}
	language, confident := latinDetector.DetectLanguageOf(text)
	expected, ok := map[string]lingua.Language{"en-US": lingua.English, "es-ES": lingua.Spanish, "pt-BR": lingua.Portuguese, "de-DE": lingua.German, "fr-FR": lingua.French, "it-IT": lingua.Italian}[locale]
	return ok && confident && language == expected
}

// Compare structural markers without comparing translated words or line wrapping.
var markdownMarker = regexp.MustCompile(`^(#{1,6})[ \t]+|^([ \t]*)(?:[-+*]|[0-9]+[.)])[ \t]+|^(>+)[ \t]?|^([ \t]*(?:---+|===+))[ \t]*$`)

func markdownStructure(value string) []string {
	var out []string
	fence := ""
	for _, line := range strings.Split(value, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			marker := trim[:3]
			if fence == "" {
				fence = marker
			} else if marker == fence {
				fence = ""
			}
			out = append(out, "fence:"+marker)
			continue
		}
		if fence != "" {
			continue
		}
		if match := markdownMarker.FindStringSubmatch(line); match != nil {
			switch {
			case match[1] != "":
				out = append(out, match[1])
			case match[3] != "":
				out = append(out, match[3])
			case match[4] != "":
				out = append(out, "rule")
			default:
				out = append(out, "list:"+match[2])
			}
		}
		if strings.HasPrefix(trim, "|") && strings.HasSuffix(trim, "|") {
			out = append(out, strings.Repeat("|", strings.Count(trim, "|")))
		}
	}
	return out
}
