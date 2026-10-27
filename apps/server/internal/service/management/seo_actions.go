package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

func mapWriteError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
	}
	return agentgate.WriteError(err)
}

// actionRevision is the newest content revision of the edited history objects.
func actionRevision(ctx context.Context, st *store.Store, refs []content.Ref) (int64, error) {
	var current int64
	for _, ref := range refs {
		var n int64
		query, args := "SELECT coalesce(max(id),0) FROM content_revisions WHERE entity=? AND object_key=?", []any{ref.Entity, ref.Key()}
		if ref.Entity == "package" {
			// Include deleted screenshots through their retained snapshots too;
			// otherwise deleting the newest screenshot could rewind this revision.
			query += " OR (entity='screenshot' AND COALESCE(after_data#>>'{package_screenshots,0,package_id}',before_data#>>'{package_screenshots,0,package_id}')=?)"
			args = append(args, ref.Key())
		}
		if err := st.DB.WithContext(ctx).Raw(query, args...).Scan(&n).Error; err != nil {
			return 0, err
		}
		current = max(current, n)
	}
	return current, nil
}

func decodeStrictBody(body map[string]any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return domain.Rejected("body_invalid")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return bodyError(err)
	}
	return nil
}

// bodyError names the field a strict JSON decode refused when it can.
func bodyError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return domain.RejectedDetail("field_type_invalid", typeErr.Field)
	}
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return domain.RejectedDetail("unknown_field", strings.Trim(field, `"`))
	}
	return domain.Rejected("body_invalid")
}

// rejectedOr keeps a reason the callee already named and names fallback otherwise.
func rejectedOr(err error, fallback string) error {
	var app *domain.AppError
	if errors.As(err, &app) && strings.HasPrefix(app.Msg, "rejected: ") {
		return app
	}
	return domain.Rejected(fallback)
}
