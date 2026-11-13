package public

import (
	"context"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/require"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Submission validation must reject abuse before any store write. The handler
// only needs a non-nil store to reach these branches; none of them query it.
func submissionHandler() *Handler {
	return &Handler{Catalog: &catalog.PublicService{Store: &store.Store{}}}
}

func submissionBody(kind publicapi.DeveloperSubmissionCreateRequestKind) *publicapi.DeveloperSubmissionCreateRequest {
	email := "dev@example.com"
	payload := map[string]interface{}{"name": "Example", "countryCode": "US"}
	return &publicapi.DeveloperSubmissionCreateRequest{
		Kind:         kind,
		ContactEmail: types.Email(email),
		Payload:      &payload,
	}
}

func TestCreateDeveloperSubmissionHoneypotStoresNothing(t *testing.T) {
	body := submissionBody("new")
	source := "https://github.com/example/app"
	website := "spam"
	body.SourceUrl = &source
	body.Website = &website

	response, err := submissionHandler().CreateDeveloperSubmission(context.Background(), publicapi.CreateDeveloperSubmissionRequestObject{Body: body})
	require.NoError(t, err)
	receipt, ok := response.(publicapi.CreateDeveloperSubmission200JSONResponse)
	require.True(t, ok)
	require.Zero(t, receipt.Data.Id)
	require.Empty(t, receipt.Data.Token)
}

func TestCreateDeveloperSubmissionRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		body func() *publicapi.DeveloperSubmissionCreateRequest
	}{
		{"invalid email", func() *publicapi.DeveloperSubmissionCreateRequest {
			body := submissionBody("new")
			source := "https://github.com/example/app"
			body.SourceUrl = &source
			body.ContactEmail = types.Email("not-an-email")
			return body
		}},
		{"new without source", func() *publicapi.DeveloperSubmissionCreateRequest {
			return submissionBody("new")
		}},
		{"claim without token", func() *publicapi.DeveloperSubmissionCreateRequest {
			return submissionBody("claim")
		}},
		{"oversized payload", func() *publicapi.DeveloperSubmissionCreateRequest {
			body := submissionBody("new")
			source := "https://github.com/example/app"
			body.SourceUrl = &source
			payload := map[string]interface{}{"name": "Example", "description": strings.Repeat("a", 70<<10)}
			body.Payload = &payload
			return body
		}},
		{"source url too long", func() *publicapi.DeveloperSubmissionCreateRequest {
			body := submissionBody("new")
			source := "https://example.com/" + strings.Repeat("a", 2050)
			body.SourceUrl = &source
			return body
		}},
		{"location without country", func() *publicapi.DeveloperSubmissionCreateRequest {
			body := submissionBody("location")
			payload := map[string]interface{}{"name": "Example"}
			body.Payload = &payload
			return body
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := submissionHandler().CreateDeveloperSubmission(context.Background(), publicapi.CreateDeveloperSubmissionRequestObject{Body: tc.body()})
			require.Error(t, err)
			var appErr *domain.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, domain.CodeValidation, appErr.Code)
		})
	}
}
