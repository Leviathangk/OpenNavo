package store

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
)

type TranslationValue struct {
	Locale     string         `json:"locale"`
	Revision   string         `json:"revision"`
	Status     string         `json:"status"`
	SourceHash string         `json:"sourceHash"`
	Model      *string        `json:"model"`
	Fields     map[string]any `json:"fields"`
}

// Revision covers the entire persisted language row, including provenance and
// update time, so intervening edits (including edit-and-revert) invalidate a CAS.
func (s *Store) TranslationValue(ctx context.Context, ref content.Ref, locale string) (TranslationValue, error) {
	out := TranslationValue{Locale: locale, Revision: content.JSONHash([]byte("null")), Status: "missing", Fields: map[string]any{}}
	if !ref.Valid() {
		return out, domain.Rejected("content_ref_invalid")
	}
	if !i18n.Valid(locale) {
		return out, domain.RejectedDetail("locale_invalid", locale)
	}
	spec, ok := content.Registry[ref.Entity]
	if !ok {
		return out, domain.Rejected("content_ref_invalid")
	}
	where, args := textWhere(ref)
	var raw json.RawMessage
	if err := s.contentJSON(ctx, &raw, "SELECT to_jsonb(i) FROM "+spec.Table+" i WHERE "+where+" AND locale=?", append(args, locale)...); err != nil {
		return out, err
	}
	if len(raw) == 0 {
		return out, nil
	}
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		return out, err
	}
	out.Revision = content.JSONHash(raw)
	out.Status, _ = row["status"].(string)
	out.SourceHash, _ = row["source_hash"].(string)
	if model, ok := row["model"].(string); ok {
		out.Model = &model
	}
	for field := range spec.Fields {
		out.Fields[textAPIKey(field)] = row[field]
	}
	return out, nil
}

func ContentAPIFields(fields map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range fields {
		out[textAPIKey(k)] = v
	}
	return out
}
