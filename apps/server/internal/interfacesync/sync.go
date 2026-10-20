// Package interfacesync implements incremental UI translation planning and per-key validation; it does not read or write language packs.
package interfacesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

type Target struct {
	Locales      []string                     `json:"locales,omitempty"`
	Source       map[string]string            `json:"source"`
	English      map[string]string            `json:"english"`
	Translations map[string]map[string]string `json:"translations"`
	LocaleHashes map[string]map[string]string `json:"localeHashes,omitempty"`
	Hashes       map[string]string            `json:"hashes"`
}
type Plan struct {
	Targets map[string]Target `json:"targets"`
}
type Batch struct {
	Target          string
	Locales         []string
	Source, English map[string]string
	Reasons         map[string]map[string]string
	RetryReason     string
}
type Result map[string]map[string]map[string]string

func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func ExpectedForms(source, english, code string) int {
	if strings.Contains(source, "|") || strings.Contains(english, "|") {
		return len(i18n.Metadata[i18n.LocaleCode(code)].PluralCategories)
	}
	return 1
}
func PendingReason(target Target, key, code string) string {
	value := target.Translations[code][key]
	if value == "" {
		return "missing key"
	}
	if len(strings.Split(value, "|")) != ExpectedForms(target.Source[key], target.English[key], code) {
		return "Plural form count mismatch"
	}
	if target.Hashes[key] != Hash(target.Source[key]) && target.LocaleHashes[code][key] != Hash(target.Source[key]) {
		return "source hash changed"
	}
	return ""
}

// TargetLocales is the interface language contract shared with the Node exporter.
func TargetLocales(name string) []string {
	codes := i18n.Locales[:]
	if name == "web" || name == "shared" {
		codes = i18n.SiteLocales[:]
	}
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		out = append(out, string(code))
	}
	return out
}

func translationLocales(name string, target Target) []string {
	out := []string{}
	for _, code := range TargetLocales(name) {
		if code != "en-US" && code != "zh-CN" && (len(target.Locales) == 0 || contains(target.Locales, code)) {
			out = append(out, code)
		}
	}
	return out
}

func Batches(plan Plan) []Batch {
	var out []Batch
	names := make([]string, 0, len(plan.Targets))
	for name := range plan.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := plan.Targets[name]
		locales := translationLocales(name, target)
		if len(locales) == 0 {
			continue
		}
		pending := map[string][]string{}
		for _, code := range locales {
			for key := range target.Source {
				if PendingReason(target, key, code) != "" {
					pending[code] = append(pending[code], key)
				}
			}
			sort.Strings(pending[code])
		}
		common := true
		for _, code := range locales[1:] {
			common = common && reflect.DeepEqual(pending[locales[0]], pending[code])
		}
		groups := [][]string{}
		if common && len(pending[locales[0]]) <= 40 {
			groups = append(groups, locales)
		} else {
			for _, code := range locales {
				groups = append(groups, []string{code})
			}
		}
		for _, group := range groups {
			keys := pending[group[0]]
			for start := 0; start < len(keys); start += 100 {
				source, english := map[string]string{}, map[string]string{}
				for _, key := range keys[start:min(start+100, len(keys))] {
					source[key] = target.Source[key]
					english[key] = target.English[key]
				}
				reasons := map[string]map[string]string{}
				for _, code := range group {
					reasons[code] = map[string]string{}
					for key := range source {
						reasons[code][key] = PendingReason(target, key, code)
					}
				}
				out = append(out, Batch{Target: name, Locales: group, Source: source, English: english, Reasons: reasons})
			}
		}
	}
	return out
}

type Translator func(context.Context, Batch) (map[string]map[string]string, error)

// ValidationError lets callers record specific reasons and retry only validation failures.
type ValidationError struct {
	Reason string
	Keys   map[string]map[string]string
}

func (e *ValidationError) Error() string { return e.Reason }
func Invalid(reason string) error        { return &ValidationError{Reason: reason} }

type StopError struct{ Reason string }

func (e *StopError) Error() string { return e.Reason }
func Run(ctx context.Context, plan Plan, translate Translator, protected []string, glossary map[string]map[string]string, checkpoints ...func(Batch, map[string]map[string]string) error) (Result, error) {
	result := Result{}
	for name, target := range plan.Targets {
		result[name] = map[string]map[string]string{}
		for _, code := range translationLocales(name, target) {
			result[name][code] = map[string]string{}
			for key := range target.Source {
				if PendingReason(target, key, code) == "" {
					result[name][code][key] = target.Translations[code][key]
				}
			}
		}
	}
	var failures []string
	stopped := false
	for _, batch := range Batches(plan) {
		queue := []Batch{batch}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			var output map[string]map[string]string
			var err error
			if stopped || ctx.Err() != nil {
				err = errors.New("stopped before request")
			} else {
				output, err = translate(ctx, current)
			}
			for code, fields := range output {
				if !contains(current.Locales, code) {
					err = Invalid("invalid schema: " + current.Target + "/" + code)
					output = nil
					break
				}
				for key := range fields {
					if _, ok := current.Source[key]; !ok {
						err = Invalid("invalid schema: " + current.Target + "/" + code + "/" + key)
						output = nil
						break
					}
				}
				if output == nil {
					break
				}
			}
			var validation *ValidationError
			var verdict *ValidationError
			if err == nil || errors.As(err, &validation) {
				checkErr := ValidateEach(current, output, protected, glossary)
				errors.As(checkErr, &verdict)
			}
			rejected := map[string]map[string]string{}
			for _, code := range current.Locales {
				rejected[code] = map[string]string{}
				for _, key := range sortedKeys(current.Source) {
					one := subset(current, code, key)
					value, exists := output[code][key]
					keyErr := err
					if err == nil || (errors.As(err, &validation) && exists) {
						keyErr = nil
						if verdict != nil {
							if verdict.Keys == nil {
								keyErr = verdict
							} else if reason := verdict.Keys[code][key]; reason != "" {
								keyErr = Invalid(reason)
							}
						}
					}
					if keyErr != nil {
						rejected[code][key] = keyErr.Error()
						continue
					}
					// Persist every successful key before retrying so interruption cannot lose neighboring successful keys.
					saved := map[string]map[string]string{code: {key: value}}
					for _, checkpoint := range checkpoints {
						if persistErr := checkpoint(one, saved); persistErr != nil {
							keyErr = &StopError{Reason: persistErr.Error()}
							break
						}
					}
					if keyErr != nil {
						err = keyErr
						rejected[code][key] = keyErr.Error()
						stopped = true
						continue
					}
					result[current.Target][code][key] = value
				}
			}
			var stop *StopError
			if errors.As(err, &stop) {
				stopped = true
			}
			for _, code := range current.Locales {
				if len(rejected[code]) == 0 {
					continue
				}
				retry := Batch{Target: current.Target, Locales: []string{code}, Source: map[string]string{}, English: map[string]string{}}
				reasons := []string{}
				for _, key := range sortedKeys(rejected[code]) {
					retry.Source[key] = current.Source[key]
					retry.English[key] = current.English[key]
					reasons = append(reasons, rejected[code][key])
				}
				if !stopped && ctx.Err() == nil && current.RetryReason == "" && (err == nil || errors.As(err, &validation)) {
					retry.RetryReason = strings.Join(reasons, "; ")
					queue = append(queue, retry)
				} else {
					for _, key := range sortedKeys(rejected[code]) {
						failures = append(failures, fmt.Sprintf("%s/%s/%s", current.Target, code, key))
					}
				}
			}
		}
	}
	if len(failures) > 0 {
		return result, fmt.Errorf("failed keys: %s", strings.Join(failures, ", "))
	}
	return result, nil
}
func validateStructure(batch Batch, output map[string]map[string]string, protected []string, glossary map[string]map[string]string) error {
	if len(output) != len(batch.Locales) {
		return Invalid(fmt.Sprintf("invalid schema: %s locales=%v keys=%v", batch.Target, batch.Locales, sortedKeys(batch.Source)))
	}
	for _, code := range batch.Locales {
		fields, ok := output[code]
		if !ok {
			return Invalid(fmt.Sprintf("missing key: %s/%s keys=%v", batch.Target, code, sortedKeys(batch.Source)))
		}
		for _, key := range sortedKeys(batch.Source) {
			source := batch.Source[key]
			target, ok := fields[key]
			if !ok {
				return Invalid(fmt.Sprintf("missing key: %s/%s/%s", batch.Target, code, key))
			}
			forms := strings.Split(target, "|")
			expected := ExpectedForms(source, batch.English[key], code)
			if len(forms) != expected {
				return Invalid(fmt.Sprintf("invalid plural: %s/%s/%s expected=%d actual=%d", batch.Target, code, key, expected, len(forms)))
			}
			original := strings.TrimSpace(strings.Split(source, "|")[0])
			for _, form := range forms {
				form = strings.TrimSpace(form)
				if form == "" || !reflect.DeepEqual(LiteralSpans(original), LiteralSpans(form)) {
					return Invalid(fmt.Sprintf("changed literal: %s/%s/%s source=%q translation=%q", batch.Target, code, key, LiteralSpans(original), LiteralSpans(form)))
				}
				for _, term := range protected {
					if term != "" && (countTerm(form, term) < countTerm(original, term) || countTerm(form, term) > max(countTerm(original, term), countTerm(batch.English[key], term))) {
						return Invalid(fmt.Sprintf("changed brand: %s/%s/%s term=%q", batch.Target, code, key, term))
					}
				}
				for term, translations := range glossary {
					if translated := translations[code]; translated != "" && strings.Contains(original, term) && !strings.Contains(form, translated) {
						return Invalid(fmt.Sprintf("missing glossary: %s/%s/%s term=%q", batch.Target, code, key, term))
					}
				}
			}
		}
		if len(fields) != len(batch.Source) {
			return Invalid(fmt.Sprintf("invalid schema: %s/%s keys=%v", batch.Target, code, sortedKeys(fields)))
		}
	}
	return nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func subset(batch Batch, code, key string) Batch {
	return Batch{Target: batch.Target, Locales: []string{code}, Source: map[string]string{key: batch.Source[key]}, English: map[string]string{key: batch.English[key]}}
}

func Validate(batch Batch, output map[string]map[string]string, protected []string, glossary map[string]map[string]string) error {
	if len(output) != len(batch.Locales) {
		return Invalid(fmt.Sprintf("invalid schema: %s locales=%v keys=%v", batch.Target, batch.Locales, sortedKeys(batch.Source)))
	}
	return ValidateEach(batch, output, protected, glossary)
}

func ValidateEach(batch Batch, output map[string]map[string]string, protected []string, glossary map[string]map[string]string) error {
	for code, fields := range output {
		if !contains(batch.Locales, code) {
			return Invalid("invalid schema: " + batch.Target + "/" + code)
		}
		for key := range fields {
			if _, ok := batch.Source[key]; !ok {
				return Invalid("invalid schema: " + batch.Target + "/" + code + "/" + key)
			}
		}
	}
	rejected := map[string]map[string]string{}
	reasons := []string{}
	for _, code := range batch.Locales {
		rejected[code] = map[string]string{}
		for _, key := range sortedKeys(batch.Source) {
			fields := map[string]string{}
			if value, ok := output[code][key]; ok {
				fields[key] = value
			}
			if err := validateStructure(subset(batch, code, key), map[string]map[string]string{code: fields}, protected, glossary); err != nil {
				rejected[code][key] = err.Error()
			}
		}
		for key, reason := range languageFailures(batch, code, output[code], protected, rejected[code]) {
			rejected[code][key] = reason
		}
		for _, key := range sortedKeys(rejected[code]) {
			reasons = append(reasons, rejected[code][key])
		}
	}
	if len(reasons) > 0 {
		return &ValidationError{Reason: strings.Join(reasons, "; "), Keys: rejected}
	}
	return nil
}
