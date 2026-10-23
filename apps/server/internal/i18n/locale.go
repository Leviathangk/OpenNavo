package i18n

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func Valid(value string) bool {
	_, ok := Metadata[LocaleCode(value)]
	return ok
}

// ValidApp limits admin UI and server messages to app locales; the website also indexes these without verified
// translations.
func ValidApp(value string) bool { return slices.Contains(Locales[:], LocaleCode(value)) }

// ValidDesktop limits client release notes and the catalog data the Mac client downloads to its languages.
func ValidDesktop(value string) bool { return slices.Contains(DesktopLocales[:], LocaleCode(value)) }

// ContentLocales keeps client release notes on the desktop language contract.
func ContentLocales(entity string) []LocaleCode {
	if entity == "desktop_release" {
		return DesktopLocales[:]
	}
	return SiteLocales[:]
}

// Fold regional variants only for supported languages into product locales; do not guess other languages.
func canonical(value string, locales []LocaleCode) string {
	value = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "_", "-")
	if slices.Contains(locales, LocaleCode("zh-Hant")) && (value == "zh-hant" || strings.HasPrefix(value, "zh-hant-") || value == "zh-tw" || value == "zh-hk" || value == "zh-mo") {
		return "zh-Hant"
	}
	for _, locale := range locales {
		code := strings.ToLower(string(locale))
		if value == code || strings.Split(value, "-")[0] == strings.Split(code, "-")[0] {
			return string(locale)
		}
	}
	return ""
}

func Negotiate(query, header string) string { return negotiate(query, header, SiteLocales[:]) }

// NegotiateApp retains the shared six-language UI matching contract.
func NegotiateApp(query, header string) string { return negotiate(query, header, Locales[:]) }

func negotiate(query, header string, locales []LocaleCode) string {
	if slices.Contains(locales, LocaleCode(query)) {
		return query
	}
	type preference struct {
		locale  string
		quality float64
	}
	var choices []preference
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		locale, quality := canonical(parts[0], locales), 1.0
		for _, parameter := range parts[1:] {
			key, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if found && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || !(parsed > 0 && parsed <= 1) {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if locale != "" && quality > 0 {
			choices = append(choices, preference{locale, quality})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].quality > choices[j].quality })
	if len(choices) > 0 {
		return choices[0].locale
	}
	return string(DefaultLocale)
}

func Fallback(requested, source string) []string {
	result := []string{}
	for _, value := range []string{requested, string(DefaultLocale), source} {
		if !Valid(value) {
			continue
		}
		duplicate := false
		for _, previous := range result {
			duplicate = duplicate || value == previous
		}
		if !duplicate {
			result = append(result, value)
		}
	}
	return result
}

type localeContextKey struct{}

func WithLocale(ctx context.Context, locale string) context.Context {
	if !Valid(locale) {
		locale = string(DefaultLocale)
	}
	return context.WithValue(ctx, localeContextKey{}, locale)
}
func FromContext(ctx context.Context) string {
	if value, ok := ctx.Value(localeContextKey{}).(string); ok && Valid(value) {
		return value
	}
	return string(DefaultLocale)
}
