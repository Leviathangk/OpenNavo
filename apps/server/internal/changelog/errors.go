package changelog

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Error preserves the chain for classification, but its public string never includes URLs, credentials or response bodies.
type Error struct {
	Kind  string
	Cause error
}

func (e *Error) Error() string {
	switch e.Kind {
	case "credential_unavailable":
		return "GitHub credential unavailable"
	case "connection_failed":
		return "upstream connection failed"
	case "invalid_source_config":
		return "invalid changelog source configuration"
	case "unsupported_source_type":
		return "changelog source type is not supported"
	case "source_not_fetchable":
		return "changelog source is not fetchable"
	case "response_too_large", "response_read_failed":
		return "invalid upstream response"
	default:
		return "invalid upstream changelog response"
	}
}
func (e *Error) Unwrap() error { return e.Cause }

// Failure emits only fixed categories and numeric status codes, safe for job metrics and logs.
func Failure(err error) (string, int) {
	if errors.Is(err, context.Canceled) {
		return "canceled", 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded", 0
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "network_timeout", 0
	}

	var status *StatusError
	if errors.As(err, &status) {
		switch {
		case status.Status >= 500:
			return "upstream_server_error", status.Status
		case status.Status == 401:
			return "upstream_unauthorized", status.Status
		case status.Status == 403:
			return "upstream_forbidden", status.Status
		case status.Status == 404:
			return "upstream_not_found", status.Status
		default:
			return "upstream_http_error", status.Status
		}
	}
	var rate *RateLimitError
	if errors.As(err, &rate) {
		return "upstream_rate_limited", 0
	}
	var failure *Error
	if errors.As(err, &failure) {
		switch failure.Kind {
		case "credential_unavailable", "response_too_large", "invalid_json", "response_read_failed", "connection_failed",
			"invalid_source_config", "unsupported_source_type", "source_not_fetchable":
			return failure.Kind, 0
		}
	}
	return "unknown", 0
}
func FailureMessage(err error) string {
	category, status := Failure(err)
	if status != 0 {
		return fmt.Sprintf("upstream changelog fetch failed [%s, HTTP %d]", category, status)
	}
	return "upstream changelog fetch failed [" + category + "]"
}
