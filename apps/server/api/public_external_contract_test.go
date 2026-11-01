package api_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestPublicExternalContractIsReadOnlyAndExcludesInternalRoutes(t *testing.T) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	spec, err := loader.LoadFromFile("public-external.openapi.yaml")
	require.NoError(t, err)
	require.NoError(t, spec.Validate(context.Background()))

	expected := map[string]struct{}{
		"/categories":                           {},
		"/search":                               {},
		"/packages/{kind}/{token}":              {},
		"/packages/{kind}/{token}/releases":     {},
		"/packages/{kind}/{token}/dependencies": {},
		"/packages/{kind}/{token}/related":      {},
		"/rankings":                             {},
		"/collections":                          {},
		"/collections/{slug}":                   {},
		"/collections/{slug}/brewfile":          {},
		"/articles":                             {},
		"/articles/{slug}":                      {},
	}
	require.Equal(t, expected, func() map[string]struct{} {
		actual := map[string]struct{}{}
		for path := range spec.Paths.Map() {
			actual[path] = struct{}{}
		}
		return actual
	}())

	for path, item := range spec.Paths.Map() {
		require.Len(t, item.Operations(), 1, path)
		op := item.Get
		requests := int64(300)
		require.NotNil(t, op, path)
		limit, ok := op.Extensions["x-rate-limit"].(map[string]interface{})
		require.True(t, ok, path)
		require.Equal(t, "GCRA", limit["algorithm"], path)
		require.Equal(t, "per-ip", limit["scope"], path)
		if path == "/search" {
			requests = 60
		}
		require.EqualValues(t, requests, limit["requests"], path)
	}

	for _, forbidden := range []string{
		"/packages/lookup",
		"/usage/activity",
		"/search/clicks",
		"/feedback",
		"/catalog/update-feeds",
		"/catalog/snapshot",
		"/catalog/changes",
		"/config/client",
		"/desktop/releases/latest",
		"/site/settings",
		"/site/indexnow-keys/{key}",
		"/sitemap/packages",
	} {
		_, present := spec.Paths.Map()[forbidden]
		require.False(t, present, forbidden)
	}
}

func TestPublicExternalContractIsSubsetOfInternalContract(t *testing.T) {
	external, internal := loadPublicContracts(t)
	requirePublicExternalContractSubset(t, external, internal)
}

// The published contract states the terms of service and claims no licence, so a reader is not sent to a
// placeholder domain and is not told a licence the product has not chosen.
func TestPublishedContractsPointAtTermsAndClaimNoLicence(t *testing.T) {
	terms := "https://opennavo.com/developers#terms"
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	for _, name := range []string{"public.openapi.yaml", "public-external.openapi.yaml", "admin.openapi.yaml"} {
		spec, err := loader.LoadFromFile(name)
		require.NoError(t, err, name)
		require.Equal(t, terms, spec.Info.TermsOfService, name)
		require.Nil(t, spec.Info.License, name)
	}
}

func loadPublicContracts(t *testing.T) (*openapi3.T, *openapi3.T) {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	external, err := loader.LoadFromFile("public-external.openapi.yaml")
	require.NoError(t, err)
	internal, err := loader.LoadFromFile("public.openapi.yaml")
	require.NoError(t, err)

	return external, internal
}

type publicContractTestingT interface {
	require.TestingT
	Helper()
}

type publicSchemaUsage bool

const (
	publicRequestSchema  publicSchemaUsage = false
	publicResponseSchema publicSchemaUsage = true
)

func requirePublicExternalContractSubset(t publicContractTestingT, external, internal *openapi3.T) {
	t.Helper()
	for path, externalItem := range external.Paths.Map() {
		internalItem, ok := internal.Paths.Map()[path]
		require.True(t, ok, "public path %s is missing from the internal contract", path)
		for method, externalOperation := range externalItem.Operations() {
			internalOperation, ok := internalItem.Operations()[method]
			require.True(t, ok, "public operation %s %s is missing from the internal contract", method, path)
			operationPath := method + " " + path
			requestSeen := map[[2]*openapi3.Schema]bool{}
			internalParameters := publicOperationParameters(t, internalItem.Parameters, internalOperation.Parameters)
			externalParameters := publicOperationParameters(t, externalItem.Parameters, externalOperation.Parameters)
			for key, parameter := range internalParameters {
				if parameter.Required {
					_, present := externalParameters[key]
					require.True(t, present, "%s: required parameter %s is missing from the external contract", operationPath, key)
				}
			}
			for key, parameter := range externalParameters {
				internalParameter, ok := internalParameters[key]
				require.True(t, ok, "%s: parameter %s is missing from the internal contract", operationPath, key)
				parameterPath := operationPath + ".parameters." + key
				require.Equal(t, internalParameter.Name, parameter.Name, "%s: name", parameterPath)
				require.Equal(t, internalParameter.In, parameter.In, "%s: in", parameterPath)
				require.Equal(t, internalParameter.Required, parameter.Required, "%s: required", parameterPath)
				if parameter.Schema != nil || internalParameter.Schema != nil {
					requirePublicSchemaSubset(t, parameterPath+".schema", parameter.Schema, internalParameter.Schema, publicRequestSchema, requestSeen)
				}
				requirePublicContentSubset(t, parameterPath+".content", parameter.Content, internalParameter.Content, publicRequestSchema, requestSeen)
			}
			if externalOperation.RequestBody != nil {
				require.NotNil(t, internalOperation.RequestBody, "%s: requestBody", operationPath)
				require.NotNil(t, externalOperation.RequestBody.Value, "%s: resolved external requestBody", operationPath)
				require.NotNil(t, internalOperation.RequestBody.Value, "%s: resolved internal requestBody", operationPath)
				requirePublicContentSubset(t, operationPath+".requestBody", externalOperation.RequestBody.Value.Content, internalOperation.RequestBody.Value.Content, publicRequestSchema, requestSeen)
			}
			responseSeen := map[[2]*openapi3.Schema]bool{}
			for status, response := range externalOperation.Responses.Map() {
				internalResponse := internalOperation.Responses.Value(status)
				// The public contract documents rate-limit responses covered by the internal default response.
				if internalResponse == nil {
					internalResponse = internalOperation.Responses.Default()
				}
				responsePath := operationPath + ".responses." + status
				require.NotNil(t, internalResponse, "%s is missing from the internal contract", responsePath)
				require.NotNil(t, response.Value, "%s: resolved external response", responsePath)
				require.NotNil(t, internalResponse.Value, "%s: resolved internal response", responsePath)
				requirePublicContentSubset(t, responsePath, response.Value.Content, internalResponse.Value.Content, publicResponseSchema, responseSeen)
			}
		}
	}
	// Schemas are compared at their use sites, where the request/response direction is known.
	for name := range external.Components.Schemas {
		_, ok := internal.Components.Schemas[name]
		require.True(t, ok, "public schema %s is missing from the internal contract", name)
	}
}

func TestPublicRequestSchemaSubsetChecksRequiredAndEnumValues(t *testing.T) {
	internal := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Required: []string{"public", "internal"},
		Enum:     []any{"stable", "internal"},
	}}
	for name, schema := range map[string]*openapi3.Schema{
		"narrower":         {Required: []string{"public"}, Enum: []any{"stable"}},
		"omitted required": {Enum: []any{"stable"}},
		"omitted":          {},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "omitted" {
				failure := &publicContractFailure{}
				require.PanicsWithValue(t, failure, func() {
					requirePublicSchemaSubset(failure, name, &openapi3.SchemaRef{Value: schema}, internal, publicRequestSchema, map[[2]*openapi3.Schema]bool{})
				})
				require.Contains(t, failure.message, name+": enum")
				return
			}
			requirePublicSchemaSubset(t, name, &openapi3.SchemaRef{Value: schema}, internal, publicRequestSchema, map[[2]*openapi3.Schema]bool{})
		})
	}
}

func TestPublicExternalContractRejectsReversePatches(t *testing.T) {
	for _, test := range []struct {
		name     string
		patch    func(external, internal *openapi3.T)
		want     string
		response bool
	}{
		{
			name: "omitted request enum",
			patch: func(external, _ *openapi3.T) {
				external.Paths.Value("/rankings").Get.Parameters[1].Value.Schema.Value.Enum = nil
			},
			want: "GET /rankings.parameters.query:period.schema: enum",
		},
		{
			name: "response enum omits no",
			patch: func(external, _ *openapi3.T) {
				external.Components.Schemas["VersionCompatibility"].Value.Properties["appleSilicon"].Value.Enum = []any{"yes", "unknown"}
			},
			want:     ".properties.appleSilicon: enum",
			response: true,
		},
		{
			name: "response enum also used by a request omits no",
			patch: func(external, internal *openapi3.T) {
				for _, spec := range []*openapi3.T{external, internal} {
					item := spec.Paths.Value("/packages/{kind}/{token}")
					// Isolate this operation so another response cannot hide a shared-cache bug.
					spec.Paths = openapi3.NewPaths(openapi3.WithPath("/packages/{kind}/{token}", item))
					item.Get.Parameters = append(item.Get.Parameters,
						&openapi3.ParameterRef{Value: &openapi3.Parameter{
							Name: "compatibility", In: "query", Schema: spec.Components.Schemas["VersionCompatibility"],
						}},
					)
				}
				external.Components.Schemas["VersionCompatibility"].Value.Properties["appleSilicon"].Value.Enum = []any{"yes", "unknown"}
			},
			want:     ".properties.appleSilicon: enum",
			response: true,
		},
		{
			name: "changed minItems",
			patch: func(external, _ *openapi3.T) {
				external.Components.Schemas["CollectionSummary"].Value.Properties["previewItems"].Value.MinItems = 1
			},
			want: ".properties.previewItems: minItems",
		},
		{
			name: "changed maxItems",
			patch: func(external, _ *openapi3.T) {
				maxItems := uint64(4)
				external.Components.Schemas["CollectionSummary"].Value.Properties["previewItems"].Value.MaxItems = &maxItems
			},
			want: ".properties.previewItems: maxItems",
		},
		{
			name: "omitted maxItems",
			patch: func(external, _ *openapi3.T) {
				external.Components.Schemas["CollectionSummary"].Value.Properties["previewItems"].Value.MaxItems = nil
			},
			want: ".properties.previewItems: maxItems",
		},
		{
			name: "changed default",
			patch: func(external, _ *openapi3.T) {
				external.Components.Parameters["Size"].Value.Schema.Value.Default = float64(25)
			},
			want: ".parameters.query:size.schema: default",
		},
		{
			name: "omitted default",
			patch: func(external, _ *openapi3.T) {
				external.Components.Parameters["Size"].Value.Schema.Value.Default = nil
			},
			want: ".parameters.query:size.schema: default",
		},
		{
			name: "missing required query parameter",
			patch: func(external, _ *openapi3.T) {
				parameters := external.Paths.Value("/rankings").Get.Parameters
				external.Paths.Value("/rankings").Get.Parameters = parameters[1:]
			},
			want: "GET /rankings: required parameter query:kind is missing from the external contract",
		},
		{
			name: "missing required path parameter inherited internally",
			patch: func(external, internal *openapi3.T) {
				externalItem := external.Paths.Value("/packages/{kind}/{token}")
				externalItem.Get.Parameters = externalItem.Get.Parameters[1:]
				internalItem := internal.Paths.Value("/packages/{kind}/{token}")
				internalItem.Parameters = append(internalItem.Parameters, internalItem.Get.Parameters[0])
				internalItem.Get.Parameters = internalItem.Get.Parameters[1:]
			},
			want: "GET /packages/{kind}/{token}: required parameter path:kind is missing from the external contract",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			external, internal := loadPublicContracts(t)
			test.patch(external, internal)
			failure := &publicContractFailure{}
			require.PanicsWithValue(t, failure, func() {
				requirePublicExternalContractSubset(failure, external, internal)
			})
			require.Contains(t, failure.message, test.want)
			if test.response {
				require.Contains(t, failure.message, ".responses.")
			}
		})
	}
}

func TestPublicExternalContractAllowsCompatiblePatches(t *testing.T) {
	for _, test := range []struct {
		name  string
		patch func(external, internal *openapi3.T)
	}{
		{
			name: "request enum is a subset",
			patch: func(external, _ *openapi3.T) {
				external.Paths.Value("/rankings").Get.Parameters[1].Value.Schema.Value.Enum = []any{"30d"}
			},
		},
		{
			name: "request enum is unrestricted internally",
			patch: func(_, internal *openapi3.T) {
				internal.Paths.Value("/rankings").Get.Parameters[1].Value.Schema.Value.Enum = nil
			},
		},
		{
			name: "request enum is unrestricted in both contracts",
			patch: func(external, internal *openapi3.T) {
				for _, spec := range []*openapi3.T{external, internal} {
					spec.Paths.Value("/rankings").Get.Parameters[1].Value.Schema.Value.Enum = nil
				}
			},
		},
		{
			name: "response enum contains every internal value",
			patch: func(external, _ *openapi3.T) {
				schema := external.Components.Schemas["VersionCompatibility"].Value.Properties["appleSilicon"].Value
				schema.Enum = append(schema.Enum, "future")
			},
		},
		{
			name: "missing optional parameter",
			patch: func(external, _ *openapi3.T) {
				parameters := external.Paths.Value("/rankings").Get.Parameters
				external.Paths.Value("/rankings").Get.Parameters = parameters[:len(parameters)-1]
			},
		},
		{
			name: "external additionalProperties absent equals true",
			patch: func(external, _ *openapi3.T) {
				external.Components.Schemas["CaskDependsOn"].Value.Properties["requirements"].Value.AdditionalProperties.Has = nil
			},
		},
		{
			name: "internal additionalProperties absent equals true",
			patch: func(_, internal *openapi3.T) {
				internal.Components.Schemas["CaskDependsOn"].Value.Properties["requirements"].Value.AdditionalProperties.Has = nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			external, internal := loadPublicContracts(t)
			test.patch(external, internal)
			requirePublicExternalContractSubset(t, external, internal)
		})
	}
}

// Capture fatal assertions so reverse patches can prove the real checker rejects drift.
type publicContractFailure struct{ message string }

func (f *publicContractFailure) Helper() {}
func (f *publicContractFailure) Errorf(format string, args ...any) {
	f.message += fmt.Sprintf(format, args...)
}
func (f *publicContractFailure) FailNow() { panic(f) }

func publicOperationParameters(t publicContractTestingT, groups ...openapi3.Parameters) map[string]*openapi3.Parameter {
	t.Helper()
	parameters := map[string]*openapi3.Parameter{}
	// Operation parameters override path parameters with the same name and location.
	for _, group := range groups {
		for _, ref := range group {
			require.NotNil(t, ref)
			require.NotNil(t, ref.Value)
			parameters[ref.Value.In+":"+ref.Value.Name] = ref.Value
		}
	}
	return parameters
}

func requirePublicContentSubset(t publicContractTestingT, path string, external, internal openapi3.Content, usage publicSchemaUsage, seen map[[2]*openapi3.Schema]bool) {
	t.Helper()
	for mediaType, content := range external {
		internalContent, ok := internal[mediaType]
		require.True(t, ok, "%s: media type %s is missing from the internal contract", path, mediaType)
		require.NotNil(t, content, "%s: external content", path)
		require.NotNil(t, internalContent, "%s: internal content", path)
		if content.Schema != nil || internalContent.Schema != nil {
			requirePublicSchemaSubset(t, path+".content."+mediaType, content.Schema, internalContent.Schema, usage, seen)
		}
	}
}

func requirePublicSchemaSubset(t publicContractTestingT, path string, external, internal *openapi3.SchemaRef, usage publicSchemaUsage, seen map[[2]*openapi3.Schema]bool) {
	t.Helper()
	require.NotNil(t, external, "%s: external schema", path)
	require.NotNil(t, internal, "%s: internal schema", path)
	require.NotNil(t, external.Value, "%s: resolved external schema", path)
	require.NotNil(t, internal.Value, "%s: resolved internal schema", path)
	a, b := external.Value, internal.Value
	pair := [2]*openapi3.Schema{a, b}
	if seen[pair] {
		return
	}
	seen[pair] = true
	require.Equal(t, b.Type, a.Type, "%s: type", path)
	require.Equal(t, b.Nullable, a.Nullable, "%s: nullable", path)
	require.Equal(t, b.Format, a.Format, "%s: format", path)
	require.Equal(t, b.MinLength, a.MinLength, "%s: minLength", path)
	require.Equal(t, b.MaxLength, a.MaxLength, "%s: maxLength", path)
	require.Equal(t, b.MinItems, a.MinItems, "%s: minItems", path)
	require.Equal(t, b.MaxItems, a.MaxItems, "%s: maxItems", path)
	require.Equal(t, b.Default, a.Default, "%s: default", path)
	require.Equal(t, b.Pattern, a.Pattern, "%s: pattern", path)
	require.Equal(t, b.Min, a.Min, "%s: minimum", path)
	require.Equal(t, b.Max, a.Max, "%s: maximum", path)
	require.Equal(t, b.ExclusiveMin, a.ExclusiveMin, "%s: exclusiveMinimum", path)
	require.Equal(t, b.ExclusiveMax, a.ExclusiveMax, "%s: exclusiveMaximum", path)
	require.Equal(t, b.AdditionalProperties.Has == nil || *b.AdditionalProperties.Has, a.AdditionalProperties.Has == nil || *a.AdditionalProperties.Has, "%s: additionalProperties", path)
	require.Equal(t, b.AdditionalProperties.Schema != nil, a.AdditionalProperties.Schema != nil, "%s: additionalProperties schema", path)
	for _, required := range a.Required {
		require.Contains(t, b.Required, required, "%s: required", path)
	}
	if usage == publicResponseSchema {
		for _, value := range b.Enum {
			require.Contains(t, a.Enum, value, "%s: enum", path)
		}
	} else if len(b.Enum) > 0 {
		// An absent request enum is unrestricted, so external requests must
		// declare an enum whenever the internal contract restricts it.
		require.NotEmpty(t, a.Enum, "%s: enum", path)
		for _, value := range a.Enum {
			require.Contains(t, b.Enum, value, "%s: enum", path)
		}
	}
	for name, property := range a.Properties {
		internalProperty, ok := b.Properties[name]
		require.True(t, ok, "%s.properties.%s is missing from the internal contract", path, name)
		requirePublicSchemaSubset(t, path+".properties."+name, property, internalProperty, usage, seen)
	}
	for _, child := range []struct {
		name               string
		external, internal *openapi3.SchemaRef
	}{
		{"items", a.Items, b.Items},
		{"additionalProperties", a.AdditionalProperties.Schema, b.AdditionalProperties.Schema},
		{"not", a.Not, b.Not},
	} {
		if child.external != nil {
			requirePublicSchemaSubset(t, path+"."+child.name, child.external, child.internal, usage, seen)
		}
	}
	for _, group := range []struct {
		name               string
		external, internal openapi3.SchemaRefs
	}{
		{"allOf", a.AllOf, b.AllOf},
		{"anyOf", a.AnyOf, b.AnyOf},
		{"oneOf", a.OneOf, b.OneOf},
	} {
		require.Len(t, group.internal, len(group.external), "%s.%s", path, group.name)
		for index, schema := range group.external {
			requirePublicSchemaSubset(t, fmt.Sprintf("%s.%s[%d]", path, group.name, index), schema, group.internal[index], usage, seen)
		}
	}
}
