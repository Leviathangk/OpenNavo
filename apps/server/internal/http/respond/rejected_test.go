package respond

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestRejectedPreservesAgentReasonAndShowsItInAdmin(t *testing.T) {
	for _, locale := range []string{"en-US", "zh-CN", "de-DE"} {
		for _, prefix := range []string{"/admin-api", "/agent-api", "/api"} {
			t.Run(prefix+"/"+locale, func(t *testing.T) {
				out := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(out)
				c.Request = httptest.NewRequest(http.MethodPut, prefix+"/packages/42", nil)
				c.Request.Header.Set("Accept-Language", locale)
				Error(c, fmt.Errorf("write failed: %w", domain.Rejected("field_too_long:package.summary")), prefix != "/api")
				var envelope Envelope
				require.NoError(t, json.Unmarshal(out.Body.Bytes(), &envelope))
				require.Equal(t, domain.CodeValidation, envelope.Code)
				require.Nil(t, envelope.Data)
				if prefix == "/admin-api" {
					require.Equal(t, domain.Message(domain.CodeValidation, locale)+" (field_too_long:package.summary)", envelope.Msg)
				} else {
					require.Equal(t, "rejected: field_too_long:package.summary", envelope.Msg)
				}
				if prefix == "/api" {
					require.Equal(t, http.StatusBadRequest, out.Code)
				} else {
					require.Equal(t, http.StatusOK, out.Code)
				}
			})
		}
	}
}
