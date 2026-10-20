package agentgate

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func canonicalValue(value any) any {
	raw, _ := json.Marshal(value)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// ChangedFields records actual JSON-pointer paths, not client-declared fields.
func ChangedFields(before, after any) []string {
	out := []string{}
	var walk func(string, any, any)
	walk = func(path string, a, b any) {
		if reflect.DeepEqual(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			for k := range keys {
				escaped := strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
				walk(path+"/"+escaped, am[k], bm[k])
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		if aok && bok && len(aa) == len(ba) {
			for i := range aa {
				walk(path+"/"+strconv.Itoa(i), aa[i], ba[i])
			}
			return
		}
		out = append(out, path)
	}
	walk("", canonicalValue(before), canonicalValue(after))
	sort.Strings(out)
	return out
}

func CheckCompensation(before, after, current any, storedFields, requested []string, reason string) error {
	if reason != "implementation_error" && reason != "previous_still_valid" {
		return reject("invalid_compensation_reason")
	}
	if !reflect.DeepEqual(canonicalValue(current), canonicalValue(after)) {
		return reject("compensation_revision_changed")
	}
	actual := ChangedFields(before, after)
	saved := slices.Clone(storedFields)
	sort.Strings(saved)
	if len(actual) == 0 || !slices.Equal(actual, saved) {
		return reject("compensation_scope_unknown")
	}
	if len(requested) > 0 {
		requested = slices.Clone(requested)
		sort.Strings(requested)
		if !slices.Equal(requested, saved) {
			return reject("compensation_scope_mismatch")
		}
	}
	return nil
}

// Historical facts do not become valid merely because a rollback is requested.
// Only changed fact subtrees are passed here by the restoration adapter.
func ValidatePriorFacts(value any, evidence []Evidence, now time.Time) error {
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			if raw, ok := v["evidence"].(map[string]any); ok {
				if err := validatePriorProof(raw, evidence, now); err != nil {
					return err
				}
				source, _ := raw["sourceUrl"].(string)
				e, err := proofFor(source, evidence)
				if err != nil {
					return err
				}
				if data, ok := v["value"]; ok {
					if err := bindValues(data, e); err != nil {
						return err
					}
				}
			}
			if _, ok := v["verifiedAt"]; ok {
				if err := validatePriorProof(v, evidence, now); err != nil {
					return err
				}
				source, _ := v["sourceUrl"].(string)
				proof, err := proofFor(source, evidence)
				if err != nil {
					return err
				}
				values := map[string]any{}
				for key, value := range v {
					if key != "sourceUrl" && key != "verifiedAt" && key != "validUntil" && key != "status" && key != "note" && key != "method" {
						values[key] = value
					}
				}
				if err := bindValues(values, proof); err != nil {
					return err
				}
			}
			for key, child := range v {
				if key != "evidence" {
					if err := visit(child); err != nil {
						return err
					}
				}
			}
		case []any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(canonicalValue(value))
}

func validatePriorProof(proof map[string]any, evidence []Evidence, now time.Time) error {
	source, _ := proof["sourceUrl"].(string)
	e, err := proofFor(source, evidence)
	if err != nil {
		return err
	}
	if e.Authority != "official" {
		return reject("source_not_official")
	}
	stamp, _ := proof["checkedAt"].(string)
	if stamp == "" {
		stamp, _ = proof["verifiedAt"].(string)
	}
	checked, err := time.Parse(time.RFC3339, stamp)
	if err != nil || checked.After(now.Add(5*time.Minute)) {
		return reject("prior_fact_invalid")
	}
	until := checked.Add(30 * 24 * time.Hour)
	if raw, ok := proof["validUntil"].(string); ok {
		until, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return reject("prior_fact_invalid")
		}
	}
	if !now.Before(until) {
		return reject("prior_fact_expired")
	}
	if status, ok := proof["status"].(string); ok && status != "verified" {
		return reject("prior_fact_unverified")
	}
	return nil
}
