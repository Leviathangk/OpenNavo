package articles

import (
	"bytes"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/net/html"
)

var (
	// appDirectiveLine is one ::app[token] line, including its line ending.
	appDirectiveLine = regexp.MustCompile(`(?m)^::app\[([a-z0-9][a-z0-9@+._-]*)\][ \t]*(?:\r?\n|$)`)
	// appPath is /apps/<token>, /<locale>/apps/<token> and any trailing path.
	appPath = regexp.MustCompile(`^/(?:[A-Za-z]{2}(?:-[A-Za-z0-9]{2,8})?/)?apps/([a-z0-9][a-z0-9@+._-]*)(?:/.*)?$`)
	// schemeLessAppURL is a bare OpenNavo URL without a scheme. Goldmark linkify handles http(s)://
	// and www. forms; this covers the scheme-less form used in existing article bodies.
	schemeLessAppURL = regexp.MustCompile(`(?i)(?:www\.)?opennavo\.com/(?:[A-Za-z]{2}(?:-[A-Za-z0-9]{2,8})?/)?apps/[^\s<>()\[\]{}"']+`)
	// appMarkdown parses with the same link syntax the article renderer uses.
	appMarkdown = goldmark.New(goldmark.WithExtensions(extension.Linkify))
)

// AppReferences lists the app tokens an article body references: ::app[token] cards, inline,
// autolinked and linkified app URLs, reference-style links and raw HTML anchors. Code is skipped,
// matching ScanBody.
func AppReferences(body string) []string {
	doc := parseAppDocument(body)
	seen := map[string]bool{}
	out := []string{}
	add := func(token string) {
		if token != "" && !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}
	for _, match := range appDirectiveLine.FindAllStringSubmatchIndex(body, -1) {
		if doc.inCode(match[0]) {
			continue
		}
		add(body[match[2]:match[3]])
	}
	for _, link := range doc.links {
		if token, ok := appToken(link.destination); ok {
			add(token)
		}
	}
	for _, definition := range doc.definitions {
		if token, ok := appToken(definition.destination); ok {
			add(token)
		}
	}
	for _, href := range doc.htmlAnchors(body) {
		if token, ok := appPathToken(href); ok {
			add(token)
		}
	}
	for _, match := range schemeLessAppURL.FindAllStringIndex(body, -1) {
		end := trimBareURLEnd(body, match[0], match[1])
		if !doc.rewritable(match[0]) || !bareURLStartOK(body, match[0]) {
			continue
		}
		if token, ok := appPathToken(body[match[0]:end]); ok {
			add(token)
		}
	}
	return out
}

// RedactPrivateAppReferences rewrites inline and reference-style private app links as plain text,
// removes private autolinks, linkified URLs, raw HTML anchors and ::app directive lines, and drops
// private reference definitions. Public and unknown references are kept, and code is left untouched.
func RedactPrivateAppReferences(body string, private map[string]bool) string {
	if body == "" || len(private) == 0 {
		return body
	}
	// Raw HTML anchors are inert in article Markdown, but strip them before parsing so their inner
	// text can still be parsed as links. The tokenizer looks at decoded attribute values only.
	text := redactHTMLAnchors(body, private)
	doc := parseAppDocument(text)
	edits := []appEdit{}

	for _, link := range doc.links {
		token, ok := appToken(link.destination)
		if !ok || !private[token] {
			continue
		}
		replacement := ""
		if !link.auto {
			replacement = text[link.text.start:link.text.stop]
		}
		edits = append(edits, appEdit{start: link.span.start, end: link.span.stop, replacement: replacement})
	}

	edits = append(edits, doc.definitionEdits(private)...)

	for _, match := range appDirectiveLine.FindAllStringSubmatchIndex(text, -1) {
		if doc.inCode(match[0]) || doc.inProtectedSpan(match[0]) {
			continue
		}
		if private[text[match[2]:match[3]]] {
			edits = append(edits, appEdit{start: match[0], end: match[1]})
		}
	}

	for _, match := range schemeLessAppURL.FindAllStringIndex(text, -1) {
		end := trimBareURLEnd(text, match[0], match[1])
		if !doc.rewritable(match[0]) || !bareURLStartOK(text, match[0]) {
			continue
		}
		token, ok := appPathToken(text[match[0]:end])
		if !ok || !private[token] {
			continue
		}
		edits = append(edits, appEdit{start: match[0], end: end})
	}

	return applyAppEdits(text, edits)
}

type sourceSpan struct {
	start int
	stop  int
}

type appLink struct {
	span        sourceSpan
	text        sourceSpan
	destination []byte
	auto        bool
}

type appDefinition struct {
	span        sourceSpan
	label       string
	rawLabel    []byte
	destination []byte
	title       []byte
}

type appDocument struct {
	source      []byte
	links       []appLink
	definitions []appDefinition // first definition per label, per CommonMark
	allDefs     []appDefinition
	code        []sourceSpan
	protected   []sourceSpan // complete link and image syntax
}

func parseAppDocument(body string) *appDocument {
	first := parseAppAST(body, nil)
	scanned := scanReferenceDefinitions(first.source, first.code)
	merged := mergeAppDefinitions(first.allDefs, scanned)
	unparsed := false
	for _, definition := range scanned {
		if !hasAppDefinitionAt(first.allDefs, definition.span.start) {
			unparsed = true
			break
		}
	}
	if !unparsed {
		first.allDefs = merged
		first.finishDefinitions()
		return first
	}
	// Goldmark, like CommonMark, does not treat a definition directly after a paragraph as a
	// definition. Existing article bodies rely on that form, so preload those references and parse
	// again: links now resolve, while code and block structure keep goldmark's interpretation.
	context := parser.NewContext()
	for _, definition := range merged {
		context.AddReference(parser.NewReference(definition.rawLabel, definition.destination, definition.title))
	}
	second := parseAppAST(body, context)
	second.allDefs = mergeAppDefinitions(second.allDefs, scanned)
	second.finishDefinitions()
	return second
}

func parseAppAST(body string, context parser.Context) *appDocument {
	doc := &appDocument{source: []byte(body)}
	options := []parser.ParseOption{}
	if context != nil {
		options = append(options, parser.WithContext(context))
	}
	ast.Walk(appMarkdown.Parser().Parse(text.NewReader(doc.source), options...), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.LinkReferenceDefinition:
			doc.allDefs = append(doc.allDefs, appDefinition{
				span:        definitionSpan(doc.source, n),
				label:       util.ToLinkReference(n.Label),
				rawLabel:    n.Label,
				destination: n.Destination,
				title:       n.Title,
			})
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			if span, ok := blockCodeSpan(doc.source, n); ok {
				doc.code = append(doc.code, span)
			}
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock:
			if span, ok := blockCodeSpan(doc.source, n); ok {
				doc.code = append(doc.code, span)
			}
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph:
			doc.collectInline(n, n.Lines())
			return ast.WalkSkipChildren, nil
		case *ast.Heading:
			doc.collectInline(n, n.Lines())
			return ast.WalkSkipChildren, nil
		case *ast.TextBlock:
			doc.collectInline(n, n.Lines())
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return doc
}

func (d *appDocument) finishDefinitions() {
	sort.SliceStable(d.allDefs, func(i, j int) bool { return d.allDefs[i].span.start < d.allDefs[j].span.start })
	seen := map[string]bool{}
	d.definitions = d.definitions[:0]
	for _, definition := range d.allDefs {
		if !seen[definition.label] {
			seen[definition.label] = true
			d.definitions = append(d.definitions, definition)
		}
	}
}

func mergeAppDefinitions(parsed, scanned []appDefinition) []appDefinition {
	merged := make([]appDefinition, 0, len(parsed)+len(scanned))
	seen := map[int]bool{}
	for _, definition := range append(append([]appDefinition{}, parsed...), scanned...) {
		if seen[definition.span.start] {
			continue
		}
		seen[definition.span.start] = true
		merged = append(merged, definition)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].span.start < merged[j].span.start })
	return merged
}

func hasAppDefinitionAt(definitions []appDefinition, start int) bool {
	for _, definition := range definitions {
		if definition.span.start == start {
			return true
		}
	}
	return false
}

// scanReferenceDefinitions finds definition-shaped lines even where CommonMark would not start a
// definition because the previous line is paragraph text. Existing article bodies use that form.
// Container prefixes are skipped, a destination may sit on the following line, and code is ignored.
func scanReferenceDefinitions(source []byte, code []sourceSpan) []appDefinition {
	definitions := []appDefinition{}
	for lineStart := 0; lineStart <= len(source); {
		lineEnd := len(source)
		next := len(source)
		if offset := bytes.IndexByte(source[lineStart:], '\n'); offset >= 0 {
			lineEnd = lineStart + offset
			next = lineEnd + 1
		}
		if !insideSourceSpans(lineStart, code) {
			if definition, ok := scanReferenceDefinition(source, lineStart, lineEnd); ok {
				definitions = append(definitions, definition)
				lineStart = definition.span.stop
				continue
			}
		}
		if next == len(source) {
			break
		}
		lineStart = next
	}
	return definitions
}

func scanReferenceDefinition(source []byte, lineStart, lineEnd int) (appDefinition, bool) {
	position := skipContainerPrefix(source, lineStart, lineEnd)
	if position >= lineEnd || source[position] != '[' {
		return appDefinition{}, false
	}
	labelStart := position + 1
	labelEnd := labelStart
	for labelEnd < lineEnd {
		if source[labelEnd] == '\\' && labelEnd+1 < lineEnd {
			labelEnd += 2
			continue
		}
		if source[labelEnd] == ']' {
			break
		}
		labelEnd++
	}
	if labelEnd >= lineEnd || labelEnd == labelStart || source[labelEnd] != ']' {
		return appDefinition{}, false
	}
	position = labelEnd + 1
	if position >= lineEnd || source[position] != ':' {
		return appDefinition{}, false
	}
	rawLabel := source[labelStart:labelEnd]
	position++
	for position < lineEnd && (source[position] == ' ' || source[position] == '\t') {
		position++
	}

	destinationEnd := lineEnd
	stop := lineEnd
	if position >= lineEnd || source[position] == '\r' {
		// A multiline destination starts on the next line.
		if lineEnd >= len(source) {
			return appDefinition{}, false
		}
		nextStart := lineEnd + 1
		nextEnd := len(source)
		if offset := bytes.IndexByte(source[nextStart:], '\n'); offset >= 0 {
			nextEnd = nextStart + offset
		}
		stop = nextEnd
		position = skipContainerPrefix(source, nextStart, nextEnd)
		destinationEnd = nextEnd
	}
	destination, position, ok := scanDefinitionDestination(source, position, destinationEnd)
	if !ok {
		return appDefinition{}, false
	}
	for position < destinationEnd && (source[position] == ' ' || source[position] == '\t' || source[position] == '\r') {
		position++
	}
	if position < destinationEnd {
		opener := source[position]
		if opener != '"' && opener != '\'' && opener != '(' {
			return appDefinition{}, false
		}
		closer := opener
		if opener == '(' {
			closer = ')'
		}
		position++
		for position < destinationEnd {
			if source[position] == '\\' && position+1 < destinationEnd {
				position += 2
				continue
			}
			if source[position] == closer {
				position++
				break
			}
			position++
		}
		for position < destinationEnd && (source[position] == ' ' || source[position] == '\t' || source[position] == '\r') {
			position++
		}
		if position != destinationEnd {
			return appDefinition{}, false
		}
	}
	return appDefinition{
		span:        sourceSpan{start: lineStart, stop: includeLineEnding(source, stop)},
		label:       util.ToLinkReference(rawLabel),
		rawLabel:    rawLabel,
		destination: destination,
	}, true
}

func scanDefinitionDestination(source []byte, position, limit int) ([]byte, int, bool) {
	if position >= limit {
		return nil, position, false
	}
	if source[position] == '<' {
		start := position + 1
		position++
		for position < limit {
			if source[position] == '\\' && position+1 < limit {
				position += 2
				continue
			}
			if source[position] == '>' {
				return source[start:position], position + 1, true
			}
			position++
		}
		return nil, position, false
	}
	start := position
	depth := 0
	for position < limit {
		switch source[position] {
		case '\\':
			if position+1 < limit {
				position += 2
				continue
			}
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return source[start:position], position, true
			}
			depth--
		case ' ', '\t', '\r':
			if depth == 0 {
				return source[start:position], position, true
			}
		}
		position++
	}
	if position == start {
		return nil, position, false
	}
	return source[start:position], position, true
}

func skipContainerPrefix(source []byte, start, end int) int {
	position := start
	for {
		for position < end && (source[position] == ' ' || source[position] == '\t') {
			position++
		}
		if position >= end {
			return position
		}
		switch source[position] {
		case '>':
			position++
			continue
		case '-', '*', '+':
			if position+1 < end && (source[position+1] == ' ' || source[position+1] == '\t') {
				position++
				continue
			}
		default:
			if source[position] >= '0' && source[position] <= '9' {
				number := position
				for number < end && source[number] >= '0' && source[number] <= '9' {
					number++
				}
				if number < end && (source[number] == '.' || source[number] == ')') &&
					number+1 < end && (source[number+1] == ' ' || source[number+1] == '\t') {
					position = number + 1
					continue
				}
			}
			return position
		}
		return position
	}
}

func (d *appDocument) collectInline(parent ast.Node, lines *text.Segments) {
	if lines == nil || lines.Len() == 0 {
		return
	}
	cursor := lines.At(0).Start
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		d.collectInlineNode(child, &cursor)
	}
}

func (d *appDocument) collectInlineNode(node ast.Node, cursor *int) {
	switch n := node.(type) {
	case *ast.Text:
		if n.Segment.Stop > *cursor {
			*cursor = n.Segment.Stop
		}
	case *ast.CodeSpan:
		if span, ok := codeSpanContent(n); ok {
			d.code = append(d.code, span)
			if span.stop > *cursor {
				*cursor = span.stop
			}
		}
	case *ast.RawHTML:
		if n.Segments != nil {
			for i := 0; i < n.Segments.Len(); i++ {
				if segment := n.Segments.At(i); segment.Stop > *cursor {
					*cursor = segment.Stop
				}
			}
		}
	case *ast.AutoLink:
		span, ok := autoLinkSpan(d.source, n, *cursor)
		if !ok {
			return
		}
		d.protected = append(d.protected, span)
		d.links = append(d.links, appLink{span: span, destination: n.URL(d.source), auto: true})
		if span.stop > *cursor {
			*cursor = span.stop
		}
	case *ast.Link:
		span, label, ok := markdownLinkSpan(d.source, n, *cursor)
		if !ok {
			return
		}
		d.collectNestedCode(n)
		d.protected = append(d.protected, span)
		d.links = append(d.links, appLink{span: span, text: label, destination: n.Destination})
		if span.stop > *cursor {
			*cursor = span.stop
		}
	case *ast.Image:
		span, _, ok := markdownLinkSpan(d.source, n, *cursor)
		if !ok {
			return
		}
		d.collectNestedCode(n)
		d.protected = append(d.protected, span)
		if span.stop > *cursor {
			*cursor = span.stop
		}
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			d.collectInlineNode(child, cursor)
		}
	}
}

// collectNestedCode keeps inline code inside a link or image label out of the HTML tokenizer and
// directive scanner without treating the label as separately parsed block content.
func (d *appDocument) collectNestedCode(parent ast.Node) {
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if code, ok := child.(*ast.CodeSpan); ok {
			if span, ok := codeSpanContent(code); ok {
				d.code = append(d.code, span)
			}
		}
		d.collectNestedCode(child)
	}
}

func (d *appDocument) definitionEdits(private map[string]bool) []appEdit {
	privateLabels := map[string]bool{}
	for _, definition := range d.definitions {
		if token, ok := appToken(definition.destination); ok && private[token] {
			privateLabels[definition.label] = true
		}
	}
	edits := []appEdit{}
	for _, definition := range d.allDefs {
		if privateLabels[definition.label] {
			edits = append(edits, appEdit{start: definition.span.start, end: definition.span.stop})
		}
	}
	return edits
}

func (d *appDocument) inCode(position int) bool {
	return insideSourceSpans(position, d.code)
}

func (d *appDocument) inProtectedSpan(position int) bool {
	return insideSourceSpans(position, d.protected)
}

// rewritable rejects positions inside code or an existing link/image, where a bare URL is either
// literal code or part of a larger destination.
func (d *appDocument) rewritable(position int) bool {
	return !d.inCode(position) && !d.inProtectedSpan(position)
}

func insideSourceSpans(position int, spans []sourceSpan) bool {
	for _, span := range spans {
		if position >= span.start && position < span.stop {
			return true
		}
	}
	return false
}

type appEdit struct {
	start, end  int
	replacement string
}

func applyAppEdits(source string, edits []appEdit) string {
	if len(edits) == 0 {
		return source
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	var out strings.Builder
	last := 0
	for _, edit := range edits {
		if edit.start < last || edit.start < 0 || edit.end > len(source) || edit.end < edit.start {
			continue
		}
		out.WriteString(source[last:edit.start])
		out.WriteString(edit.replacement)
		last = edit.end
	}
	out.WriteString(source[last:])
	return out.String()
}

// redactHTMLAnchors drops raw HTML anchors to private apps and keeps their inner text. Markdown
// rendering escapes raw HTML in article bodies, so this is a guard rather than a live-link fix.
func redactHTMLAnchors(body string, private map[string]bool) string {
	doc := parseAppDocument(body)
	edits := []appEdit{}
	for _, segment := range outsideSpans(body, doc.code) {
		edits = append(edits, htmlAnchorEdits(body, segment, private)...)
	}
	return applyAppEdits(body, edits)
}

func htmlAnchorEdits(source string, segment sourceSpan, private map[string]bool) []appEdit {
	tokenizer := html.NewTokenizer(strings.NewReader(source[segment.start:segment.stop]))
	type anchor struct {
		start, innerStart int
		private           bool
	}
	stack := []anchor{}
	edits := []appEdit{}
	offset := segment.start
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		start := offset
		offset += len(tokenizer.Raw())
		switch kind {
		case html.StartTagToken:
			name, hasAttributes := tokenizer.TagName()
			if string(name) != "a" {
				continue
			}
			href := ""
			for hasAttributes {
				key, value, more := tokenizer.TagAttr()
				if string(key) == "href" {
					href = string(value)
				}
				hasAttributes = more
			}
			token, ok := appPathToken(href)
			stack = append(stack, anchor{start: start, innerStart: offset, private: ok && private[token]})
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			if string(name) != "a" || len(stack) == 0 {
				continue
			}
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if current.private {
				edits = append(edits, appEdit{
					start:       current.start,
					end:         offset,
					replacement: source[current.innerStart:start],
				})
			}
		}
	}
	return edits
}

// htmlAnchors lists the decoded href values of raw HTML anchors outside code.
func (d *appDocument) htmlAnchors(body string) []string {
	hrefs := []string{}
	for _, segment := range outsideSpans(body, d.code) {
		tokenizer := html.NewTokenizer(strings.NewReader(body[segment.start:segment.stop]))
		for {
			kind := tokenizer.Next()
			if kind == html.ErrorToken {
				break
			}
			if kind != html.StartTagToken {
				continue
			}
			name, hasAttributes := tokenizer.TagName()
			if string(name) != "a" {
				continue
			}
			for hasAttributes {
				key, value, more := tokenizer.TagAttr()
				if string(key) == "href" {
					hrefs = append(hrefs, string(value))
				}
				hasAttributes = more
			}
		}
	}
	return hrefs
}

func outsideSpans(source string, spans []sourceSpan) []sourceSpan {
	if len(spans) == 0 {
		return []sourceSpan{{start: 0, stop: len(source)}}
	}
	sorted := append([]sourceSpan(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	out := []sourceSpan{}
	position := 0
	for _, span := range sorted {
		start, stop := span.start, span.stop
		if start < 0 {
			start = 0
		}
		if stop > len(source) {
			stop = len(source)
		}
		if start > position {
			out = append(out, sourceSpan{start: position, stop: start})
		}
		if stop > position {
			position = stop
		}
	}
	if position < len(source) {
		out = append(out, sourceSpan{start: position, stop: len(source)})
	}
	return out
}

func blockCodeSpan(source []byte, node ast.Node) (sourceSpan, bool) {
	lines := node.Lines()
	if lines == nil || lines.Len() == 0 {
		return sourceSpan{}, false
	}
	span := sourceSpan{start: lineStart(source, lines.At(0).Start), stop: lines.At(lines.Len() - 1).Stop}
	span.stop = includeLineEnding(source, span.stop)
	return span, span.stop > span.start
}

func codeSpanContent(node *ast.CodeSpan) (sourceSpan, bool) {
	span := sourceSpan{start: -1, stop: -1}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		textNode, ok := child.(*ast.Text)
		if !ok {
			continue
		}
		if span.start < 0 || textNode.Segment.Start < span.start {
			span.start = textNode.Segment.Start
		}
		if textNode.Segment.Stop > span.stop {
			span.stop = textNode.Segment.Stop
		}
	}
	if span.start < 0 || span.stop <= span.start {
		return sourceSpan{}, false
	}
	return span, true
}

func definitionSpan(source []byte, node *ast.LinkReferenceDefinition) sourceSpan {
	lines := node.Lines()
	if lines == nil || lines.Len() == 0 {
		return sourceSpan{}
	}
	span := sourceSpan{start: lineStart(source, lines.At(0).Start), stop: lines.At(lines.Len() - 1).Stop}
	span.stop = includeLineEnding(source, span.stop)
	return span
}

func lineStart(source []byte, position int) int {
	if position <= 0 {
		return 0
	}
	if index := bytes.LastIndexByte(source[:position], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

func includeLineEnding(source []byte, position int) int {
	if position < len(source) && source[position] == '\r' {
		position++
	}
	if position < len(source) && source[position] == '\n' {
		position++
	}
	return position
}

// autoLinkSpan recovers the source range of a goldmark AutoLink. Explicit autolinks keep their
// angle brackets; linkified bare URLs use the text itself.
func autoLinkSpan(source []byte, node *ast.AutoLink, cursor int) (sourceSpan, bool) {
	label := node.Label(source)
	if len(label) == 0 || cursor < 0 || cursor > len(source) {
		return sourceSpan{}, false
	}
	index := bytes.Index(source[cursor:], label)
	if index < 0 {
		return sourceSpan{}, false
	}
	start := cursor + index
	stop := start + len(label)
	if start > 0 && source[start-1] == '<' && stop < len(source) && source[stop] == '>' {
		start--
		stop++
	}
	return sourceSpan{start: start, stop: stop}, true
}

// markdownLinkSpan recovers a complete inline or reference link from its label text. Goldmark does
// not expose syntax offsets, so the label is located through its text segments and the destination
// is scanned with CommonMark bracket, escape and title rules.
func markdownLinkSpan(source []byte, node ast.Node, cursor int) (sourceSpan, sourceSpan, bool) {
	innerStart := firstTextStart(node)
	start := -1
	if innerStart >= 0 {
		start = linkOpen(source, innerStart)
	}
	if start < 0 {
		index := bytes.IndexByte(source[cursor:], '[')
		if index < 0 {
			return sourceSpan{}, sourceSpan{}, false
		}
		start = cursor + index
	}
	labelEnd := matchBracket(source, start)
	if labelEnd < 0 {
		return sourceSpan{}, sourceSpan{}, false
	}
	stop := labelEnd + 1
	if stop < len(source) && source[stop] == '(' {
		if end := inlineLinkEnd(source, stop); end > stop {
			stop = end
		}
	} else if stop < len(source) && source[stop] == '[' {
		if end := matchBracket(source, stop); end >= stop {
			stop = end + 1
		}
	}
	return sourceSpan{start: start, stop: stop}, sourceSpan{start: start + 1, stop: labelEnd}, true
}

func firstTextStart(node ast.Node) int {
	start := -1
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if textNode, ok := child.(*ast.Text); ok && (start < 0 || textNode.Segment.Start < start) {
			start = textNode.Segment.Start
		}
		if childStart := firstTextStart(child); childStart >= 0 && (start < 0 || childStart < start) {
			start = childStart
		}
	}
	return start
}

func linkOpen(source []byte, innerStart int) int {
	position := innerStart
	for position > 0 {
		index := bytes.LastIndexByte(source[:position], '[')
		if index < 0 {
			return -1
		}
		if index > 0 && (source[index-1] == '\\' || source[index-1] == '!') {
			position = index
			continue
		}
		return index
	}
	return -1
}

func matchBracket(source []byte, open int) int {
	depth := 0
	for index := open; index < len(source); index++ {
		switch source[index] {
		case '\\':
			if index+1 < len(source) {
				index++
			}
		case '`':
			length := backtickRun(source[index:])
			closeAt := -1
			for scan := index + length; scan < len(source); {
				if source[scan] != '`' {
					scan++
					continue
				}
				run := backtickRun(source[scan:])
				if run == length {
					closeAt = scan + run
					break
				}
				scan += run
			}
			if closeAt >= 0 {
				index = closeAt - 1
			} else {
				index += length - 1
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func inlineLinkEnd(source []byte, openParen int) int {
	position := skipSpaces(source, openParen+1)
	if position >= len(source) {
		return -1
	}
	if source[position] == '<' {
		position++
		for position < len(source) {
			if source[position] == '\\' && position+1 < len(source) {
				position += 2
				continue
			}
			if source[position] == '>' {
				position++
				break
			}
			position++
		}
		if position == 0 || source[position-1] != '>' {
			return -1
		}
	} else {
		depth := 0
		for position < len(source) {
			switch source[position] {
			case '\\':
				if position+1 < len(source) {
					position += 2
					continue
				}
			case '(':
				depth++
			case ')':
				if depth == 0 {
					goto destination
				}
				depth--
			default:
				if isMarkdownSpace(source[position]) {
					goto destination
				}
			}
			position++
		}
	}
destination:
	position = skipSpaces(source, position)
	if position < len(source) && (source[position] == '"' || source[position] == '\'' || source[position] == '(') {
		position = skipLinkTitle(source, position)
		if position < 0 {
			return -1
		}
		position = skipSpaces(source, position)
	}
	if position < len(source) && source[position] == ')' {
		return position + 1
	}
	return -1
}

func skipLinkTitle(source []byte, position int) int {
	opener := source[position]
	closer := opener
	if opener == '(' {
		closer = ')'
	}
	for index := position + 1; index < len(source); index++ {
		if source[index] == '\\' && index+1 < len(source) {
			index++
			continue
		}
		if source[index] == closer {
			return index + 1
		}
	}
	return -1
}

func skipSpaces(source []byte, position int) int {
	for position < len(source) && isMarkdownSpace(source[position]) {
		position++
	}
	return position
}

func isMarkdownSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func backtickRun(source []byte) int {
	length := 0
	for length < len(source) && source[length] == '`' {
		length++
	}
	return length
}

// bareURLStartOK keeps a hostname suffix such as notopennavo.com from matching opennavo.com, and
// rejects a scheme-less candidate embedded after another URL.
func bareURLStartOK(text string, start int) bool {
	if start == 0 {
		return true
	}
	switch c := text[start-1]; {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return false
	case c == '.', c == '-', c == '_', c == '/', c == ':', c == '@', c == '%', c == '+':
		return false
	default:
		return true
	}
}

// trimBareURLEnd removes sentence punctuation that linkify leaves outside the link.
func trimBareURLEnd(text string, start, end int) int {
	for end > start {
		switch text[end-1] {
		case '.', ',', ';', ':', '!', '?':
			end--
		default:
			return end
		}
	}
	return end
}

// appToken resolves Markdown escapes and character references, then matches the URL against the app
// page shape. It is shared by discovery and redaction so both see the same destination.
func appToken(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	return appPathToken(string(util.URLEscape(raw, true)))
}

func appPathToken(raw string) (string, bool) {
	// Linkify turns a bare opennavo.com URL into a link without changing its text.
	lower := strings.ToLower(raw)
	if !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "/") &&
		(strings.HasPrefix(lower, "opennavo.com/") || strings.HasPrefix(lower, "www.opennavo.com/")) {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "" || parsed.Host != "" {
		if parsed.Scheme == "" {
			return "", false
		}
		switch scheme := strings.ToLower(parsed.Scheme); scheme {
		case "https":
			if port := parsed.Port(); port != "" && port != "443" {
				return "", false
			}
		case "http":
			if port := parsed.Port(); port != "" && port != "80" {
				return "", false
			}
		default:
			return "", false
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "opennavo.com" && host != "www.opennavo.com" {
			return "", false
		}
	} else if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", false
	}
	match := appPath.FindStringSubmatch(parsed.Path)
	if match == nil {
		return "", false
	}
	return match[1], true
}
