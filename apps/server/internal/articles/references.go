package articles

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	// appLink is an inline Markdown link whose destination may point at an app page.
	appLink = regexp.MustCompile(`\[([^\]\n]*)\]\(\s*<?([^)\s>]+)>?(?:\s+(?:"[^"\n]*"|'[^'\n]*'))?\s*\)`)
	// appDirectiveLine is one ::app[token] line, including its line ending.
	appDirectiveLine = regexp.MustCompile(`(?m)^::app\[([a-z0-9][a-z0-9@+._-]*)\][ \t]*(?:\r?\n|$)`)
	// fenceLine is a fenced code block opener or closer.
	fenceLine = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	// appPath is /apps/<token>, /<locale>/apps/<token> and any trailing path.
	appPath = regexp.MustCompile(`^/(?:[A-Za-z]{2}(?:-[A-Za-z0-9]{2,8})?/)?apps/([a-z0-9][a-z0-9@+._-]*)(?:/.*)?$`)
	// bareAppURL is a linkified app URL, with or without a scheme and www.
	bareAppURL = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?opennavo\.com/(?:[A-Za-z]{2}(?:-[A-Za-z0-9]{2,8})?/)?apps/[^\s<>()\[\]{}"']+`)
	// referenceDefinition is a link reference definition line, including its line ending.
	referenceDefinition = regexp.MustCompile(`(?m)^ {0,3}\[([^\]\n]+)\]:[ \t]*(?:<([^>\n]*)>|(\S+))(?:[ \t]+(?:"[^"\n]*"|'[^'\n]*'|\([^)\n]*\)))?[ \t]*(?:\r?\n|$)`)
	// fullReference is [text][label] or the collapsed [text][].
	fullReference = regexp.MustCompile(`\[([^\]\n]*)\]\[([^\]\n]*)\]`)
	// shortcutReference is [label], valid only when a matching definition exists.
	shortcutReference = regexp.MustCompile(`\[([^\]\n]+)\]`)
	// htmlAnchor is a raw HTML anchor, inert in article Markdown but stripped anyway as a guard.
	htmlAnchor = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a\s*>`)
	// htmlHref reads the href attribute of an HTML anchor.
	htmlHref = regexp.MustCompile("(?is)\\bhref\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s\"'=<>]+))")
)

// AppReferences lists the app tokens an article body references: ::app[token] cards, inline,
// autolinked and linkified app URLs, reference-style links and raw HTML anchors. Code is skipped,
// matching ScanBody.
func AppReferences(body string) []string {
	text := withoutCode(body)
	seen := map[string]bool{}
	out := []string{}
	add := func(token string) {
		if token != "" && !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}
	for _, match := range appDirective.FindAllStringSubmatch(text, -1) {
		add(match[1])
	}
	for _, match := range appLink.FindAllStringSubmatchIndex(text, -1) {
		if match[0] > 0 && text[match[0]-1] == '!' {
			continue
		}
		if token, ok := appPathToken(text[match[4]:match[5]]); ok {
			add(token)
		}
	}
	for _, match := range autolink.FindAllStringSubmatch(text, -1) {
		if token, ok := appPathToken(match[1]); ok {
			add(token)
		}
	}
	for _, match := range bareAppURL.FindAllStringIndex(text, -1) {
		if !bareURLStartOK(text, match[0]) {
			continue
		}
		end := trimBareURLEnd(text, match[0], match[1])
		if token, ok := appPathToken(text[match[0]:end]); ok {
			add(token)
		}
	}
	for _, definition := range referenceDefinitions(text) {
		if token, ok := appPathToken(definition.target); ok {
			add(token)
		}
	}
	for _, match := range htmlAnchor.FindAllStringSubmatch(text, -1) {
		if token, ok := appPathToken(htmlAnchorHref(match[1])); ok {
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
	ranges := codeRanges(body)
	if len(ranges) == 0 {
		return redactAppReferences(body, private)
	}
	var out strings.Builder
	last := 0
	for _, item := range ranges {
		out.WriteString(redactAppReferences(body[last:item[0]], private))
		out.WriteString(body[item[0]:item[1]])
		last = item[1]
	}
	out.WriteString(redactAppReferences(body[last:], private))
	return out.String()
}

func redactAppReferences(text string, private map[string]bool) string {
	// Raw HTML anchors are inert in article Markdown, but strip them before bare URLs so the guard
	// sees the complete href rather than an anchor already broken by URL redaction.
	text = redactHTMLAnchors(text, private)
	text = redactReferenceLinks(text, private)
	text = appDirectiveLine.ReplaceAllStringFunc(text, func(line string) string {
		match := appDirectiveLine.FindStringSubmatch(line)
		if len(match) > 1 && private[match[1]] {
			return ""
		}
		return line
	})
	text = redactInlineLinks(text, private)
	text = redactAutolinks(text, private)
	return redactBareURLs(text, private)
}

func redactInlineLinks(text string, private map[string]bool) string {
	matches := appLink.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	rewritten := false
	for _, match := range matches {
		if match[0] > 0 && text[match[0]-1] == '!' {
			continue
		}
		token, ok := appPathToken(text[match[4]:match[5]])
		if !ok || !private[token] {
			continue
		}
		out.WriteString(text[last:match[0]])
		out.WriteString(text[match[2]:match[3]])
		last = match[1]
		rewritten = true
	}
	if !rewritten {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

func redactAutolinks(text string, private map[string]bool) string {
	matches := autolink.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	rewritten := false
	for _, match := range matches {
		token, ok := appPathToken(text[match[2]:match[3]])
		if !ok || !private[token] {
			continue
		}
		// A private autolink has no separate text: keeping its URL would be linkified again.
		out.WriteString(text[last:match[0]])
		last = match[1]
		rewritten = true
	}
	if !rewritten {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

func redactBareURLs(text string, private map[string]bool) string {
	protected := markdownLinkSpans(text)
	matches := bareAppURL.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	rewritten := false
	for _, match := range matches {
		if !bareURLStartOK(text, match[0]) {
			continue
		}
		end := trimBareURLEnd(text, match[0], match[1])
		if insideSpans(match[0], protected) {
			continue
		}
		token, ok := appPathToken(text[match[0]:end])
		if !ok || !private[token] {
			continue
		}
		out.WriteString(text[last:match[0]])
		last = end
		rewritten = true
	}
	if !rewritten {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

// redactHTMLAnchors drops raw HTML anchors to private apps and keeps their inner text. Markdown
// rendering escapes raw HTML in article bodies, so this is a guard rather than a live-link fix.
func redactHTMLAnchors(text string, private map[string]bool) string {
	return htmlAnchor.ReplaceAllStringFunc(text, func(anchor string) string {
		match := htmlAnchor.FindStringSubmatch(anchor)
		if len(match) < 3 {
			return anchor
		}
		token, ok := appPathToken(htmlAnchorHref(match[1]))
		if !ok || !private[token] {
			return anchor
		}
		return match[2]
	})
}

func htmlAnchorHref(attrs string) string {
	match := htmlHref.FindStringSubmatch(attrs)
	if match == nil {
		return ""
	}
	for _, value := range match[1:] {
		if value != "" {
			return value
		}
	}
	return ""
}

type referenceDefinitionMatch struct {
	start, end int
	label      string
	target     string
}

func referenceDefinitions(text string) []referenceDefinitionMatch {
	matches := referenceDefinition.FindAllStringSubmatchIndex(text, -1)
	out := make([]referenceDefinitionMatch, 0, len(matches))
	for _, match := range matches {
		target := ""
		switch {
		case match[4] >= 0:
			target = text[match[4]:match[5]]
		case match[6] >= 0:
			target = text[match[6]:match[7]]
		}
		out = append(out, referenceDefinitionMatch{start: match[0], end: match[1], label: text[match[2]:match[3]], target: target})
	}
	return out
}

func redactReferenceLinks(text string, private map[string]bool) string {
	definitions := referenceDefinitions(text)
	privateLabels := map[string]bool{}
	for _, definition := range definitions {
		if token, ok := appPathToken(definition.target); ok && private[token] {
			privateLabels[normalizeReferenceLabel(definition.label)] = true
		}
	}
	if len(privateLabels) == 0 {
		return text
	}
	var defs strings.Builder
	last := 0
	for _, definition := range definitions {
		if !privateLabels[normalizeReferenceLabel(definition.label)] {
			continue
		}
		defs.WriteString(text[last:definition.start])
		last = definition.end
	}
	defs.WriteString(text[last:])
	text = defs.String()
	text = redactFullReferences(text, privateLabels)
	return redactShortcutReferences(text, privateLabels)
}

func redactFullReferences(text string, privateLabels map[string]bool) string {
	matches := fullReference.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	rewritten := false
	for _, match := range matches {
		if match[0] > 0 && text[match[0]-1] == '!' {
			continue
		}
		label := text[match[4]:match[5]]
		if label == "" {
			label = text[match[2]:match[3]]
		}
		if !privateLabels[normalizeReferenceLabel(label)] {
			continue
		}
		out.WriteString(text[last:match[0]])
		out.WriteString(text[match[2]:match[3]])
		last = match[1]
		rewritten = true
	}
	if !rewritten {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

func redactShortcutReferences(text string, privateLabels map[string]bool) string {
	matches := shortcutReference.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	rewritten := false
	for _, match := range matches {
		if match[0] > 0 && text[match[0]-1] == '!' {
			continue
		}
		if match[0] > 0 && text[match[0]-1] == '\\' {
			continue
		}
		if match[1] < len(text) && (text[match[1]] == '[' || text[match[1]] == '(') {
			continue
		}
		label := text[match[2]:match[3]]
		if !privateLabels[normalizeReferenceLabel(label)] {
			continue
		}
		out.WriteString(text[last:match[0]])
		out.WriteString(label)
		last = match[1]
		rewritten = true
	}
	if !rewritten {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

func normalizeReferenceLabel(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(label), " "))
}

func markdownLinkSpans(text string) [][2]int {
	spans := [][2]int{}
	for _, match := range appLink.FindAllStringIndex(text, -1) {
		spans = append(spans, [2]int{match[0], match[1]})
	}
	for _, match := range image.FindAllStringIndex(text, -1) {
		spans = append(spans, [2]int{match[0], match[1]})
	}
	return spans
}

func insideSpans(start int, spans [][2]int) bool {
	for _, span := range spans {
		if start >= span[0] && start < span[1] {
			return true
		}
	}
	return false
}

// bareURLStartOK keeps a hostname suffix such as notopennavo.com from matching opennavo.com.
func bareURLStartOK(text string, start int) bool {
	if start == 0 {
		return true
	}
	switch c := text[start-1]; {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
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

// codeRanges returns the byte ranges of fenced blocks and inline code spans, in order.
func codeRanges(body string) [][2]int {
	fences := fenceRanges(body)
	ranges := [][2]int{}
	last := 0
	for _, fence := range fences {
		ranges = append(ranges, inlineCodeRanges(body[last:fence[0]], last)...)
		ranges = append(ranges, fence)
		last = fence[1]
	}
	return append(ranges, inlineCodeRanges(body[last:], last)...)
}

func fenceRanges(body string) [][2]int {
	ranges := [][2]int{}
	inFence := false
	var fenceChar byte
	fenceLen, fenceStart := 0, 0
	for start := 0; start <= len(body); {
		lineEnd, next := len(body), len(body)
		if offset := strings.IndexByte(body[start:], '\n'); offset >= 0 {
			lineEnd, next = start+offset, start+offset+1
		}
		line := strings.TrimSuffix(body[start:lineEnd], "\r")
		if !inFence {
			if char, length, _, ok := fenceMarker(line); ok {
				inFence, fenceChar, fenceLen, fenceStart = true, char, length, start
			}
		} else if char, length, rest, ok := fenceMarker(line); ok && char == fenceChar && length >= fenceLen && strings.TrimSpace(rest) == "" {
			ranges = append(ranges, [2]int{fenceStart, next})
			inFence = false
		}
		if next == len(body) {
			break
		}
		start = next
	}
	if inFence {
		ranges = append(ranges, [2]int{fenceStart, len(body)})
	}
	return ranges
}

func fenceMarker(line string) (byte, int, string, bool) {
	match := fenceLine.FindStringSubmatch(line)
	if match == nil {
		return 0, 0, "", false
	}
	return match[1][0], len(match[1]), match[2], true
}

func inlineCodeRanges(text string, offset int) [][2]int {
	ranges := [][2]int{}
	for index := 0; index < len(text); {
		if text[index] != '`' {
			index++
			continue
		}
		run := backtickRun(text[index:])
		closeAt := -1
		for scan := index + run; scan < len(text); {
			if text[scan] == '\n' {
				break
			}
			if text[scan] != '`' {
				scan++
				continue
			}
			length := backtickRun(text[scan:])
			if length == run {
				closeAt = scan + length
				break
			}
			scan += length
		}
		if closeAt < 0 {
			index += run
			continue
		}
		ranges = append(ranges, [2]int{offset + index, offset + closeAt})
		index = closeAt
	}
	return ranges
}

func backtickRun(text string) int {
	length := 0
	for length < len(text) && text[length] == '`' {
		length++
	}
	return length
}
