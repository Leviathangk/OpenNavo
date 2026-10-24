package domain

import (
	"net/http"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

type AppError struct {
	Code       string
	Msg        string
	HTTPStatus int
	Cause      error
}

func (e *AppError) Error() string { return e.Code + ": " + e.Msg }
func (e *AppError) Unwrap() error { return e.Cause }
func Internal(cause error) *AppError {
	return &AppError{Code: CodeInternal, HTTPStatus: http.StatusInternalServerError, Cause: cause}
}
func Validation() *AppError {
	return &AppError{Code: CodeValidation, HTTPStatus: http.StatusBadRequest}
}

// Rejected is a validation failure that names its cause, so agents can fix the request instead of
// guessing. The agent API forwards "rejected: <reason>" to MCP clients unchanged. reason is lowercase
// snake_case, optionally followed by ":<detail>" naming the field or locale (letters, digits and
// "._-[]" only). Never put user-supplied values in either part.
func Rejected(reason string) *AppError {
	return &AppError{Code: CodeValidation, HTTPStatus: http.StatusBadRequest, Msg: "rejected: " + reason}
}

func Message(code, locale string) string {
	return i18n.Message(code, locale)
}
