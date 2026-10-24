// Package traditional derives Taiwan Traditional Chinese from Simplified Chinese.
// The same pinned converter is used by the API, worker and repository generator.
package traditional

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const Version = "opencc-data-1.1.9-s2twp-v4"
const Locale = "zh-Hant"
const Source = "zh-CN"

var converter = sync.OnceValues(loadDictionaries)

// Markdown syntax and code are preserved by selecting only AST prose segments.
// Other protected literals are excluded globally across text-node boundaries.
const literalPattern = `\{[^{}]*\}|[A-Za-z][A-Za-z0-9+.-]*://[^\s<>]+|<[^>]+>|@:[A-Za-z0-9_.]+`

func Text(input string, protected []string) (string, error) {
	c, err := converter()
	if err != nil {
		return "", err
	}
	terms := append([]string{}, protected...)
	sort.Slice(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
	pattern := literalPattern
	for _, term := range terms {
		if term != "" {
			pattern += "|" + regexp.QuoteMeta(term)
		}
	}
	literals := regexp.MustCompile(pattern).FindAllStringIndex(input, -1)
	var out strings.Builder
	cursor := 0
	for _, span := range proseSpans(input) {
		out.WriteString(input[cursor:span[0]])
		start := span[0]
		for _, literal := range literals {
			if literal[1] <= start {
				continue
			}
			if literal[0] >= span[1] {
				break
			}
			left, right := max(start, literal[0]), min(span[1], literal[1])
			part, err := c.Convert(input[start:left])
			if err != nil {
				return "", err
			}
			out.WriteString(part)
			out.WriteString(input[left:right])
			start = right
		}
		part, err := c.Convert(input[start:span[1]])
		if err != nil {
			return "", err
		}
		out.WriteString(part)
		cursor = span[1]
	}
	out.WriteString(input[cursor:])
	return out.String(), nil
}

func Fields(fields map[string]any, protected []string) (map[string]any, error) {
	var walk func(any) (any, error)
	walk = func(value any) (any, error) {
		switch v := value.(type) {
		case string:
			return Text(v, protected)
		case []any:
			result := make([]any, len(v))
			for i, x := range v {
				y, err := walk(x)
				if err != nil {
					return nil, err
				}
				result[i] = y
			}
			return result, nil
		case map[string]any:
			result := map[string]any{}
			for k, x := range v {
				y, err := walk(x)
				if err != nil {
					return nil, err
				}
				result[k] = y
			}
			return result, nil
		default:
			return value, nil
		}
	}
	result, err := walk(fields)
	if err != nil {
		return nil, err
	}
	return result.(map[string]any), nil
}

// Copy the entire source verbatim except for visible prose. Fence delimiters,
// code spans, link destinations and reference definitions are never candidates
// for conversion, so syntax cannot interfere with a later literal's boundaries.
func proseSpans(input string) [][]int {
	source := []byte(input)
	doc := goldmark.New().Parser().Parse(text.NewReader(source))
	spans := [][]int{}
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.Kind() {
		case ast.KindFencedCodeBlock, ast.KindCodeBlock, ast.KindCodeSpan, ast.KindAutoLink, ast.KindRawHTML, ast.KindHTMLBlock:
			return ast.WalkSkipChildren, nil
		}
		// Implicit reference labels are lookup keys and must match their definitions.
		switch n := node.(type) {
		case *ast.Link:
			if n.Reference != nil && n.Reference.Type != ast.ReferenceLinkFull {
				return ast.WalkSkipChildren, nil
			}
		case *ast.Image:
			if n.Reference != nil && n.Reference.Type != ast.ReferenceLinkFull {
				return ast.WalkSkipChildren, nil
			}
		case *ast.Text:
			spans = append(spans, []int{n.Segment.Start, n.Segment.Stop})
		}
		return ast.WalkContinue, nil
	})
	return spans
}
