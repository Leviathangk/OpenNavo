package management

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
)

// requestField is a body field name a rejection may repeat: a JSON path of letters, digits and _.
var requestField = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}(\.[A-Za-z0-9_]{1,64}){0,7}$`)

// decodeWriteBody decodes a write body strictly and names what is wrong: unknown_field:<name>,
// field_invalid:<path> for a value of the wrong type, otherwise body_invalid.
func decodeWriteBody(body map[string]any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return domain.Rejected("body_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(out)
	if err == nil {
		return nil
	}
	var typed *json.UnmarshalTypeError
	if errors.As(err, &typed) && requestField.MatchString(typed.Field) {
		return domain.Rejected("field_invalid:" + typed.Field)
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return unknownField(strings.Trim(name, `"`))
	}
	return domain.Rejected("body_invalid")
}

// unknownField names an unexpected body field when its name is a plain identifier.
func unknownField(name string) error {
	if requestField.MatchString(name) {
		return domain.Rejected("unknown_field:" + name)
	}
	return domain.Rejected("unknown_field")
}

// packagePageSnapshot is the write-layer state of an app's intent page switches or overview overrides:
// the stored rows, compared before and after an agent write and shown in its preview.
func (s *Service) packagePageSnapshot(ctx context.Context, object string, in Input) (any, error) {
	if object == "intent_pages" {
		rows, err := s.jsonRows(ctx, "SELECT (to_jsonb(i)-'created_at')::text FROM package_intent_pages i WHERE package_id=? ORDER BY page_kind,locale", in.ID)
		return map[string]any{"package_intent_pages": rows}, err
	}
	rows, err := s.jsonRows(ctx, "SELECT to_jsonb(o)::text FROM package_seo_overrides o WHERE package_id=? ORDER BY locale", in.ID)
	return map[string]any{"package_seo_overrides": rows}, err
}

// packagePageURLs are the pages an intent switch or overview override write affects.
func (s *Service) packagePageURLs(ctx context.Context, object string, in Input) ([]string, error) {
	var p struct{ Token string }
	if err := s.Store.DB.WithContext(ctx).Raw("SELECT token FROM packages WHERE id=?", in.ID).Scan(&p).Error; err != nil || p.Token == "" {
		return []string{}, err
	}
	if object == "package_seo" {
		return overviewURLs(s.Config.WebBaseURL, p.Token, text(in.Body, "locale")), nil
	}
	changes, err := decodeIntentChanges(in.Body)
	if err != nil {
		return []string{}, nil
	}
	return intentURLs(s.Config.WebBaseURL, p.Token, changes), nil
}
