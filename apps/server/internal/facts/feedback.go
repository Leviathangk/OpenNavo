package facts

import (
	"html"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	htmlparser "golang.org/x/net/html"
)

var feedbackSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?im)(?:authorization|proxy-authorization|cookie|set-cookie)\s*:[^\r\n]*`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|passwd|secret|token)["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;&]+)`),
	regexp.MustCompile(`(?i)https?://[^\s/@]+:[^\s/@]+@`),
	regexp.MustCompile(`(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`),
	regexp.MustCompile(`(?:/Users/|/home/)[^/\s]+`),
}

// CleanFeedback produces escaped plain text, not HTML. Contact details supplied explicitly in
// the separate private contact field are not added to logs or public knowledge articles.
func CleanFeedback(value string) string {
	root, err := htmlparser.Parse(strings.NewReader(value))
	if err != nil {
		return ""
	}
	var out strings.Builder
	var visit func(*htmlparser.Node)
	visit = func(n *htmlparser.Node) {
		if n.Type == htmlparser.ElementNode {
			switch n.Data {
			case "script", "style", "iframe", "object", "embed", "svg", "math", "template":
				return
			}
		}
		if n.Type == htmlparser.TextNode {
			out.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
		if n.Type == htmlparser.ElementNode {
			switch n.Data {
			case "p", "div", "br", "li", "pre":
				out.WriteByte('\n')
			}
		}
	}
	visit(root)
	text := out.String()
	for _, pattern := range feedbackSecrets {
		text = pattern.ReplaceAllString(text, "[REDACTED]")
	}
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
	return html.EscapeString(strings.TrimSpace(text))
}

// Only explicitly supplied links are navigable references. URLs in feedback
// content remain escaped plain text and are not subject to this link allowlist.
func FeedbackLinks(links []string) error {
	if len(links) > 3 {
		return ErrInvalid
	}
	for _, raw := range links {
		if len(raw) > 2048 || !ValidSourceURL(raw) {
			return ErrInvalid
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "localhost" || net.ParseIP(u.Hostname()) != nil {
			return ErrInvalid
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return ErrInvalid
		}
	}
	return nil
}
