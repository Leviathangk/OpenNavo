package interfacesync

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/llm"
	"github.com/stretchr/testify/require"
)

func TestEnglishReferenceBrandBounds(t *testing.T) {
	b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"k": "最低系统"}, English: map[string]string{"k": "Min macOS"}}
	require.NoError(t, Validate(b, map[string]map[string]string{"ja-JP": {"k": "最低 macOS"}}, DefaultBrands, nil))
	require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {"k": "macOS macOS の最低システム"}}, DefaultBrands, nil), "changed brand")
	b.Source["k"] = "OpenNavo 系统"
	b.English["k"] = "OpenNavo system"
	require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {"k": "システム"}}, DefaultBrands, nil), "changed brand")
}
func TestUILabelIsNotCommand(t *testing.T) {
	b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"k": "brew Git 仓库"}, English: map[string]string{"k": "brew Git repository"}}
	require.NoError(t, Validate(b, map[string]map[string]string{"ja-JP": {"k": "brew Git リポジトリ"}}, nil, nil))
	require.Equal(t, []string{"brew Git 仓库"}, llm.LiteralSpans(b.Source["k"]), "The content engine still protects whole-line commands")
	b.Source["k"] = "运行 `brew update`"
	require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {"k": "`brew upgrade`を実行"}}, nil, nil), "changed literal")
}
func TestCurrencyIsNotTemplateLiteral(t *testing.T) {
	b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"k": "约 ${spend} / ${budget}"}, English: map[string]string{"k": "About ${spend} of ${budget}"}}
	require.NoError(t, Validate(b, map[string]map[string]string{"ja-JP": {"k": "約 {spend} / {budget}"}}, nil, nil))
	require.Equal(t, []string{"${budget}", "${spend}"}, llm.LiteralSpans(b.Source["k"]))
	require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {"k": "約 {other} / {budget}"}}, nil, nil), "changed literal")
}
func TestStructuredPluralSchemaAndDecode(t *testing.T) {
	b := Batch{Target: "admin", Locales: []string{"ja-JP", "es-ES", "pt-BR", "ru-RU"}, Source: map[string]string{"count": "{count} 个更新", "label": "打开"}, English: map[string]string{"count": "{count} update | {count} updates", "label": "Open"}}
	schema, counts := Schema(b)
	for code, n := range map[string]int{"ja-JP": 1, "es-ES": 2, "pt-BR": 2, "ru-RU": 4} {
		require.Equal(t, n, counts[code]["count"])
		fields := schema["properties"].(map[string]any)[code].(map[string]any)["properties"].(map[string]any)
		require.Equal(t, map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": n, "maxItems": n}, fields["count"])
		require.Equal(t, map[string]any{"type": "string"}, fields["label"])
	}
	b.Locales = []string{"es-ES"}
	output, err := DecodeOutput(b, []byte(`{"es-ES":{"count":["{count} actualización","{count} actualizaciones"],"label":"Abrir"}}`))
	require.NoError(t, err)
	require.Equal(t, "{count} actualización | {count} actualizaciones", output["es-ES"]["count"])
	for _, raw := range []string{`{"es-ES":{"count":["una"],"label":"Abrir"}}`, `{"es-ES":{"count":"una | otras","label":"Abrir"}}`} {
		output, err = DecodeOutput(b, []byte(raw))
		require.ErrorContains(t, err, "invalid plural")
		require.Equal(t, "Abrir", output["es-ES"]["label"])
	}
}
func TestPerKeyRetryAndInterruptResume(t *testing.T) {
	plan := fixture()
	delete(plan.Targets["server"].Translations["ja-JP"], "a")
	delete(plan.Targets["server"].Translations["ja-JP"], "b")
	cache := ResumeCache{Directory: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := Run(ctx, plan, func(_ context.Context, b Batch) (map[string]map[string]string, error) {
		calls++
		if calls == 2 {
			require.Equal(t, map[string]string{"b": "关闭"}, b.Source)
			require.Contains(t, b.RetryReason, "changed literal")
			files, readErr := cache.read("server")
			require.NoError(t, readErr)
			require.Equal(t, "開きます", files.Entries["ja-JP"]["a"].Translation)
			cancel()
			return nil, context.Canceled
		}
		return map[string]map[string]string{"ja-JP": {"a": "開きます", "b": "閉じます {bad}"}}, nil
	}, nil, nil, cache.Save)
	require.ErrorContains(t, err, "server/ja-JP/b")
	require.NotContains(t, err.Error(), "server/ja-JP/a")
	restored, _, err := cache.Restore(plan, nil, nil)
	require.NoError(t, err)
	calls = 0
	result, err := Run(context.Background(), restored, func(_ context.Context, b Batch) (map[string]map[string]string, error) {
		calls++
		require.Equal(t, map[string]string{"b": "关闭"}, b.Source)
		return map[string]map[string]string{"ja-JP": {"b": "閉じます"}}, nil
	}, nil, nil, cache.Save)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "開きます", result["server"]["ja-JP"]["a"])
	// A bad cache entry in a historical batch does not invalidate successful keys.
	require.NoError(t, cache.Save(Batch{Target: "server", Locales: []string{"ja-JP"}, Source: plan.Targets["server"].Source, English: plan.Targets["server"].English}, map[string]map[string]string{"ja-JP": {"a": "開きます", "b": "閉じます {bad}"}}))
	restored, _, err = cache.Restore(plan, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "開きます", restored.Targets["server"].Translations["ja-JP"]["a"])
	require.Empty(t, restored.Targets["server"].Translations["ja-JP"]["b"])
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)
}

func TestPerKeyValidationStillRejectsUnexpectedSchema(t *testing.T) {
	b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"label": "打开"}, English: map[string]string{"label": "Open"}}
	for _, raw := range []string{`{"ja-JP":{"label":"開きます","extra":"閉じます"}}`, `{"ja-JP":{"label":"開きます"},"es-ES":{"label":"Abrir"}}`} {
		output, err := DecodeOutput(b, []byte(raw))
		require.ErrorContains(t, err, "invalid schema")
		require.Nil(t, output)
	}
	require.ErrorContains(t, ValidateEach(b, map[string]map[string]string{"ja-JP": {"label": "開きます", "extra": "閉じます"}}, nil, nil), "invalid schema")
}
