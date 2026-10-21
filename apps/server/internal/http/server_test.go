package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/opennavo/opennavo/server/internal/testutil"
)

type readiness struct{ err error }

func (r readiness) Ready(context.Context) error { return r.err }
func app(t *testing.T, r readiness, log io.Writer) *httpserver.Server {
	t.Helper()
	server, err := httpserver.New(config.Config{WebBaseURL: "http://localhost:3000", AdminOrigin: "http://localhost:9527"}, slog.New(slog.NewJSONHandler(log, nil)), r)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
func sample(schema *openapi3.Schema) any {
	if schema.Example != nil {
		return schema.Example
	}
	if schema.Default != nil {
		return schema.Default
	}
	if len(schema.Enum) > 0 {
		return schema.Enum[0]
	}
	if len(schema.AnyOf) > 0 {
		return sample(schema.AnyOf[0].Value)
	}
	if len(schema.OneOf) > 0 {
		return sample(schema.OneOf[0].Value)
	}
	switch {
	case schema.Type.Is("object"):
		value := map[string]any{}
		for _, name := range schema.Required {
			value[name] = sample(schema.Properties[name].Value)
		}
		return value
	case schema.Type.Is("array"):
		values := make([]any, max(1, schema.MinItems))
		for i := range values {
			values[i] = sample(schema.Items.Value)
		}
		return values
	case schema.Type.Is("boolean"):
		return true
	case schema.Type.Is("integer"), schema.Type.Is("number"):
		number := float64(1)
		if schema.Min != nil {
			number = max(number, *schema.Min)
		}
		return number
	default:
		if schema.Format == "date-time" {
			return "2026-10-04T00:00:00Z"
		}
		if schema.Format == "email" {
			return "local@example.test"
		}
		if schema.Format == "uri" || schema.Format == "url" {
			return "https://example.test"
		}
		if schema.Pattern != "" {
			pattern, err := regexp.Compile(schema.Pattern)
			if err == nil {
				for _, candidate := range []string{"sample", "lucide:download", "R_TEST", "0.0.1", strings.Repeat("a", 64)} {
					if pattern.MatchString(candidate) {
						return candidate
					}
				}
			}
		}
		length := max(12, schema.MinLength)
		if schema.MaxLength != nil {
			length = min(length, *schema.MaxLength)
		}
		if length > 1024 {
			panic("fixture string exceeds local bound")
		}
		return strings.Repeat("a", int(length))
	}
}
func requestFor(t *testing.T, base, path, method string, item *openapi3.PathItem, operation *openapi3.Operation) *http.Request {
	t.Helper()
	query := url.Values{}
	for _, p := range append(item.Parameters, operation.Parameters...) {
		parameter := p.Value
		if !parameter.Required {
			continue
		}
		value := fmt.Sprint(sample(parameter.Schema.Value))
		if parameter.Name == "token" {
			value = "visual-studio-code"
		}
		if parameter.Name == "slug" {
			value = "sample"
		}
		if parameter.Name == "version" {
			value = "0.0.1"
		}
		if parameter.In == "path" {
			path = strings.ReplaceAll(path, "{"+parameter.Name+"}", url.PathEscape(value))
		}
		if parameter.In == "query" {
			query.Set(parameter.Name, value)
		}
	}
	var body io.Reader
	contentType := ""
	if operation.RequestBody != nil {
		content := operation.RequestBody.Value.Content
		if media := content["application/json"]; media != nil {
			data, err := json.Marshal(sample(media.Schema.Value))
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(data)
			contentType = "application/json"
		}
		if media := content["multipart/form-data"]; media != nil {
			buffer := &bytes.Buffer{}
			writer := multipart.NewWriter(buffer)
			required := append([]string(nil), media.Schema.Value.Required...)
			// Exclusive requests must select a branch, rather than generate only shared required fields.
			if len(media.Schema.Value.OneOf) > 0 {
				required = append(required, media.Schema.Value.OneOf[0].Value.Required...)
			}
			for _, name := range required {
				if media.Schema.Value.Properties[name].Value.Format != "binary" {
					if err := writer.WriteField(name, fmt.Sprint(sample(media.Schema.Value.Properties[name].Value))); err != nil {
						t.Fatal(err)
					}
					continue
				}
				part, err := writer.CreateFormFile(name, "sample.png")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write([]byte("sample")); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			body = buffer
			contentType = writer.FormDataContentType()
		}
	}
	request := httptest.NewRequest(method, base+path+"?"+query.Encode(), body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request
}
func TestAllPlaceholderRoutesFollowContracts(t *testing.T) {
	server := app(t, readiness{}, io.Discard)
	count := 0
	for _, group := range []struct {
		base   string
		load   func() (*openapi3.T, error)
		status int
	}{{"/api/v1", publicapi.GetSpec, 500}, {"/admin-api", adminapi.GetSpec, 200}} {
		spec, err := group.load()
		if err != nil {
			t.Fatal(err)
		}
		for path, item := range spec.Paths.Map() {
			for method, operation := range item.Operations() {
				t.Run(method+" "+group.base+path, func(t *testing.T) {
					request := requestFor(t, group.base, path, method, item, operation)
					response := httptest.NewRecorder()
					server.Engine.ServeHTTP(response, request)
					var data respond.Envelope
					if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
						t.Fatal(err)
					}
					expectedCode := domain.CodeInternal
					// Introspection belongs only to the internal entry point; the generated admin placeholder must explicitly reject it.
					if group.base == "/admin-api" && path == "/introspect" && method == http.MethodPost {
						expectedCode = domain.CodeForbidden
					}
					if response.Code != group.status || data.Code != expectedCode || data.Data != nil {
						t.Fatalf("unexpected placeholder response: status=%d body=%s", response.Code, response.Body.String())
					}
					testutil.ValidateResponse(t, spec, group.base, request, response)
				})
				count++
			}
		}
	}
	if count != 231 {
		t.Fatalf("expected 231 contracted routes, got %d", count)
	}
}
func TestHealthValidationLocaleAndCORS(t *testing.T) {
	server := app(t, readiness{}, io.Discard)
	cases := []struct {
		path, method, body, origin, language, code string
		status                                     int
	}{
		{"/healthz", "GET", "", "", "", domain.CodeOK, 200}, {"/readyz", "GET", "", "", "", domain.CodeOK, 200},
		{"/api/v1/packages?size=101", "GET", "", "", "", domain.CodeValidation, 400},
		{"/api/v1/packages/bad/abc", "GET", "", "", "", domain.CodeValidation, 400},
		{"/admin-api/auth/login", "POST", "{", "", "", domain.CodeValidation, 200},
		{"/admin-api/auth/login", "POST", "{}", "", "", domain.CodeValidation, 200},
		{"/api/v1/home", "GET", "", "https://other.test", "", domain.CodeForbidden, 403},
		{"/admin-api/dashboard/overview", "GET", "", "http://localhost:3000", "", domain.CodeForbidden, 200},
		{"/api/v1/home?locale=en-US", "GET", "", "http://localhost:3000", "zh-CN", domain.CodeInternal, 500},
		{"/api/v1/home", "GET", "", "tauri://localhost", "en-US", domain.CodeInternal, 500},
		{"/undefined", "GET", "", "", "", domain.CodeNotFound, 404},
	}
	for _, c := range cases {
		t.Run(c.path+c.body+c.origin, func(t *testing.T) {
			request := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			if c.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			request.Header.Set("Origin", c.origin)
			request.Header.Set("Accept-Language", c.language)
			request.Header.Set("X-Request-ID", "sample-request")
			response := httptest.NewRecorder()
			server.Engine.ServeHTTP(response, request)
			var data respond.Envelope
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if response.Code != c.status || data.Code != c.code || response.Header().Get("X-Request-ID") != "sample-request" {
				t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(c.path, "locale=en-US") && data.Msg != "Service temporarily unavailable" {
				t.Fatal("query locale must override header")
			}
		})
	}
	failed := app(t, readiness{errors.New("unavailable")}, io.Discard)
	response := httptest.NewRecorder()
	failed.Engine.ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil))
	if response.Code != 503 {
		t.Fatal("unready dependencies must return 503")
	}
	request := httptest.NewRequest("OPTIONS", "/api/v1/home", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	response = httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal("preflight failed")
	}
}
func TestErrorsDoNotLeakSensitiveValuesAndRefreshNeverExpires(t *testing.T) {
	var logs bytes.Buffer
	server := app(t, readiness{}, &logs)
	server.Engine.GET("/test-error", func(c *gin.Context) { respond.Error(c, errors.New("private-password-value"), false) })
	server.Engine.GET("/test/auth/refreshToken", func(c *gin.Context) { respond.Error(c, &domain.AppError{Code: domain.CodeAccessExpired}, true) })
	server.Engine.GET("/test-panic", func(_ *gin.Context) { panic("private-password-value") })
	for _, path := range []string{"/test-error", "/test/auth/refreshToken", "/test-panic"} {
		response := httptest.NewRecorder()
		server.Engine.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if strings.Contains(response.Body.String(), "private-password-value") {
			t.Fatal("response leaked a sensitive value")
		}
		if strings.Contains(path, "refreshToken") && strings.Contains(response.Body.String(), domain.CodeAccessExpired) {
			t.Fatal("refresh returned access-expired code")
		}
	}
	if strings.Contains(logs.String(), "private-password-value") {
		t.Fatal("logs leaked a sensitive value")
	}
	metrics := httptest.NewRecorder()
	server.Metrics.ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), "status="+strconv.Quote("500")) {
		t.Fatal("HTTP status metrics missing")
	}
}

type probe func(context.Context) error

func (p probe) Ready(ctx context.Context) error { return p(ctx) }
func TestReadinessInheritsRequestCancellation(t *testing.T) {
	observed := false
	server, err := httpserver.New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), probe(func(ctx context.Context) error { observed = ctx.Err() != nil; return ctx.Err() }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "/readyz", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if !observed || response.Code != 503 {
		t.Fatal("request cancellation was lost")
	}
}

func TestClientIPOnlyTrustsConfiguredProxy(t *testing.T) {
	for _, sample := range []struct {
		name, remote, forwarded, real, want string
		trusted                             []string
	}{
		{"SSR web forwards visitor", "172.30.81.3:1234", "203.0.113.1", "", "203.0.113.1", []string{"172.30.81.0/24"}},
		{"direct ignores headers", "192.0.2.1:1234", "203.0.113.1", "203.0.113.2", "192.0.2.1", nil},
		{"proxy forwards first client", "172.30.80.2:1234", "203.0.113.1", "", "203.0.113.1", []string{"172.30.80.2"}},
		{"proxy forwards second client", "172.30.80.2:1234", "203.0.113.2", "", "203.0.113.2", []string{"172.30.80.2"}},
		{"untrusted peer ignores headers", "172.30.80.3:1234", "203.0.113.1", "", "172.30.80.3", []string{"172.30.80.2"}},
		{"spoofed prefix ignored", "172.30.80.2:1234", "198.51.100.5, 203.0.113.1", "", "203.0.113.1", []string{"172.30.80.2"}},
		{"malformed forwarded ignored", "172.30.80.2:1234", "bad-address", "203.0.113.2", "172.30.80.2", []string{"172.30.80.2"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			server, err := httpserver.New(config.Config{TrustedProxies: sample.trusted}, slog.New(slog.NewTextHandler(io.Discard, nil)), readiness{})
			if err != nil {
				t.Fatal(err)
			}
			server.Engine.GET("/ip", func(c *gin.Context) { c.String(200, c.ClientIP()) })
			request := httptest.NewRequest("GET", "/ip", nil)
			request.RemoteAddr = sample.remote
			request.Header.Set("X-Forwarded-For", sample.forwarded)
			request.Header.Set("X-Real-IP", sample.real)
			response := httptest.NewRecorder()
			server.Engine.ServeHTTP(response, request)
			if response.Body.String() != sample.want {
				t.Fatalf("want %s, got %s", sample.want, response.Body.String())
			}
		})
	}
	_, err := httpserver.New(config.Config{TrustedProxies: []string{"invalid"}}, slog.New(slog.NewTextHandler(io.Discard, nil)), readiness{})
	if err == nil {
		t.Fatal("invalid proxy configuration must fail startup")
	}
}
