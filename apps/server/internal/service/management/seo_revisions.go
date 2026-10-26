package management

import (
	"context"
	"encoding/json"
	"strconv"
)

// nonHistoryObject names write objects without content history. Their write
// revision is entity_revisions[entityRevisionKey], bumped by database triggers.
func nonHistoryObject(object string) bool {
	return object == "background" || object == "update_feed" || object == "intent_pages" || object == "package_seo"
}

func entityRevisionKey(object string, in Input) string {
	switch object {
	case "background":
		return "background:" + in.Key
	case "update_feed":
		return "update_feed:" + text(in.Body, "source") + ":" + text(in.Body, "bundleId")
	case "intent_pages":
		return "intent:" + strconv.FormatInt(in.ID, 10)
	case "package_seo":
		return "pkgseo:" + strconv.FormatInt(in.ID, 10)
	}
	return ""
}

func (s *Service) nonHistorySnapshot(ctx context.Context, object string, in Input) (any, error) {
	switch object {
	case "background":
		rows, err := s.jsonRows(ctx, "SELECT to_jsonb(b)::text FROM background_item_content b WHERE label=? ORDER BY locale", in.Key)
		return map[string]any{"background_item_content": rows}, err
	case "intent_pages", "package_seo":
		return s.packagePageSnapshot(ctx, object, in)
	default:
		return snapshotEntity(ctx, s.Store, object, in)
	}
}

// jsonRows decodes a query that selects one JSON text column.
func (s *Service) jsonRows(ctx context.Context, query string, args ...any) ([]any, error) {
	var rows []string
	if err := s.Store.DB.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := []any{}
	for _, raw := range rows {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
