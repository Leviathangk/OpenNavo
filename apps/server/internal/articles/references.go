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
)

// AppReferences lists the app tokens an article body references: ::app[token] cards and inline
// links to app pages. Code is skipped, matching ScanBody.
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
	return out
}

// RedactPrivateAppReferences rewrites links to private apps as plain text and removes private
// ::app directive lines. Public and unknown references are kept, and code is left untouched.
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
	text = appDirectiveLine.ReplaceAllStringFunc(text, func(line string) string {
		match := appDirectiveLine.FindStringSubmatch(line)
		if len(match) > 1 && private[match[1]] {
			return ""
		}
		return line
	})
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

func appPathToken(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "" || parsed.Host != "" {
		if !strings.EqualFold(parsed.Scheme, "https") || parsed.Port() != "" && parsed.Port() != "443" {
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
