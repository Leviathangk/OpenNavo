package admin

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionalCatalogExecution(t *testing.T) {
	for _, operation := range []string{"updatePackageMeta", "publishArticle", "addLinkPackage", "uploadAsset", "createCategory"} {
		for _, dry := range []bool{true, false} {
			execution, err := optionalCatalogExecution(operation, http.Header{}, dry)
			require.NoError(t, err, operation)
			require.False(t, execution.ExpectedRevisionSet)
			require.Empty(t, execution.IdempotencyKey)
		}
	}
	headers := http.Header{}
	headers.Set("Idempotency-Key", "optional-retry-001")
	headers.Set("X-Expected-Revision", "4")
	headers.Set("X-SEO-Expected-Revision", "9")
	execution, err := optionalCatalogExecution("publishArticle", headers, false)
	require.NoError(t, err)
	require.True(t, execution.ExpectedRevisionSet)
	require.EqualValues(t, 4, execution.ExpectedRevision, "the current header wins over the legacy one")
	require.Equal(t, "optional-retry-001", execution.IdempotencyKey)
	headers.Del("X-Expected-Revision")
	execution, err = optionalCatalogExecution("publishArticle", headers, false)
	require.NoError(t, err)
	require.EqualValues(t, 9, execution.ExpectedRevision, "the legacy header is still read for one release")
	headers.Set("X-Expected-Revision", "-1")
	_, err = optionalCatalogExecution("publishArticle", headers, false)
	require.Error(t, err, "an explicitly supplied revision must be valid")
}
