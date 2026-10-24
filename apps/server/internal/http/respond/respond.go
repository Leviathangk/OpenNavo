package respond

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
)

type Envelope struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

func Locale(c *gin.Context) string {
	return i18n.Negotiate(c.Query("locale"), c.GetHeader("Accept-Language"))
}
func OK(c *gin.Context, data any) {
	if data == nil {
		data = struct{}{}
	}
	c.JSON(http.StatusOK, Envelope{Code: domain.CodeOK, Msg: domain.Message(domain.CodeOK, Locale(c)), Data: data})
}
func Error(c *gin.Context, err error, admin bool) {
	appErr := domain.Internal(err)
	var known *domain.AppError
	if errors.As(err, &known) {
		appErr = known
	}
	status := appErr.HTTPStatus
	if status < 400 {
		status = http.StatusInternalServerError
	}
	if admin {
		status = http.StatusOK
	}
	msg := appErr.Msg
	// Both Agent API and admin JWT handlers use admin=true (HTTP 200 envelopes).
	// Preserve machine-readable reasons for MCP, while keeping the admin UI localized.
	if admin && !strings.HasPrefix(c.Request.URL.Path, "/agent-api/") && strings.HasPrefix(msg, "rejected: ") {
		msg = domain.Message(appErr.Code, Locale(c))
	}
	if msg == "" {
		msg = domain.Message(appErr.Code, Locale(c))
	}
	code := appErr.Code
	// No refresh-chain error may trigger client refresh logic again.
	if strings.HasSuffix(c.Request.URL.Path, "/auth/refreshToken") && code == domain.CodeAccessExpired {
		code = domain.CodeUnauthenticated
		msg = domain.Message(code, Locale(c))
	}
	c.AbortWithStatusJSON(status, Envelope{Code: code, Msg: msg, Data: nil})
}

// oapi-codegen generates Body wrappers for endpoints declaring response headers; decode other responses directly.
func DecodeResponse(data []byte, target any) error {
	value := reflect.ValueOf(target).Elem()
	if value.Kind() == reflect.Struct {
		if body := value.FieldByName("Body"); body.IsValid() && body.CanAddr() {
			return json.Unmarshal(data, body.Addr().Interface())
		}
	}
	return json.Unmarshal(data, target)
}
