package httpserver_test

import (
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/stretchr/testify/require"
)

func TestCatalogV2DownloadSchemasAndExamples(t *testing.T) {
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	schemas := spec.Components.Schemas
	base := schemas["CatalogBaseSnapshot"].Value
	document := exampleObject(t, base.Example)
	require.NoError(t, base.VisitJSON(document))
	item := exampleObject(t, schemas["CatalogBaseItem"].Value.Example)
	require.NotContains(t, item, "displayName")
	require.NotContains(t, item, "summary")
	require.NotEmpty(t, item["sourceLocale"])
	for _, invalid := range []string{"displayName", "summary"} {
		bad := exampleObject(t, item)
		bad[invalid] = map[string]any{"ja-JP": "日本語"}
		require.Error(t, schemas["CatalogBaseItem"].Value.VisitJSON(bad))
	}
	delete(item, "sourceLocale")
	require.Error(t, schemas["CatalogBaseItem"].Value.VisitJSON(item))
	item = exampleObject(t, schemas["CatalogBaseItem"].Value.Example)
	item["kind"] = "formula"
	require.Error(t, schemas["CatalogBaseItem"].Value.VisitJSON(item))
	metadata := schemas["CatalogSnapshotInfo"].Value
	value := exampleObject(t, metadata.Example)
	require.NoError(t, metadata.VisitJSON(value))
	value["formatVersion"] = float64(1)
	require.Error(t, metadata.VisitJSON(value), "The v2 publishing contract rejects v1 metadata")
	value = exampleObject(t, metadata.Example)
	delete(value, "textPacks")
	require.Error(t, metadata.VisitJSON(value))
	value = exampleObject(t, metadata.Example)
	packs := exampleObject(t, value["textPacks"])
	for _, locale := range i18n.Locales {
		entry := exampleObject(t, packs[string(locale)])
		require.Equal(t, string(locale), entry["locale"])
		require.NotEmpty(t, entry["url"])
		require.Len(t, entry["sha256"], 64)
		require.Positive(t, entry["bytes"])
		require.Positive(t, entry["version"])
	}
	delete(packs, "ja-JP")
	value["textPacks"] = packs
	require.Error(t, metadata.VisitJSON(value))
	text := schemas["CatalogTextPack"].Value
	require.NoError(t, text.VisitJSON(text.Example))
	badText := exampleObject(t, text.Example)
	badText["locale"] = "unknown"
	require.Error(t, text.VisitJSON(badText))
	badInfo := exampleObject(t, schemas["CatalogTextPackInfo"].Value.Example)
	badInfo["sha256"] = "broken"
	require.Error(t, schemas["CatalogTextPackInfo"].Value.VisitJSON(badInfo))
	textItem := schemas["CatalogTextItem"].Value
	require.Error(t, textItem.VisitJSON(map[string]any{"kind": "cask", "token": "empty"}))
	require.Error(t, textItem.VisitJSON(map[string]any{"kind": "cask", "token": "empty", "displayName": ""}))
	localized := schemas["LocalizedNullableString"].Value
	require.NoError(t, localized.VisitJSON(map[string]any{"ja-JP": "日本語"}))
	require.NoError(t, localized.VisitJSON(map[string]any{}))
	require.NoError(t, localized.VisitJSON(map[string]any{"zh-CN": nil}), "Legacy HTTP nulls remain compatible; IPC normalizes them")
	for _, path := range []string{"/catalog/snapshot", "/catalog/changes"} {
		media := spec.Paths.Find(path).Get.Responses.Status(200).Value.Content["application/json"]
		require.Len(t, media.Examples, len(i18n.SiteLocales))
		for _, locale := range i18n.Locales {
			require.Contains(t, media.Examples, string(locale))
		}
		for name, example := range media.Examples {
			t.Run(path+"/"+name, func(t *testing.T) { require.NoError(t, media.Schema.Value.VisitJSON(example.Value.Value)) })
		}
	}
	// Generated models must represent single-language text packs and new metadata; existing tests still verify v1 fixtures byte for byte.
	raw, err := json.Marshal(schemas["CatalogTextPack"].Value.Example)
	require.NoError(t, err)
	var pack publicapi.CatalogTextPack
	require.NoError(t, json.Unmarshal(raw, &pack))
	require.Equal(t, publicapi.Locale("ja-JP"), pack.Locale)
}
