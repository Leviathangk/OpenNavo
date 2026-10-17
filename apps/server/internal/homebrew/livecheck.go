package homebrew

import (
	"net/url"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
)

// SparkleLivecheck reads only a literal, unconditional livecheck URL. No Ruby is
// evaluated, and catalog JSON stays byte-for-byte independent of this reader.
// Dynamic URLs, nested platform branches and multiple livecheck declarations are
// deliberately left for an explicit editor rule.
type UpdateLivecheck struct {
	Source     string
	URL        string
	GithubRepo string
}

func SparkleLivecheck(source string) (string, error) {
	rule, err := ParseUpdateLivecheck(source)
	if err != nil || rule.Source != "sparkle" {
		return "", err
	}
	return rule.URL, nil
}
func ParseUpdateLivecheck(source string) (UpdateLivecheck, error) {
	if len(source) > 256<<10 {
		return UpdateLivecheck{}, &UnsupportedCaskError{Reason: "source exceeds livecheck limit"}
	}
	tokens, err := lexRuby(source)
	if err != nil {
		return UpdateLivecheck{}, err
	}
	depth := 0
	found := UpdateLivecheck{}
	inherited := map[string]string{}
	seen := false
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.kind == rbKeyword {
			switch t.text {
			case "do", "if", "unless", "case", "begin", "class", "module", "def", "for", "while", "until":
				depth++
			case "end":
				depth--
			}
		}
		if depth == 1 && t.kind == rbIdent && (t.text == "url" || t.text == "homepage") && i+1 < len(tokens) && tokens[i+1].kind == rbString {
			var value strings.Builder
			for _, part := range tokens[i+1].parts {
				if part.interp {
					break
				}
				value.WriteString(part.text)
			}
			inherited[t.text] = value.String()
		}
		if t.kind != rbIdent || t.text != "livecheck" {
			continue
		}
		if seen {
			return UpdateLivecheck{}, &UnsupportedCaskError{Line: t.line, Reason: "multiple livecheck blocks"}
		}
		seen = true
		if depth != 1 || i+1 >= len(tokens) || !isKeyword(tokens[i+1], "do") {
			return UpdateLivecheck{}, &UnsupportedCaskError{Line: t.line, Reason: "conditional livecheck"}
		}
		p := &caskParser{tokens: tokens, pos: i + 2}
		url, strategy := "", ""
		used := map[string]bool{}
		for {
			p.skipNewlines()
			name := p.next()
			if isKeyword(name, "end") {
				break
			}
			if name.kind != rbIdent || used[name.text] {
				return UpdateLivecheck{}, p.fail(name.line, "unsupported livecheck statement")
			}
			used[name.text] = true
			if name.text == "skip" {
				return UpdateLivecheck{}, nil
			}
			args, block, err := p.parseCall(name)
			if err != nil {
				return UpdateLivecheck{}, err
			}
			if len(args.positional) != 1 || len(args.options.keys) != 0 {
				return UpdateLivecheck{}, p.fail(name.line, "dynamic livecheck arguments")
			}
			value := args.positional[0]
			switch name.text {
			case "url":
				if value.kind == vSymbol && (value.text == "url" || value.text == "homepage") && block == "" {
					url = inherited[value.text]
					continue
				}
				if value.kind != vString || block != "" {
					return UpdateLivecheck{}, p.fail(name.line, "literal feed URL required")
				}
				url = value.text
			case "strategy":
				if value.kind != vSymbol {
					return UpdateLivecheck{}, nil
				}
				strategy = value.text
				if block != "" {
					if _, err := p.skipBlock(block); err != nil {
						return UpdateLivecheck{}, err
					}
				}
			default:
				return UpdateLivecheck{}, p.fail(name.line, "unsupported livecheck stanza")
			}
		}
		if domain.LinkHTTPS(url) && !strings.Contains(url, "#{") {
			switch strategy {
			case "sparkle":
				found = UpdateLivecheck{Source: "sparkle", URL: url}
			case "electron_builder":
				found = UpdateLivecheck{Source: "electron", URL: url}
			case "github_latest":
				if repo := livecheckRepo(url); repo != "" {
					found = UpdateLivecheck{Source: "github", URL: "https://api.github.com/repos/" + repo + "/releases/latest", GithubRepo: repo}
				}
			}
		}
		i = p.pos - 1 // The outer scanner did not consume the livecheck opener.
	}
	return found, nil
}

func livecheckRepo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host != "github.com" || u.Scheme != "https" || u.User != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	for _, s := range parts[:2] {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "#{} ") {
			return ""
		}
	}
	return strings.Join(parts[:2], "/")
}
