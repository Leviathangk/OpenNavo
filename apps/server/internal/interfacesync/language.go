package interfacesync

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/llm"
)

// Short labels use language evidence from their batch only; word ambiguity is not a wrong-language error.
func languageText(source, translated string, protected []string) string {
	var text strings.Builder
	original := stripLanguageLiterals(strings.TrimSpace(strings.Split(source, "|")[0]), protected)
	for _, form := range strings.Split(translated, "|") {
		form = strings.TrimSpace(form)
		form = stripLanguageLiterals(form, protected)

		// Short technical names from the source may remain unchanged; full English sentences still need detection.
		if form == original && !strings.ContainsFunc(original, func(r rune) bool { return unicode.Is(unicode.Han, r) }) && shortText(form) {
			continue
		}
		text.WriteString(form)
		text.WriteByte(' ')
	}
	return strings.TrimSpace(text.String())
}
func letterCount(text string) int {
	n := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// After replaying all six target languages, require at least 8 words and 60 letters for independent classification.
// Shorter technical labels and cognates rely on batch context, not single-word classification confidence.
func shortText(text string) bool {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) })
	return len(words) < 8 || letterCount(text) < 60
}
func languageFailures(batch Batch, code string, fields map[string]string, protected []string, invalid map[string]string) map[string]string {
	rejected := map[string]string{}
	short := map[string]string{}
	fail := func(key, text string) {
		runes := []rune(text)
		rejected[key] = fmt.Sprintf("wrong language: %s/%s keys=[%s] text=%q", batch.Target, code, key, string(runes[:min(200, len(runes))]))
	}
	for _, key := range sortedKeys(batch.Source) {
		if invalid[key] != "" {
			continue
		}
		if isLocaleName(batch.Source[key]) && isLocaleName(fields[key]) {
			continue
		}
		value := languageText(batch.Source[key], fields[key], protected)
		if letterCount(value) == 0 {
			continue
		}
		switch {
		case code == "ja-JP" || code == "ru-RU" || code == "zh-CN" || code == "zh-Hant" || strings.ContainsFunc(value, func(r rune) bool {
			return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Cyrillic)
		}):
			if !llm.MatchesLanguage(value, code) {
				fail(key, value)
			}
		case !shortText(value):
			if !llm.MatchesLanguage(value, code) {
				fail(key, value)
			}
		default:
			short[key] = value
		}
	}
	var bisect func([]string)
	bisect = func(keys []string) {
		values := []string{}
		for _, key := range keys {
			values = append(values, short[key])
		}
		pooled := strings.Join(values, " ")
		// Preserve ambiguity when evidence is insufficient; do not fall back to hard single-word classification.
		if shortText(pooled) || llm.MatchesLanguage(pooled, code) {
			return
		}
		if len(keys) == 1 {
			fail(keys[0], pooled)
			return
		}
		midpoint := len(keys) / 2
		left, right := keys[:midpoint], keys[midpoint:]
		leftText, rightText := []string{}, []string{}
		for _, key := range left {
			leftText = append(leftText, short[key])
		}
		for _, key := range right {
			rightText = append(rightText, short[key])
		}
		if shortText(strings.Join(leftText, " ")) && shortText(strings.Join(rightText, " ")) {
			// Bisect to the smallest detectable group, preserving group-level evidence rather than isolated-word probabilities.
			for _, key := range keys {
				fail(key, pooled)
			}
			return
		}
		bisect(left)
		bisect(right)
	}
	bisect(sortedKeys(short))
	return rejected
}

func stripLanguageLiterals(form string, protected []string) string {
	for _, literal := range LiteralSpans(form) {
		form = strings.ReplaceAll(form, literal, "")
	}
	for _, term := range protected {
		if term != "" {
			form = stripTerm(form, term, false)
		}
	}
	for _, term := range append(append([]string{}, TechnicalTerms...), "OK", "CLI", "Iframe", "Web") {
		form = stripTerm(form, term, true)
	}
	return strings.TrimSpace(form)
}
func isLocaleName(value string) bool {
	for _, metadata := range i18n.Metadata {
		if value == metadata.Name {
			return true
		}
	}
	return false
}

// ReplayLanguage uses the same script, long-text and short-text aggregation rules as sync, without calling a gateway.
func ReplayLanguage(batch Batch, output map[string]map[string]string, protected []string) error {
	reasons := []string{}
	for _, code := range batch.Locales {
		failures := languageFailures(batch, code, output[code], protected, nil)
		for _, key := range sortedKeys(failures) {
			reasons = append(reasons, failures[key])
		}
	}
	if len(reasons) > 0 {
		return Invalid(strings.Join(reasons, "; "))
	}
	return nil
}
