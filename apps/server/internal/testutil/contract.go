package testutil

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// ValidateResponse validates the actual route and status; success models cannot substitute for business-error branches.
func ValidateResponse(t *testing.T, spec *openapi3.T, basePath string, request *http.Request, response *httptest.ResponseRecorder) {
	t.Helper()
	spec.Servers = openapi3.Servers{&openapi3.Server{URL: basePath}}
	route, params, err := router(t, spec).FindRoute(request)
	if err != nil {
		t.Fatal(err)
	}
	input := &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: request, PathParams: params, Route: route}, Status: response.Code, Header: response.Header(), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	input.SetBodyBytes(bytes.Clone(response.Body.Bytes()))
	if err := openapi3filter.ValidateResponse(context.Background(), input); err != nil {
		t.Fatalf("response violates contract: %v", err)
	}
}

// Building a router validates the whole document, which took most of the time
// of contract tests that check hundreds of responses, so routers are kept for
// the few documents used last. FindRoute reads doc.Servers on every call, so
// the base path set above still applies.
var routerCache struct {
	sync.Mutex
	entries []cachedRouter
}

type cachedRouter struct {
	spec   *openapi3.T
	router routers.Router
}

func router(t *testing.T, spec *openapi3.T) routers.Router {
	t.Helper()
	routerCache.Lock()
	defer routerCache.Unlock()
	for _, entry := range routerCache.entries {
		if entry.spec == spec {
			return entry.router
		}
	}
	built, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	routerCache.entries = append([]cachedRouter{{spec, built}}, routerCache.entries...)
	if len(routerCache.entries) > 4 {
		routerCache.entries = routerCache.entries[:4]
	}
	return built
}
