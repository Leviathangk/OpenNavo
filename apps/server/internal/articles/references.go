package articles

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/net/html"
)

var (
	// appDirectiveLine is one ::app[token] line, including its line ending.
	appDirectiveLine = regexp.MustCompile(`(?m)^::app\[([a-z0-9][a-z0-9@+._-]*)\][ \t]*(?:\r?\n|$)`)
	// appPath matches /apps/<token> or /<locale>/apps/<token> with optional trailing path.
	appPath = regexp.MustCompile(`(?i)^/(?:[a-z]{2}(?:-[a-z0-9]{2,8})?/)?apps/([a-z0-9][a-z0-9@+._-]*)(?:/.*)?$`)
	// schemeLessAppURL covers bare opennavo.com / www.opennavo.com app URLs that linkify does not catch.
	schemeLessAppURL = regexp.MustCompile(`(?i)(?:www\.)?opennavo\.com/(?:[A-Za-z]{2}(?:-[A-Za-z0-9]{2,8})?/)?apps/[^\s<>()\[\]{}"']+`)
	// plainAppURL finds absolute or site-relative app URLs in plain text (invalid ref-def lines, etc.).
	plainAppURL = regexp.MustCompile(`(?i)(?:(?:https?://)?(?:www\.)?opennavo\.com(?::\d+)?)?/(?:[a-z]{2}(?:-[a-z0-9]{2,8})?/)?apps/[a-z0-9][a-z0-9@+._-]*(?:/[^\s<>()\[\]{}"']*)?`)
	// linkifyURLRegexp mirrors markdown-it linkify: case-insensitive scheme/host, optional port.
	linkifyURLRegexp = regexp.MustCompile(`(?i)^(?:https?|ftp)://[-a-zA-Z0-9@:%._+~#=]{1,256}\.[a-zA-Z]+(?::\d+)?(?:[/#?][-a-zA-Z0-9@:%_+.~#$!?&/=();,'">\^{}\[\]` + "`" + `]*)?`)
	linkifyWWWRegexp = regexp.MustCompile(`(?i)^www\.[-a-zA-Z0-9@:%._+~#=]{1,256}\.[a-zA-Z]+(?:[/#?][-a-zA-Z0-9@:%_+.~#!?&/=();,'">\^{}\[\]` + "`" + `]*)?`)
	appMarkdown      = goldmark.New(goldmark.WithExtensions(extension.NewLinkify(
		extension.WithLinkifyAllowedProtocols([]string{
			"http:", "https:", "ftp:",
			"HTTP:", "HTTPS:", "FTP:",
			"Http:", "Https:", "Ftp:",
		}),
		extension.WithLinkifyURLRegexp(linkifyURLRegexp),
		extension.WithLinkifyWWWRegexp(linkifyWWWRegexp),
	)))
)

// AppReferences lists the app tokens an article body references: ::app[token] cards, inline,
// autolinked and linkified app URLs, reference-style links and raw HTML anchors. Code is skipped,
// matching ScanBody.
func AppReferences(body string) []string {
	source := []byte(body)
	doc := parseAppMarkdown(source)
	seen := map[string]bool{}
	out := []string{}
	add := func(token string) {
		if token != "" && !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}

	code := collectCodeSpans(doc, source)
	for _, match := range appDirectiveLine.FindAllStringSubmatchIndex(body, -1) {
		if insideSpans(match[0], code) {
			continue
		}
		add(body[match[2]:match[3]])
	}

	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Link:
			if token, ok := appToken(n.Destination); ok {
				add(token)
			}
		case *ast.Image:
			if token, ok := appToken(n.Destination); ok {
				add(token)
			}
		case *ast.AutoLink:
			if token, ok := appToken(n.URL(source)); ok {
				add(token)
			}
		case *ast.LinkReferenceDefinition:
			if token, ok := appToken(n.Destination); ok {
				add(token)
			}
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.CodeSpan:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})

	for _, href := range htmlHrefs(body, code) {
		if token, ok := appPathToken(href); ok {
			add(token)
		}
	}
	for _, match := range schemeLessAppURL.FindAllStringIndex(body, -1) {
		if insideSpans(match[0], code) || !bareURLStartOK(body, match[0]) {
			continue
		}
		end := trimBareURLEnd(body, match[0], match[1])
		if token, ok := appPathToken(body[match[0]:end]); ok {
			add(token)
		}
	}
	return out
}

// RedactPrivateAppReferences rewrites private app links as plain text by mutating the goldmark AST
// and re-serializing Markdown. Public and unknown references stay; code is untouched. Raw HTML
// anchors to private apps are neutralized before parsing. ::app directive lines for private tokens
// are dropped after serialization.
func RedactPrivateAppReferences(body string, private map[string]bool) string {
	if body == "" || len(private) == 0 {
		return body
	}
	text := redactHTMLAnchors(body, private)
	source := []byte(text)
	doc := parseAppMarkdown(source)
	redactPrivateNodes(doc, source, private)
	redactSchemeLessInText(doc, source, private)
	pruneEmptyContainers(doc)
	out := redactAppDirectiveLines(serializeMarkdown(doc, source), private)
	if !strings.HasSuffix(text, "\n") {
		out = strings.TrimRight(out, "\n")
	} else if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// RedactPrivateAppHTML removes or neutralizes href/src attributes that resolve to private app
// pages. Only real href and src attributes are considered (not data-href). Attribute values are
// entity-decoded by the tokenizer and then percent-decoded for matching. This is the HTML safety
// net for encoding forms that survive Markdown-level handling.
func RedactPrivateAppHTML(fragment string, private map[string]bool) string {
	if fragment == "" || len(private) == 0 {
		return fragment
	}
	root, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return redactHTMLAnchors(fragment, private)
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "a":
				if href, ok := attrValue(n, "href"); ok {
					if token, match := appPathToken(href); match && private[token] {
						// Keep inner text; drop the anchor element.
						unwrapHTMLNode(n)
						return
					}
				}
			case "img":
				if src, ok := attrValue(n, "src"); ok {
					if token, match := appPathToken(src); match && private[token] {
						if parent := n.Parent; parent != nil {
							parent.RemoveChild(n)
						}
						return
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			walk(child)
			child = next
		}
	}
	walk(root)
	var buf bytes.Buffer
	// html.Parse wraps fragments in <html><head/><body>…; emit body children only.
	body := findHTMLBody(root)
	if body == nil {
		_ = html.Render(&buf, root)
		return buf.String()
	}
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		_ = html.Render(&buf, child)
	}
	return buf.String()
}

func parseAppMarkdown(source []byte) ast.Node {
	return appMarkdown.Parser().Parse(text.NewReader(source))
}

func redactPrivateNodes(doc ast.Node, source []byte, private map[string]bool) {
	var unwrap []ast.Node
	var remove []ast.Node
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Link:
			if token, ok := appToken(n.Destination); ok && private[token] {
				unwrap = append(unwrap, n)
				return ast.WalkSkipChildren, nil
			}
		case *ast.Image:
			if token, ok := appToken(n.Destination); ok && private[token] {
				remove = append(remove, n)
				return ast.WalkSkipChildren, nil
			}
		case *ast.AutoLink:
			if token, ok := appToken(n.URL(source)); ok && private[token] {
				remove = append(remove, n)
				return ast.WalkSkipChildren, nil
			}
		case *ast.LinkReferenceDefinition:
			if token, ok := appToken(n.Destination); ok && private[token] {
				remove = append(remove, n)
				return ast.WalkSkipChildren, nil
			}
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.CodeSpan:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	for _, n := range unwrap {
		unwrapMarkdownNode(n)
	}
	for _, n := range remove {
		if parent := n.Parent(); parent != nil {
			parent.RemoveChild(parent, n)
		}
	}
}

// redactSchemeLessInText removes bare / encoded app URLs from Text nodes outside code and links.
func redactSchemeLessInText(doc ast.Node, source []byte, private map[string]bool) {
	var targets []*ast.Text
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.CodeSpan, *ast.AutoLink, *ast.Link, *ast.Image:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			targets = append(targets, n)
		}
		return ast.WalkContinue, nil
	})
	for _, textNode := range targets {
		parent := textNode.Parent()
		if parent == nil {
			continue
		}
		seg := textNode.Segment
		value := string(seg.Value(source))
		matches := plainAppURL.FindAllStringIndex(value, -1)
		if len(matches) == 0 {
			continue
		}
		var parts []ast.Node
		cursor := 0
		changed := false
		for _, match := range matches {
			start, end := match[0], match[1]
			if !bareURLStartOK(value, start) {
				continue
			}
			end = trimBareURLEnd(value, start, end)
			token, ok := appPathToken(value[start:end])
			if !ok || !private[token] {
				continue
			}
			if start > cursor {
				parts = append(parts, ast.NewString([]byte(value[cursor:start])))
			}
			cursor = end
			changed = true
		}
		if !changed {
			continue
		}
		if cursor < len(value) {
			rest := ast.NewString([]byte(value[cursor:]))
			if textNode.SoftLineBreak() {
				// Preserve soft break on the trailing fragment.
				_ = rest
			}
			parts = append(parts, ast.NewString([]byte(value[cursor:])))
		}
		// Soft/hard line breaks live on the Text node; keep a trailing empty Text if needed.
		if textNode.SoftLineBreak() || textNode.HardLineBreak() {
			tail := ast.NewText()
			if textNode.SoftLineBreak() {
				tail.SetSoftLineBreak(true)
			}
			if textNode.HardLineBreak() {
				tail.SetHardLineBreak(true)
			}
			parts = append(parts, tail)
		}
		for i, part := range parts {
			if i == 0 {
				parent.ReplaceChild(parent, textNode, part)
				textNode = nil
				continue
			}
			prev := parts[i-1]
			parent.InsertAfter(parent, prev, part)
		}
	}
}

func unwrapMarkdownNode(n ast.Node) {
	parent := n.Parent()
	if parent == nil {
		return
	}
	for child := n.FirstChild(); child != nil; {
		next := child.NextSibling()
		n.RemoveChild(n, child)
		parent.InsertBefore(parent, n, child)
		child = next
	}
	parent.RemoveChild(parent, n)
}

func redactAppDirectiveLines(body string, private map[string]bool) string {
	source := []byte(body)
	doc := parseAppMarkdown(source)
	code := collectCodeSpans(doc, source)
	var b strings.Builder
	last := 0
	for _, match := range appDirectiveLine.FindAllStringSubmatchIndex(body, -1) {
		if insideSpans(match[0], code) || !private[body[match[2]:match[3]]] {
			continue
		}
		b.WriteString(body[last:match[0]])
		last = match[1]
	}
	if last == 0 {
		return body
	}
	b.WriteString(body[last:])
	return b.String()
}

func collectCodeSpans(doc ast.Node, source []byte) [][2]int {
	spans := [][2]int{}
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			lines := n.Lines()
			if lines == nil || lines.Len() == 0 {
				return ast.WalkSkipChildren, nil
			}
			start := lineStart(source, lines.At(0).Start)
			stop := includeLineEnding(source, lines.At(lines.Len()-1).Stop)
			// Include the fence lines when present.
			if start > 0 {
				prev := lineStart(source, start-1)
				spans = append(spans, [2]int{prev, stop})
			} else {
				spans = append(spans, [2]int{start, stop})
			}
			if stop < len(source) {
				nextLine := includeLineEnding(source, stop)
				_ = nextLine
			}
			// Expand forward over a closing fence line for fenced blocks.
			if _, ok := n.(*ast.FencedCodeBlock); ok {
				end := stop
				for end < len(source) && (source[end] == ' ' || source[end] == '\t' || source[end] == '`' || source[end] == '~' || source[end] == '\r') {
					end++
					if end > 0 && end <= len(source) && source[end-1] == '\n' {
						break
					}
				}
				if end > stop {
					spans[len(spans)-1][1] = end
				}
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan:
			spanStart, spanStop := -1, -1
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				if t, ok := child.(*ast.Text); ok {
					if spanStart < 0 || t.Segment.Start < spanStart {
						spanStart = t.Segment.Start
					}
					if t.Segment.Stop > spanStop {
						spanStop = t.Segment.Stop
					}
				}
			}
			if spanStart >= 0 && spanStop > spanStart {
				// Include surrounding backticks by expanding to nearest matching runs.
				left := spanStart
				for left > 0 && source[left-1] == '`' {
					left--
				}
				right := spanStop
				for right < len(source) && source[right] == '`' {
					right++
				}
				spans = append(spans, [2]int{left, right})
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return spans
}

func insideSpans(pos int, spans [][2]int) bool {
	for _, span := range spans {
		if pos >= span[0] && pos < span[1] {
			return true
		}
	}
	return false
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

func pruneEmptyContainers(doc ast.Node) {
	var remove []ast.Node
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			return ast.WalkContinue, nil
		}
		switch node.(type) {
		case *ast.ListItem:
			if node.FirstChild() == nil {
				remove = append(remove, node)
			}
		case *ast.List:
			if node.FirstChild() == nil {
				remove = append(remove, node)
			}
		case *ast.Paragraph, *ast.TextBlock:
			if node.FirstChild() == nil {
				remove = append(remove, node)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, n := range remove {
		if parent := n.Parent(); parent != nil {
			parent.RemoveChild(parent, n)
		}
	}
}

// serializeMarkdown renders a (possibly mutated) goldmark AST back to CommonMark-ish Markdown.
func serializeMarkdown(doc ast.Node, source []byte) string {
	var b strings.Builder
	writeBlocks(&b, doc, source, 0, false)
	return b.String()
}

func writeBlocks(b *strings.Builder, parent ast.Node, source []byte, depth int, inQuote bool) {
	children := []ast.Node{}
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		children = append(children, child)
	}
	for i, child := range children {
		switch n := child.(type) {
		case *ast.Heading:
			writeQuotePrefix(b, inQuote, depth)
			b.WriteString(strings.Repeat("#", n.Level))
			b.WriteByte(' ')
			writeInlines(b, n, source)
			b.WriteByte('\n')
		case *ast.Paragraph:
			writeQuotePrefix(b, inQuote, depth)
			writeInlines(b, n, source)
			b.WriteByte('\n')
		case *ast.TextBlock:
			writeQuotePrefix(b, inQuote, depth)
			writeInlines(b, n, source)
			b.WriteByte('\n')
		case *ast.Blockquote:
			writeBlocks(b, n, source, depth+1, true)
		case *ast.FencedCodeBlock:
			writeFencedCode(b, n, source, inQuote, depth)
		case *ast.CodeBlock:
			writeIndentedCode(b, n, source, inQuote, depth)
		case *ast.List:
			writeList(b, n, source, depth, inQuote)
		case *ast.ThematicBreak:
			writeQuotePrefix(b, inQuote, depth)
			b.WriteString("---\n")
		case *ast.LinkReferenceDefinition:
			writeQuotePrefix(b, inQuote, depth)
			writeDefinition(b, n, source)
			b.WriteByte('\n')
		case *ast.HTMLBlock:
			writeHTMLBlock(b, n, source, inQuote, depth)
		default:
			if n.Type() == ast.TypeBlock {
				writeBlocks(b, n, source, depth, inQuote)
			}
		}
		if i+1 < len(children) && needsBlankLineBetween(child, children[i+1]) {
			writeQuotePrefix(b, inQuote, depth)
			b.WriteByte('\n')
		}
	}
}

func needsBlankLineBetween(prev, next ast.Node) bool {
	switch next.(type) {
	case *ast.FencedCodeBlock:
		return false
	case *ast.CodeBlock:
		// Indented code requires a blank line after a paragraph.
		return true
	case *ast.LinkReferenceDefinition:
		// Definitions must not interrupt a paragraph on re-parse.
		if _, ok := prev.(*ast.LinkReferenceDefinition); ok {
			return false
		}
		return true
	}
	switch prev.(type) {
	case *ast.FencedCodeBlock, *ast.LinkReferenceDefinition:
		return false
	case *ast.CodeBlock:
		return true
	default:
		return true
	}
}

func writeQuotePrefix(b *strings.Builder, inQuote bool, depth int) {
	if !inQuote {
		return
	}
	for i := 0; i < depth; i++ {
		b.WriteString("> ")
	}
}

func writeFencedCode(b *strings.Builder, n *ast.FencedCodeBlock, source []byte, inQuote bool, depth int) {
	info := ""
	if n.Info != nil {
		seg := n.Info.Segment
		info = string(seg.Value(source))
	}
	writeQuotePrefix(b, inQuote, depth)
	b.WriteString("```")
	b.WriteString(info)
	b.WriteByte('\n')
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		writeQuotePrefix(b, inQuote, depth)
		seg := lines.At(i)
		b.Write(seg.Value(source))
	}
	writeQuotePrefix(b, inQuote, depth)
	b.WriteString("```\n")
}

func writeIndentedCode(b *strings.Builder, n *ast.CodeBlock, source []byte, inQuote bool, depth int) {
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		writeQuotePrefix(b, inQuote, depth)
		b.WriteString("    ")
		seg := lines.At(i)
		b.Write(seg.Value(source))
	}
}

func writeList(b *strings.Builder, list *ast.List, source []byte, depth int, inQuote bool) {
	index := list.Start
	if index == 0 {
		index = 1
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		writeQuotePrefix(b, inQuote, depth)
		if list.IsOrdered() {
			fmt.Fprintf(b, "%d%c ", index, list.Marker)
			index++
		} else {
			b.WriteByte(list.Marker)
			b.WriteByte(' ')
		}
		// ListItem children are blocks; write first block's inlines on the same line when paragraph.
		first := true
		for child := item.FirstChild(); child != nil; child = child.NextSibling() {
			switch n := child.(type) {
			case *ast.Paragraph, *ast.TextBlock:
				if !first {
					writeQuotePrefix(b, inQuote, depth)
					b.WriteString("  ")
				}
				writeInlines(b, n, source)
				b.WriteByte('\n')
			case *ast.List:
				writeList(b, n, source, depth, inQuote)
			case *ast.CodeBlock:
				writeIndentedCode(b, n, source, inQuote, depth)
			case *ast.FencedCodeBlock:
				writeFencedCode(b, n, source, inQuote, depth)
			case *ast.Blockquote:
				writeBlocks(b, n, source, depth+1, true)
			default:
				writeBlocks(b, item, source, depth, inQuote)
				first = false
				continue
			}
			first = false
		}
		if item.FirstChild() == nil {
			b.WriteByte('\n')
		}
	}
}

func writeDefinition(b *strings.Builder, n *ast.LinkReferenceDefinition, _ []byte) {
	b.WriteByte('[')
	b.Write(n.Label)
	b.WriteString("]: ")
	b.Write(n.Destination)
	if len(n.Title) > 0 {
		b.WriteString(" \"")
		b.Write(n.Title)
		b.WriteByte('"')
	}
}

func writeHTMLBlock(b *strings.Builder, n *ast.HTMLBlock, source []byte, inQuote bool, depth int) {
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		writeQuotePrefix(b, inQuote, depth)
		seg := lines.At(i)
		b.Write(seg.Value(source))
	}
}

func writeInlines(b *strings.Builder, parent ast.Node, source []byte) {
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		switch n := child.(type) {
		case *ast.Text:
			seg := n.Segment
			b.Write(seg.Value(source))
			if n.SoftLineBreak() {
				b.WriteByte('\n')
			}
			if n.HardLineBreak() {
				b.WriteString("\\\n")
			}
		case *ast.String:
			b.Write(n.Value)
		case *ast.CodeSpan:
			content := codeSpanText(n, source)
			ticks := neededTicks(content)
			b.WriteString(strings.Repeat("`", ticks))
			if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") || strings.HasPrefix(content, " ") || strings.HasSuffix(content, " ") {
				b.WriteByte(' ')
				b.WriteString(content)
				b.WriteByte(' ')
			} else {
				b.WriteString(content)
			}
			b.WriteString(strings.Repeat("`", ticks))
		case *ast.Emphasis:
			mark := strings.Repeat("*", n.Level)
			b.WriteString(mark)
			writeInlines(b, n, source)
			b.WriteString(mark)
		case *ast.Link:
			writeLink(b, n, source, false)
		case *ast.Image:
			b.WriteByte('!')
			writeLink(b, n, source, true)
		case *ast.AutoLink:
			label := n.Label(source)
			if n.AutoLinkType == ast.AutoLinkEmail || (len(label) > 0 && label[0] == '<') {
				b.WriteByte('<')
				b.Write(label)
				b.WriteByte('>')
			} else {
				b.Write(label)
			}
		case *ast.RawHTML:
			if n.Segments != nil {
				for i := 0; i < n.Segments.Len(); i++ {
					seg := n.Segments.At(i)
					b.Write(seg.Value(source))
				}
			}
		default:
			writeInlines(b, n, source)
		}
	}
}

func writeLink(b *strings.Builder, n ast.Node, source []byte, image bool) {
	var dest, title []byte
	var ref *ast.ReferenceLink
	switch t := n.(type) {
	case *ast.Link:
		dest, title, ref = t.Destination, t.Title, t.Reference
	case *ast.Image:
		dest, title, ref = t.Destination, t.Title, t.Reference
	}
	_ = image
	b.WriteByte('[')
	writeInlines(b, n, source)
	b.WriteByte(']')
	if ref != nil {
		switch ref.Type {
		case ast.ReferenceLinkShortcut:
			// shortcut: [text] only
		case ast.ReferenceLinkCollapsed:
			b.WriteString("[]")
		default:
			b.WriteByte('[')
			b.Write(ref.Value)
			b.WriteByte(']')
		}
		return
	}
	b.WriteByte('(')
	if bytes.IndexByte(dest, ' ') >= 0 {
		b.WriteByte('<')
		b.Write(dest)
		b.WriteByte('>')
	} else {
		b.Write(dest)
	}
	if len(title) > 0 {
		b.WriteString(" \"")
		b.Write(bytes.ReplaceAll(title, []byte(`"`), []byte(`\"`)))
		b.WriteByte('"')
	}
	b.WriteByte(')')
}

func codeSpanText(n *ast.CodeSpan, source []byte) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			seg := t.Segment
			b.Write(seg.Value(source))
			if t.SoftLineBreak() {
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}

func neededTicks(content string) int {
	maxRun := 0
	run := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	return maxRun + 1
}

func redactHTMLAnchors(body string, private map[string]bool) string {
	code := collectCodeSpans(parseAppMarkdown([]byte(body)), []byte(body))
	edits := []struct {
		start, end int
		repl       string
	}{}
	for _, segment := range outsideCode(body, code) {
		edits = append(edits, htmlAnchorEdits(body, segment[0], segment[1], private)...)
	}
	if len(edits) == 0 {
		return body
	}
	// Apply back to front.
	sortEditsDesc(edits)
	out := body
	for _, edit := range edits {
		out = out[:edit.start] + edit.repl + out[edit.end:]
	}
	return out
}

func htmlAnchorEdits(source string, segStart, segStop int, private map[string]bool) []struct {
	start, end int
	repl       string
} {
	tokenizer := html.NewTokenizer(strings.NewReader(source[segStart:segStop]))
	type anchor struct {
		start, innerStart int
		private           bool
	}
	stack := []anchor{}
	edits := []struct {
		start, end int
		repl       string
	}{}
	offset := segStart
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
				edits = append(edits, struct {
					start, end int
					repl       string
				}{start: current.start, end: offset, repl: source[current.innerStart:start]})
			}
		}
	}
	return edits
}

func htmlHrefs(body string, code [][2]int) []string {
	hrefs := []string{}
	for _, segment := range outsideCode(body, code) {
		tokenizer := html.NewTokenizer(strings.NewReader(body[segment[0]:segment[1]]))
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

func outsideCode(source string, code [][2]int) [][2]int {
	if len(code) == 0 {
		return [][2]int{{0, len(source)}}
	}
	out := [][2]int{}
	pos := 0
	for _, span := range code {
		start, stop := span[0], span[1]
		if start < 0 {
			start = 0
		}
		if stop > len(source) {
			stop = len(source)
		}
		if start > pos {
			out = append(out, [2]int{pos, start})
		}
		if stop > pos {
			pos = stop
		}
	}
	if pos < len(source) {
		out = append(out, [2]int{pos, len(source)})
	}
	return out
}

func sortEditsDesc(edits []struct {
	start, end int
	repl       string
}) {
	for i := 0; i < len(edits); i++ {
		for j := i + 1; j < len(edits); j++ {
			if edits[j].start > edits[i].start {
				edits[i], edits[j] = edits[j], edits[i]
			}
		}
	}
}

func attrValue(n *html.Node, name string) (string, bool) {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val, true
		}
	}
	return "", false
}

func unwrapHTMLNode(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	for child := n.FirstChild; child != nil; {
		next := child.NextSibling
		n.RemoveChild(child)
		parent.InsertBefore(child, n)
		child = next
	}
	parent.RemoveChild(n)
}

func findHTMLBody(root *html.Node) *html.Node {
	var body *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil && body == nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return body
}

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
// page shape. Shared by discovery and redaction so both see the same destination.
func appToken(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	return appPathToken(string(util.URLEscape(raw, true)))
}

func appPathToken(raw string) (string, bool) {
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
			if port := parsed.Port(); port != "" {
				n, err := strconv.Atoi(port)
				if err != nil || n != 443 {
					return "", false
				}
			}
		case "http":
			if port := parsed.Port(); port != "" {
				n, err := strconv.Atoi(port)
				if err != nil || n != 80 {
					return "", false
				}
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
	for _, candidate := range pathCandidates(parsed) {
		if match := appPath.FindStringSubmatch(candidate); match != nil {
			return strings.ToLower(match[1]), true
		}
	}
	return "", false
}

// pathCandidates yields normalized path forms so percent-encoded slashes like %2Fapps%2F still match.
func pathCandidates(parsed *url.URL) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(parsed.Path)
	if parsed.RawPath != "" {
		if unescaped, err := url.PathUnescape(parsed.RawPath); err == nil {
			add(unescaped)
		}
		add(parsed.RawPath)
	}
	if unescaped, err := url.PathUnescape(parsed.Path); err == nil {
		add(unescaped)
	}
	// /%2Fapps%2Ftoken → Path "//apps/token"; collapse leading slashes.
	if parsed.Path != "" {
		collapsed := "/" + strings.TrimLeft(parsed.Path, "/")
		add(collapsed)
	}
	return out
}
