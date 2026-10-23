package api_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/stretchr/testify/require"
)

func localeCodes(t *testing.T) []string {
	return surfaceLocaleCodes(t, "site")
}

func surfaceLocaleCodes(t *testing.T, surface string) []string {
	t.Helper()
	data, err := os.ReadFile("../../../packages/shared/src/locales.json")
	require.NoError(t, err)
	var manifest struct {
		DefaultLocale string `json:"defaultLocale"`
		Locales       []struct {
			Code     string   `json:"code"`
			Surfaces []string `json:"surfaces"`
		} `json:"locales"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, "en-US", manifest.DefaultLocale)
	codes := []string{}
	for _, locale := range manifest.Locales {
		if slices.Contains(locale.Surfaces, surface) {
			codes = append(codes, locale.Code)
		}
	}
	return codes
}

func loadContract(t *testing.T, name string) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	spec, err := loader.LoadFromFile(name + ".openapi.yaml")
	require.NoError(t, err)
	require.NoError(t, spec.Validate(context.Background()))
	return spec
}

func TestLocaleManifestGeneratedConstantsAndBothContractsAgree(t *testing.T) {
	codes := localeCodes(t)
	generated := make([]string, len(i18n.SiteLocales))
	for index, locale := range i18n.SiteLocales {
		generated[index] = string(locale)
	}
	require.Equal(t, codes, generated)
	for _, name := range []string{"public", "admin"} {
		spec := loadContract(t, name)
		enum := spec.Components.Schemas["Locale"].Value.Enum
		actual := make([]string, len(enum))
		for index, value := range enum {
			actual[index] = value.(string)
		}
		require.Equal(t, codes, actual, name)
		// AppLocale is the Mac client's language set (client release notes): the desktop surface.
		app := spec.Components.Schemas["AppLocale"].Value.Enum
		require.Len(t, app, len(i18n.DesktopLocales))
		for index, locale := range i18n.DesktopLocales {
			require.Equal(t, string(locale), app[index])
		}
		require.Equal(t, surfaceLocaleCodes(t, "desktop"), func() []string {
			out := []string{}
			for _, locale := range i18n.DesktopLocales {
				out = append(out, string(locale))
			}
			return out
		}())
	}
}

func TestI18nContractStaticExamplesConformIncludingEveryLocale(t *testing.T) {
	codes := localeCodes(t)
	for _, name := range []string{"public", "admin"} {
		spec := loadContract(t, name)
		for schemaName, ref := range spec.Components.Schemas {
			t.Run(name+"/schema/"+schemaName, func(t *testing.T) {
				require.NotNil(t, ref.Value.Example)
				require.NoError(t, ref.Value.VisitJSON(ref.Value.Example))
			})
		}
		for path, item := range spec.Paths.Map() {
			for method, op := range item.Operations() {
				t.Run(name+"/"+method+path, func(t *testing.T) {
					if op.RequestBody != nil {
						for _, media := range op.RequestBody.Value.Content {
							if media.Example != nil {
								require.NoError(t, media.Schema.Value.VisitJSON(media.Example))
							}
						}
					}
					response := op.Responses.Status(200)
					if response == nil {
						return
					}
					media := response.Value.Content.Get("application/json")
					if media == nil {
						return
					}
					for _, locale := range codes {
						example := media.Examples[locale]
						require.NotNil(t, example, locale)
						require.NoError(t, media.Schema.Value.VisitJSON(example.Value.Value), locale)
					}
				})
			}
		}
	}
}

func TestAdminWritesAcceptOnlySourceLanguageAndRejectUnknownLocale(t *testing.T) {
	spec := loadContract(t, "admin")
	for _, name := range []string{"CategoryUpsert", "CollectionUpsert", "FeatureUpsert", "MirrorUpsert", "ScreenshotCreate", "ScreenshotUpdate", "DesktopReleaseCreate", "DesktopReleaseUpdate"} {
		schema := spec.Components.Schemas[name].Value
		data, err := json.Marshal(schema.Example)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(data, &body))
		text := body["i18n"].(map[string]any)["zh-CN"]
		codes := localeCodes(t)
		if strings.HasPrefix(name, "DesktopRelease") {
			codes = surfaceLocaleCodes(t, "app")
		}
		for _, locale := range codes {
			body["sourceLocale"] = locale
			body["i18n"] = map[string]any{locale: text}
			require.NoError(t, schema.VisitJSON(body), name+"/"+locale)
		}
		body["sourceLocale"] = "unknown"
		body["i18n"] = map[string]any{"unknown": text}
		require.Error(t, schema.VisitJSON(body), name)
	}
	require.True(t, spec.Paths.Value("/translations/approve").Post.Deprecated)
	require.Contains(t, spec.Components.Schemas["ReleaseSource"].Value.Enum, "editorial")
}
