package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/stretchr/testify/require"
)

func TestPartialUpdateRequestValidation(t *testing.T) {
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	validate, err := Validate(spec, "/agent-api", true)
	require.NoError(t, err)
	router := gin.New()
	for _, path := range []string{"/categories/:id", "/collections/:id", "/features/:id"} {
		router.PUT("/agent-api"+path, validate, func(c *gin.Context) { c.String(200, "passed") })
	}
	for _, tc := range []struct {
		path, body string
		valid      bool
	}{
		{"/categories/1", `{"visible":false}`, true},
		{"/categories/1", `{"i18n":{"de-DE":{"description":"Partial text"}}}`, true},
		{"/collections/1", `{"sort":3,"expectedState":"unchanged-digest"}`, true},
		{"/collections/1", `{"i18n":{"de-DE":null}}`, true},
		{"/features/1", `{"i18n":{"de-DE":{"badge":"Partial"}}}`, true},
		{"/features/1", `{"i18n":{"de-DE":null}}`, true},
		{"/categories/1", `{"i18n":{"de-DE":{"name":"` + strings.Repeat("字", 31) + `"}}}`, false},
		{"/categories/1", `{"i18n":{"de-DE":null}}`, false},
		{"/collections/1", `{"i18n":{"unknown":{"title":"Invalid"}}}`, false},
		{"/features/1", `{"i18n":{"de-DE":{"badge":"Too long badge"}}}`, false},
	} {
		req := httptest.NewRequest("PUT", "/agent-api"+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if tc.valid {
			require.Equal(t, "passed", recorder.Body.String(), tc.path+" "+tc.body)
		} else {
			require.Contains(t, recorder.Body.String(), `"code":"1001"`, tc.path+" "+tc.body)
		}
	}
	for _, schema := range []string{"CategoryUpsert", "CollectionUpsert", "FeatureUpsert"} {
		require.Error(t, spec.Components.Schemas[schema].Value.VisitJSON(map[string]any{}), "Creation still requires complete content")
	}
}
