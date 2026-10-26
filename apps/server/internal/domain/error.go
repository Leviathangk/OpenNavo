package domain

import (
	"net/http"
	"regexp"
	"strings"

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

var detailPattern = regexp.MustCompile(`^[A-Za-z0-9_.@+/-]{1,64}$`)

// RejectedDetail is Rejected with details taken from the request, such as an
// unknown field name. Details are joined with ":"; the first one outside the
// safe alphabet ends the list, so caller input is never echoed raw.
func RejectedDetail(reason string, details ...string) *AppError {
	for _, detail := range details {
		if !detailPattern.MatchString(detail) || strings.Contains(detail, "//") {
			break
		}
		reason += ":" + detail
	}
	return Rejected(reason)
}

// Forbidden keeps the forbidden code and the caller's HTTP status and names the
// refusal: permission_required:<permission>, delete_not_allowed or
// operation_not_exposed.
func Forbidden(status int, reason string) *AppError {
	return &AppError{Code: CodeForbidden, HTTPStatus: status, Msg: "rejected: " + reason}
}

func Message(code, locale string) string {
	return i18n.Message(code, locale)
}
