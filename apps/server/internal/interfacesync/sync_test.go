package interfacesync

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture() Plan {
	source := map[string]string{"a": "打开", "b": "关闭"}
	translations := map[string]map[string]string{}
	for _, code := range []string{"ja-JP", "es-ES", "pt-BR", "ru-RU"} {
		translations[code] = map[string]string{"a": "manual-a", "b": "manual-b"}
	}
	return Plan{Targets: map[string]Target{"server": {Source: source, English: map[string]string{"a": "Open", "b": "Close"}, Translations: translations, Hashes: map[string]string{"a": Hash(source["a"]), "b": Hash(source["b"])}}}}
}
func TestIncrementalAndManualPreservation(t *testing.T) {
	plan := fixture()
	require.Empty(t, Batches(plan))
	target := plan.Targets["server"]
	target.Source["a"] = "打开应用"
	plan.Targets["server"] = target
	batches := Batches(plan)
	require.Len(t, batches, 1)
	require.Equal(t, map[string]string{"a": "打开应用"}, batches[0].Source)
	calls := 0
	result, err := Run(context.Background(), plan, func(_ context.Context, b Batch) (map[string]map[string]string, error) {
		calls++
		return map[string]map[string]string{"ja-JP": {"a": "アプリを開きます"}, "es-ES": {"a": "Abre la aplicación"}, "pt-BR": {"a": "Abra o aplicativo"}, "ru-RU": {"a": "Открыть приложение"}}, nil
	}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	for _, fields := range result["server"] {
		require.Equal(t, "manual-b", fields["b"])
	}
	require.Equal(t, Hash("打开"), plan.Targets["server"].Hashes["a"], "Update the lock only after a successful writeback")
}
func TestValidationFailurePreservesSuccessfulKeys(t *testing.T) {
	for _, value := range []string{"Открыть {different}", "Открыть {count}"} {
		t.Run(value, func(t *testing.T) {
			plan := Plan{Targets: map[string]Target{"server": {Source: map[string]string{"a": "打开 {count}"}, English: map[string]string{"a": "Open {count} | Open {count}"}}}}
			result, err := Run(context.Background(), plan, func(_ context.Context, b Batch) (map[string]map[string]string, error) {
				values := map[string]map[string]string{"ja-JP": {"a": "開く {count}"}, "es-ES": {"a": "Abre la aplicación {count} | Abre la aplicación {count}"}, "pt-BR": {"a": "Abra o aplicativo {count} | Abra o aplicativo {count}"}, "ru-RU": {"a": value}}
				selected := map[string]map[string]string{}
				for _, code := range b.Locales {
					selected[code] = values[code]
				}
				return selected, nil
			}, nil, nil)
			require.Error(t, err)
			require.NotContains(t, result["server"]["ru-RU"], "a")
			for _, code := range []string{"ja-JP", "es-ES", "pt-BR"} {
				require.Contains(t, result["server"][code], "a")
			}
		})
	}
}
func TestSmallAndLargeBatches(t *testing.T) {
	plan := fixture()
	target := plan.Targets["server"]
	target.Translations["ja-JP"]["a"] = ""
	plan.Targets["server"] = target
	require.Len(t, Batches(plan), 1)
	for i := 0; i < 45; i++ {
		target.Source[fmt.Sprint(i)] = "新键"
	}
	require.Len(t, Batches(plan), 4)
}

func TestMissingLocaleDoesNotOverwriteOtherManualTranslations(t *testing.T) {
	plan := fixture()
	target := plan.Targets["server"]
	delete(target.Translations["ja-JP"], "a")
	plan.Targets["server"] = target
	batches := Batches(plan)
	require.Len(t, batches, 1)
	require.Equal(t, []string{"ja-JP"}, batches[0].Locales)
	result, err := Run(context.Background(), plan, func(context.Context, Batch) (map[string]map[string]string, error) {
		return map[string]map[string]string{"ja-JP": {"a": "開きます"}}, nil
	}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "manual-a", result["server"]["ru-RU"]["a"])
}

func TestPluralReferenceChangesPlan(t *testing.T) {
	plan := fixture()
	target := plan.Targets["server"]
	target.English["a"] = "One update | Updates"
	plan.Targets["server"] = target
	batches := Batches(plan)
	require.Len(t, batches, 3) // Japanese remains other and is not retranslated.
	for _, b := range batches {
		require.Len(t, b.Source, 1)
		require.Contains(t, b.Source, "a")
		require.Equal(t, "Plural form count mismatch", b.Reasons[b.Locales[0]]["a"])
	}
	target.Translations["es-ES"]["a"] = "Actualización | Actualizaciones"
	target.Translations["pt-BR"]["a"] = "Atualização | Atualizações"
	target.Translations["ru-RU"]["a"] = "Обновление | Обновления | Обновлений | Обновления"
	require.Empty(t, Batches(plan))
	target.English["a"] = "Updates"
	batches = Batches(plan)
	require.Len(t, batches, 3)
	for _, b := range batches {
		require.Equal(t, "Plural form count mismatch", b.Reasons[b.Locales[0]]["a"])
	}
}

func TestPartialFailureRetryAndResume(t *testing.T) {
	plan := fixture()
	target := plan.Targets["server"]
	delete(target.Translations["ja-JP"], "a")
	delete(target.Translations["es-ES"], "a")
	calls := 0
	result, err := Run(context.Background(), plan, func(_ context.Context, b Batch) (map[string]map[string]string, error) {
		calls++
		if b.Locales[0] == "ja-JP" {
			return map[string]map[string]string{"ja-JP": {"a": "開きます"}}, nil
		}
		if calls == 3 {
			require.Contains(t, b.RetryReason, "changed literal")
		}
		return map[string]map[string]string{"es-ES": {"a": "Abrir {bad}"}}, nil
	}, nil, nil)
	require.ErrorContains(t, err, "server/es-ES/a")
	require.Equal(t, 3, calls)
	require.Equal(t, "開きます", result["server"]["ja-JP"]["a"])
	require.NotContains(t, result["server"]["es-ES"], "a")
	target.Translations = result["server"]
	plan.Targets["server"] = target
	batches := Batches(plan)
	require.Len(t, batches, 1)
	require.Equal(t, []string{"es-ES"}, batches[0].Locales)
}

func TestValidationDiagnostics(t *testing.T) {
	b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"path": "src/theme/settings.ts 中的变量"}, English: map[string]string{"path": "Variable in src/theme/settings.ts"}}
	require.NoError(t, Validate(b, map[string]map[string]string{"ja-JP": {"path": "src/theme/settings.tsの変数"}}, nil, nil))
	err := Validate(b, map[string]map[string]string{"ja-JP": {"path": "src/theme/changed.tsの変数"}}, nil, nil)
	require.ErrorContains(t, err, "changed literal: admin/ja-JP/path")
	require.ErrorContains(t, err, `source=["/theme/settings.ts"] translation=["/theme/changed.ts"]`)
	require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {}}, nil, nil), "missing key: admin/ja-JP/path")
	b.Source = map[string]string{"label": "打开"}
	b.English = map[string]string{"label": "Open"}
	require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {"label": "This English sentence is not Japanese"}}, nil, nil), `wrong language: admin/ja-JP keys=[label] text=`)
}

func TestValidationRetryCanRecover(t *testing.T) {
	plan := fixture()
	delete(plan.Targets["server"].Translations["ja-JP"], "a")
	calls := 0
	result, err := Run(context.Background(), plan, func(_ context.Context, b Batch) (map[string]map[string]string, error) {
		calls++
		if calls == 1 {
			return nil, Invalid("invalid schema: server/ja-JP/a")
		}
		require.Contains(t, b.RetryReason, "invalid schema")
		return map[string]map[string]string{"ja-JP": {"a": "開きます"}}, nil
	}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "開きます", result["server"]["ja-JP"]["a"])
}

func TestCompletedLocaleSurvivesSourceHashChange(t *testing.T) {
	plan := fixture()
	target := plan.Targets["server"]
	target.Source["a"] = "新的源文"
	target.Translations["ja-JP"]["a"] = "新しい文章"
	target.LocaleHashes = map[string]map[string]string{"ja-JP": {"a": Hash(target.Source["a"])}}
	plan.Targets["server"] = target
	batches := Batches(plan)
	require.Len(t, batches, 3)
	for _, b := range batches {
		require.NotContains(t, b.Locales, "ja-JP")
		require.Equal(t, map[string]string{"a": "新的源文"}, b.Source)
	}
}

func TestStopErrorDoesNotRetryOrCallRemainingBatches(t *testing.T) {
	plan := fixture()
	delete(plan.Targets["server"].Translations["ja-JP"], "a")
	delete(plan.Targets["server"].Translations["es-ES"], "a")
	calls := 0
	result, err := Run(context.Background(), plan, func(context.Context, Batch) (map[string]map[string]string, error) {
		calls++
		return nil, &StopError{Reason: "gateway quota rejected"}
	}, nil, nil)
	require.ErrorContains(t, err, "server/ja-JP/a")
	require.ErrorContains(t, err, "server/es-ES/a")
	require.Equal(t, 1, calls)
	require.Equal(t, "manual-b", result["server"]["ja-JP"]["b"])
}

func TestTechnicalTermsAreNotProtectedBrands(t *testing.T) {
	for _, pair := range [][2]string{{"token", "Token"}, {"token", "トークン"}, {"App", "アプリ"}, {"请输入 token、名称或描述", "Token、名前、説明を入力してください"}} {
		b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"key": pair[0]}, English: map[string]string{"key": "Token, name or description"}}
		require.NoError(t, Validate(b, map[string]map[string]string{"ja-JP": {"key": pair[1]}}, DefaultBrands, nil))
	}
	require.Zero(t, countTerm("Enterprise", "Enter"))
	require.Equal(t, 1, countTerm("OpenNavoを開きます", "OpenNavo"))
	require.Zero(t, countTerm("MyOpenNavoHelper", "OpenNavo"))
	for _, value := range []string{"アプリを開きます", "オープンナボを開きます", "opennavo を開きます"} {
		b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"key": "打开 OpenNavo"}, English: map[string]string{"key": "Open OpenNavo"}}
		require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {"key": value}}, DefaultBrands, nil), "changed brand")
	}
	b := Batch{Target: "admin", Locales: []string{"ja-JP"}, Source: map[string]string{"key": "使用 CustomProduct"}, English: map[string]string{"key": "Use CustomProduct"}}
	require.ErrorContains(t, Validate(b, map[string]map[string]string{"ja-JP": {"key": "製品を使います"}}, append(append([]string{}, DefaultBrands...), "CustomProduct"), nil), "changed brand")
	// Even if Enter is explicitly protected, do not count the substring in Enterprise.
	b.Source["key"] = "Enterprise"
	b.English["key"] = "Enterprise"
	require.NoError(t, Validate(b, map[string]map[string]string{"ja-JP": {"key": "企業"}}, []string{"Enter"}, nil))
}
