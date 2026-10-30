package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Public declarations can mention packages whose detail is unavailable. Keep those declarations,
// but remove private references from dependencies, conflicts and platform-specific requirements.
func (s *Store) privateReferences(ctx context.Context) (map[string]map[string]bool, error) {
	var refs []struct{ Kind, Token string }
	if err := s.DB.WithContext(ctx).Raw(`SELECT p.kind,p.token FROM packages p JOIN package_meta m ON m.package_id=p.id WHERE m.private
 UNION SELECT p.kind,p.full_token FROM packages p JOIN package_meta m ON m.package_id=p.id WHERE m.private
 UNION SELECT a.kind,a.token FROM package_aliases a JOIN package_meta m ON m.package_id=a.package_id WHERE m.private`).Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("read package reference visibility: %w", err)
	}
	out := map[string]map[string]bool{}
	for _, ref := range refs {
		if out[ref.Kind] == nil {
			out[ref.Kind] = map[string]bool{}
		}
		out[ref.Kind][ref.Token] = true
	}
	return out, nil
}

func filterPrivateReferences(raw json.RawMessage, private map[string]map[string]bool) (json.RawMessage, error) {
	if len(private) == 0 || len(raw) == 0 {
		return raw, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode package references: %w", err)
	}
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case []any:
			for _, child := range v {
				walk(child)
			}
		case map[string]any:
			for key, child := range v {
				kind := ""
				switch key {
				case "cask", "casks":
					kind = "cask"
				case "formula", "formulae", "runtime", "build", "test", "optional", "recommended", "usesFromMacos":
					kind = "formula"
				}
				if list, ok := child.([]any); ok && kind != "" {
					kept := make([]any, 0, len(list))
					for _, item := range list {
						token, _ := item.(string)
						hidden := private[kind][token]
						if object, ok := item.(map[string]any); ok {
							for name := range object {
								hidden = hidden || private[kind][name]
							}
						}
						if !hidden {
							kept = append(kept, item)
						}
					}
					v[key] = kept
				}
				walk(v[key])
			}
		}
	}
	walk(value)
	return json.Marshal(value)
}

func privateReplacement(value *string, private map[string]map[string]bool) *string {
	if value != nil {
		if kind, token, ok := strings.Cut(*value, ":"); ok && private[kind][token] {
			return nil
		}
	}
	return value
}
