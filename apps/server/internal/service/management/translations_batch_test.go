package management

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTranslationFailureReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"validation reason", domain.Rejected("translation_locale_is_source"), "validation_failed:rejected: translation_locale_is_source"},
		{"not found", gorm.ErrRecordNotFound, "content_not_found"},
		{"conflict", &pgconn.PgError{Code: "23505", Detail: "private database value"}, "state_conflict"},
		{"invalid state", &pgconn.PgError{Code: "23503", Detail: "private database value"}, "invalid_state"},
		{"revision", fmt.Errorf("wrapped: %w", errTranslationRevision), "revision_conflict"},
		{"idempotency", agentgate.ErrIdempotencyKeyReused, "idempotency_key_reused"},
		{"permission", domain.Forbidden(403, "permission_required:translation:review"), "permission_denied"},
		{"cancelled", context.Canceled, "request_cancelled"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
		{"unstructured validation", &domain.AppError{Code: domain.CodeValidation, Msg: "private database value"}, "validation_failed"},
		{"internal", errors.New("private database value"), "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.reason, translationFailureReason(tc.err))
		})
	}
}
