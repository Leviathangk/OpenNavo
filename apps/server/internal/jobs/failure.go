package jobs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/opennavo/opennavo/server/internal/changelog"
	"gorm.io/gorm"
)

// Extract fixed categories only from error types; third-party responses, URLs, paths and credentials must never enter job ledgers or logs.
func failureCategory(err error) string {
	switch {
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return "storage_full"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, syscall.EROFS):
		return "read_only_filesystem"
	case errors.Is(err, syscall.EIO):
		return "storage_io"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	// Changelog errors carry a more actionable fixed category even when their cause is
	// gorm.ErrRecordNotFound (for example source_not_fetchable).
	if category, _ := changelog.Failure(err); category != "unknown" {
		return category
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "record_not_found"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "network_timeout"
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		return "network_error"
	}
	category, _ := changelog.Failure(err)
	return category
}

// failureDetail describes the underlying cause for the job-run ledger. Handler errors can embed
// upstream response bodies, URLs with credentials, file paths, and user data, so this never copies an
// arbitrary message: it names known sentinel errors and changelog failure types, and otherwise reports
// only the Go type of the deepest cause. The result is bounded and safe to persist and display.
func failureDetail(err error) string {
	const limit = 120
	detail := describeFailure(err)
	if len(detail) > limit {
		return detail[:limit-3] + "…"
	}
	return detail
}

func describeFailure(err error) string {
	for range 16 {
		if err == nil {
			return ""
		}
		switch {
		case errors.Is(err, context.Canceled):
			return "context canceled"
		case errors.Is(err, context.DeadlineExceeded):
			return "context deadline exceeded"
		case errors.Is(err, errHandlerPanic):
			return errHandlerPanic.Error()
		}
		var failure *changelog.Error
		if errors.As(err, &failure) {
			return "changelog " + failure.Kind
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "record not found"
		}
		var status *changelog.StatusError
		if errors.As(err, &status) {
			return fmt.Sprintf("upstream HTTP %d", status.Status)
		}
		var rate *changelog.RateLimitError
		if errors.As(err, &rate) {
			return "upstream rate limited"
		}
		next := primaryCause(err)
		if next == nil {
			return fmt.Sprintf("%T", err)
		}
		err = next
	}
	return "cause chain too deep"
}

// primaryCause follows the first branch of a joined or wrapped error, which is the branch handlers put
// the real failure in before adding control-flow sentinels such as asynq.SkipRetry.
func primaryCause(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if cause != nil {
				return cause
			}
		}
		return nil
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return wrapped.Unwrap()
	}
	return nil
}
