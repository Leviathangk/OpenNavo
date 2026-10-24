package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestFailureDetailsIdentifyCausesWithoutPayloads(t *testing.T) {
	for _, item := range []struct {
		cause  error
		reason string
	}{
		{context.DeadlineExceeded, "deadline_exceeded"},
		{context.Canceled, "request_canceled"},
		{&pgconn.PgError{Code: "42501", Message: "private-password-value", Detail: "private-password-value"}, "database_error"},
		{errors.New("private-password-value"), "unexpected_error"},
	} {
		attrs := failureDetails(&domain.OperationError{Operation: "seo_assignment_lookup", Cause: item.cause})
		data, err := json.Marshal(attrs)
		require.NoError(t, err)
		require.Contains(t, string(data), item.reason)
		require.Contains(t, string(data), "seo_assignment_lookup")
		require.NotContains(t, string(data), "private-password-value")
		if item.reason == "database_error" {
			require.Contains(t, string(data), "42501")
		}
	}
}
