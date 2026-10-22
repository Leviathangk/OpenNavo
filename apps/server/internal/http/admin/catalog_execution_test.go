package admin

import (
	"net/http"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestOptionalCatalogExecution(t *testing.T) {
	_, err := optionalCatalogExecution("updatePackageMeta", http.Header{}, true)
	require.Error(t, err, "an unleased catalog edit still needs an idempotency key")
	_, err = optionalCatalogExecution("publishArticle", http.Header{}, false)
	require.Error(t, err)

	headers := http.Header{}
	headers.Set("Idempotency-Key", "catalog-edit-001")
	execution, err := optionalCatalogExecution("updatePackageMeta", headers, true)
	require.NoError(t, err, "a preview may omit the revision it reports")
	require.Zero(t, execution.TaskID)
	require.False(t, execution.ExpectedRevisionSet)
	_, err = optionalCatalogExecution("updatePackageMeta", headers, false)
	var app *domain.AppError
	require.ErrorAs(t, err, &app, "a commit must name the revision its preview returned")
	require.Equal(t, "rejected: expected_revision_required", app.Msg)

	headers.Set("X-SEO-Expected-Revision", "4")
	headers.Set("X-SEO-Evidence-IDs", "9,10")
	execution, err = optionalCatalogExecution("updatePackageMeta", headers, false)
	require.NoError(t, err)
	require.True(t, execution.ExpectedRevisionSet)
	require.EqualValues(t, 4, execution.ExpectedRevision)
	require.Equal(t, []int64{9, 10}, execution.EvidenceIDs)

	headers.Set("X-SEO-Task-ID", "12")
	_, err = optionalCatalogExecution("updatePackageMeta", headers, false)
	require.Error(t, err, "a partial lease must not silently become an unleased edit")
	headers.Set("X-SEO-Fencing", "3")
	execution, err = optionalCatalogExecution("updatePackageMeta", headers, false)
	require.NoError(t, err)
	require.EqualValues(t, 12, execution.TaskID)
	require.EqualValues(t, 4, execution.ExpectedRevision)
}
