package httpserver_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestStaticContractExamplesConformAndHaveAbsoluteLinks(t *testing.T) {
	public, err := publicapi.GetSpec()
	require.NoError(t, err)
	admin, err := adminapi.GetSpec()
	require.NoError(t, err)
	for group, spec := range map[string]*openapi3.T{"public": public, "admin": admin} {
		for name, ref := range spec.Components.Schemas {
			t.Run(group+"/"+name, func(t *testing.T) {
				require.NotNil(t, ref.Value.Example, "Prism component must have a static example")
				require.NoError(t, ref.Value.VisitJSON(ref.Value.Example))
				checkExampleLinks(t, name, ref.Value.Example)
			})
		}
		require.NoError(t, spec.Validate(context.Background()))
		for name, locales := range map[string][]i18n.LocaleCode{"Locale": i18n.SiteLocales[:], "AppLocale": i18n.Locales[:]} {
			values := make([]any, 0, len(locales))
			for _, locale := range locales {
				values = append(values, string(locale))
			}
			require.ElementsMatch(t, values, spec.Components.Schemas[name].Value.Enum, group+"/"+name)
		}
	}
	data := exampleObject(t, public.Components.Schemas["HomeData"].Value.Example)
	for _, section := range []string{"popularApps", "popularCli", "recentlyUpdated"} {
		items, ok := data[section].([]any)
		require.True(t, ok)
		if section == "popularCli" {
			require.Empty(t, items)
			continue
		}
		require.GreaterOrEqual(t, len(items), 3)
		seen := map[string]bool{}
		for _, item := range items {
			p := exampleObject(t, item)
			token := p["token"].(string)
			require.False(t, seen[token], "home list must contain different packages")
			seen[token] = true
		}
	}
	detail := exampleObject(t, public.Components.Schemas["PackageDetail"].Value.Example)
	require.Equal(t, "visual-studio-code", detail["token"])
	require.Equal(t, "https://code.visualstudio.com/", detail["homepage"])
	require.Equal(t, "brew install --cask visual-studio-code", detail["installCommand"])
	user := exampleObject(t, admin.Components.Schemas["UserInfo"].Value.Example)
	require.Equal(t, "local-admin", user["userName"])
	require.Equal(t, []any{"R_SUPER"}, user["roles"])
	seed, err := seeds.Load()
	require.NoError(t, err)
	buttons := []any{}
	for _, permission := range seed.Permissions {
		require.NotEmpty(t, permission.Code, "retired permissions must be removed, not left as empty grants")
		buttons = append(buttons, permission.Code)
	}
	require.ElementsMatch(t, buttons, user["buttons"])
	for _, name := range []string{"AdminPackageDetail", "AdminPackageDetailResponse", "AdminPackageDetailResponseResult"} {
		object := exampleObject(t, admin.Components.Schemas[name].Value.Example)
		if value, wrapped := object["data"]; wrapped {
			object = exampleObject(t, value)
		}
		require.Equal(t, "https://opennavo.example/apps/visual-studio-code", object["webUrl"], name)
	}
	for _, name := range []string{"AdminCollection", "AdminCollectionResponse", "AdminCollectionResponseResult", "AdminCollectionPageResponse", "AdminCollectionPageResponseResult"} {
		object := exampleObject(t, admin.Components.Schemas[name].Value.Example)
		if value, wrapped := object["data"]; wrapped {
			object = exampleObject(t, value)
		}
		if value, paged := object["records"]; paged {
			records, ok := value.([]any)
			require.True(t, ok)
			require.NotEmpty(t, records)
			object = exampleObject(t, records[0])
		}
		require.Equal(t, "https://opennavo.example/collections/new-mac", object["webUrl"], name)
	}
}

func exampleObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	var object map[string]any
	require.NoError(t, json.Unmarshal(data, &object))
	return object
}
func checkExampleLinks(t *testing.T, path string, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			lower := strings.ToLower(key)
			link := lower == "url" || lower == "homepage" || strings.HasSuffix(lower, "url") || (strings.HasSuffix(lower, "domain") && lower != "domain") || strings.HasSuffix(lower, "gitremote") || strings.HasSuffix(path, ".links")
			if text, ok := child.(string); ok && lower == "domain" {
				u, err := url.Parse("https://" + text)
				require.NoError(t, err)
				require.Equal(t, text, u.Hostname(), path+".domain must be a hostname")
				require.Contains(t, text, ".")
				require.Empty(t, u.Path)
				require.Empty(t, u.RawQuery)
				require.Empty(t, u.Fragment)
				require.Nil(t, u.User)
			}
			if text, ok := child.(string); ok && link {
				u, err := url.Parse(text)
				require.NoError(t, err, "%s.%s", path, key)
				require.NotEmpty(t, u.Host, "%s.%s must be an absolute URL", path, key)
				require.Contains(t, []string{"https", "http"}, u.Scheme)
			}
			if key == "msg" {
				require.Equal(t, "ok", child)
			}
			checkExampleLinks(t, path+"."+key, child)
		}
	case []any:
		for _, child := range value {
			checkExampleLinks(t, path, child)
		}
	case string:
		require.NotEqual(t, "string", value, "%s contains a placeholder", path)
	}
}
