package management

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf16"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Merge only after the write lock and idempotency lookup. The recorded request
// remains the caller's patch, independent of later reads or revision changes.
func (s *Service) mergeContentUpdate(ctx context.Context, op string, in Input) (Input, error) {
	entity, read := "category", "GetCategoryTree"
	keys := []string{"parentId", "slug", "icon", "appliesTo", "visible", "hiddenByDefault", "sourceLocale"}
	switch op {
	case "UpdateCollection":
		entity, read = "collection", "GetAdminCollection"
		keys = []string{"slug", "sort", "unpublishAt", "coverAssetId", "sourceLocale"}
	case "UpdateFeature":
		entity, read = "feature", "GetFeature"
		keys = []string{"placement", "categoryId", "targetType", "packageId", "collectionId", "url", "glowColor", "status", "startsAt", "endsAt", "sort", "sourceLocale"}
	}
	result, err := s.execute(ctx, read, Input{ID: in.ID})
	if err != nil {
		return in, err
	}
	if _, guarded := in.Body["expectedState"]; guarded {
		result, err = s.withReadRevision(ctx, read, Input{ID: in.ID}, result)
		if err != nil {
			return in, err
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return in, err
	}
	var before map[string]any
	if read == "GetCategoryTree" {
		var nodes []map[string]any
		if err := json.Unmarshal(raw, &nodes); err != nil {
			return in, err
		}
		before = categoryNode(nodes, in.ID)
		if before == nil {
			return in, fail(domain.CodeNotFound)
		}
	} else if err := json.Unmarshal(raw, &before); err != nil {
		return in, err
	}
	if expected, supplied := in.Body["expectedState"]; supplied {
		hash, err := partialStateHash(before)
		if err != nil {
			return in, err
		}
		if expected != hash {
			return in, fail(domain.CodeConflict)
		}
	}
	body := map[string]any{}
	for _, key := range keys {
		if value, ok := before[key]; ok {
			body[key] = value
		}
	}
	for key, value := range in.Body {
		if key != "expectedState" && key != "i18n" {
			body[key] = value
		}
	}
	current, _ := before["i18n"].(map[string]any)
	requested, ok := in.Body["i18n"].(map[string]any)
	if _, supplied := in.Body["i18n"]; supplied && !ok {
		return in, domain.Rejected("i18n_required")
	}
	locales := map[string]bool{text(body, "sourceLocale"): true}
	for locale := range requested {
		locales[locale] = true
	}
	columns := map[string]any{}
	for column := range content.Registry[entity].Fields {
		columns[column] = nil
	}
	allowed := store.ContentAPIFields(columns)
	localized := map[string]any{}
	for locale := range locales {
		fields := map[string]any{}
		old, _ := current[locale].(map[string]any)
		for key := range allowed {
			if value, ok := old[key]; ok {
				fields[key] = value
			}
		}
		if value, supplied := requested[locale]; supplied {
			patch, ok := value.(map[string]any)
			if !ok {
				localized[locale] = value
				continue
			}
			for key, value := range patch {
				fields[key] = value
			}
		}
		if _, supplied := requested[locale]; supplied || len(fields) > 0 {
			required := "title"
			if entity == "category" {
				required = "name"
			}
			if _, ok := fields[required].(string); !ok {
				return in, domain.Rejected("field_required:" + required)
			}
			localized[locale] = fields
		}
	}
	body["i18n"] = localized
	in.Body = body
	return in, nil
}

func categoryNode(nodes []map[string]any, id int64) map[string]any {
	for _, node := range nodes {
		if valueID(node["id"]) == id {
			return node
		}
		children, _ := node["children"].([]any)
		for _, child := range children {
			if row, ok := child.(map[string]any); ok {
				if found := categoryNode([]map[string]any{row}, id); found != nil {
					return found
				}
			}
		}
	}
	return nil
}

// Match the original MCP digest: Python json.dumps(sort_keys=True,
// separators=(",", ":")), including ASCII escapes and UTF-16 surrogate pairs.
func partialStateHash(value any) (string, error) {
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	var ascii bytes.Buffer
	for _, r := range string(bytes.TrimSuffix(raw.Bytes(), []byte{'\n'})) {
		switch {
		case r < 0x7f:
			ascii.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&ascii, "\\u%04x", r)
		default:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&ascii, "\\u%04x\\u%04x", hi, lo)
		}
	}
	sum := sha256.Sum256(ascii.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
