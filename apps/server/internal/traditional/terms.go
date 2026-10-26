package traditional

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// OpenNavo's Taiwan wording on top of OpenCC (System Settings names as macOS shows them). It is
// OpenNavo data, kept apart from the pinned upstream files listed in data/upstream.json.
//
//go:embed data/opennavo-terms.json
var termsFile []byte

type term struct {
	From string `json:"from"`
	To   string `json:"to"`
	Why  string `json:"why"`
	// suffix is what To adds to From (登入項 → 登入項目); text already followed by it is left alone.
	suffix string
}

var wording = sync.OnceValues(func() ([]term, error) { return loadTerms(termsFile) })

func loadTerms(raw []byte) ([]term, error) {
	var file struct {
		About   string `json:"about"`
		Replace []term `json:"replace"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("invalid wording rules: %w", err)
	}
	for i := range file.Replace {
		rule := &file.Replace[i]
		if rule.From == "" || rule.To == "" || rule.Why == "" || rule.From == rule.To {
			return nil, fmt.Errorf("invalid wording rule %d", i)
		}
		if strings.HasPrefix(rule.To, rule.From) {
			rule.suffix = rule.To[len(rule.From):]
		}
	}
	return file.Replace, nil
}

// applyTerms replaces every occurrence of each rule's source, rule by rule in order. An occurrence
// already followed by its rule's extension is kept, and the search resumes one character later.
func applyTerms(text string, rules []term) string {
	for _, rule := range rules {
		if !strings.Contains(text, rule.From) {
			continue
		}
		var out strings.Builder
		rest := text
		for {
			i := strings.Index(rest, rule.From)
			if i < 0 {
				break
			}
			end := i + len(rule.From)
			if rule.suffix != "" && strings.HasPrefix(rest[end:], rule.suffix) {
				_, size := utf8.DecodeRuneInString(rest[i:])
				out.WriteString(rest[:i+size])
				rest = rest[i+size:]
				continue
			}
			out.WriteString(rest[:i])
			out.WriteString(rule.To)
			rest = rest[end:]
		}
		out.WriteString(rest)
		text = out.String()
	}
	return text
}
