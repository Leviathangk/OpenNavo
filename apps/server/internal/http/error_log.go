package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/opennavo/opennavo/server/internal/domain"
)

// Raw errors may contain SQL literals, connection credentials or upstream URLs.
// Log actionable classifications and the error chain's types instead.
func failureDetails(err error) []any {
	types := []string{}
	for cause, depth := err, 0; cause != nil && depth < 12; cause, depth = errors.Unwrap(cause), depth+1 {
		types = append(types, fmt.Sprintf("%T", cause))
	}
	out := []any{"errorTypes", types}
	var operation *domain.OperationError
	if errors.As(err, &operation) {
		out = append(out, "operation", operation.Operation)
	}
	var pg *pgconn.PgError
	var network net.Error
	reason := "unexpected_error"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		reason = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		reason = "request_canceled"
	case errors.As(err, &pg):
		reason = "database_error"
		out = append(out, "sqlState", pg.Code)
	case errors.As(err, &network) && network.Timeout():
		reason = "network_timeout"
	case errors.Is(err, net.ErrClosed):
		reason = "connection_closed"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		reason = "connection_eof"
	}
	return append(out, "reason", reason)
}
