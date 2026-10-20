package jobs_test

import (
	"errors"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/stretchr/testify/require"
)

func TestOutboxDuplicatesAndFailures(t *testing.T) {
	duplicate := &domain.AppError{Code: domain.CodeInvalidState, HTTPStatus: 409}
	for _, kind := range []string{"catalog:sync"} {
		require.NoError(t, jobs.OutboxDeliveryError(kind, duplicate))
		require.NoError(t, jobs.OutboxDeliveryError(kind, nil))
		failure := errors.New("queue unavailable")
		require.ErrorIs(t, jobs.OutboxDeliveryError(kind, failure), failure)
	}
	require.NoError(t, jobs.OutboxDeliveryError("catalog:sync", duplicate))
}
