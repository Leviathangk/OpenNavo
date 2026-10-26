package agentgate

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
