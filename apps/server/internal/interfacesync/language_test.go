package interfacesync

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func languageBatch(code string, values map[string]string) Batch {
	b := Batch{Target: "admin", Locales: []string{code}, Source: map[string]string{}, English: map[string]string{}}
	for key := range values {
		b.Source[key] = "设置"
		b.English[key] = "Settings"
	}
	return b
}
func TestShortLabelsAndLetterlessTemplates(t *testing.T) {
	for code, values := range map[string]map[string]string{
		"es-ES": {"a": "Orden", "b": "Estado", "c": "Objetivo", "d": "Tipo de objetivo", "e": "Cobertura", "f": "Icono"},
		"pt-BR": {"a": "Nome", "b": "Sim"},
	} {
		require.NoError(t, ValidateEach(languageBatch(code, values), map[string]map[string]string{code: values}, DefaultBrands, nil))
	}
	for _, code := range []string{"es-ES", "pt-BR", "ru-RU", "ja-JP", "zh-CN", "en-US"} {
		values := map[string]string{"count": "{count}/{max}", "by": "{a} · {b}"}
		b := languageBatch(code, values)
		b.Source = values
		b.English = values
		require.NoError(t, ValidateEach(b, map[string]map[string]string{code: values}, DefaultBrands, nil))
	}
}
func TestLongWrongLanguageAndShortScripts(t *testing.T) {
	for _, test := range []struct{ code, text string }{
		{"pt-BR", "La aplicación permite configurar todas las opciones disponibles para gestionar los archivos del sistema y mantener actualizada la información de los usuarios."},
		{"es-ES", "This application allows administrators to configure all available settings and manage the information displayed to every user of the system."},
		{"pt-BR", "This application allows administrators to configure all available settings and manage the information displayed to every user of the system."},
		{"ja-JP", "Nombre"}, {"ru-RU", "Nombre"}, {"zh-CN", "Nombre"},
	} {
		values := map[string]string{"key": test.text}
		require.ErrorContains(t, ValidateEach(languageBatch(test.code, values), map[string]map[string]string{test.code: values}, DefaultBrands, nil), "wrong language", "%s: %s", test.code, test.text)
	}
}
func TestShortGroupBisectsAndPreservesGoodKeys(t *testing.T) {
	values := map[string]string{}
	for i := 0; i < 4; i++ {
		values[fmt.Sprintf("a%02d", i)] = "Sí"
	}
	for i := 0; i < 12; i++ {
		values[fmt.Sprintf("b%02d", i)] = "The settings are available"
	}
	failures := languageFailures(languageBatch("es-ES", values), "es-ES", values, DefaultBrands, nil)

	require.NotEmpty(t, failures)
	for i := 0; i < 4; i++ {
		require.NotContains(t, failures, fmt.Sprintf("a%02d", i))
	}
}
func TestRepositoryLanguageReplay(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	command := exec.Command("node", "--input-type=module", "-e", "import {exportPlan,targetNames} from './tooling/scripts/interface-i18n.mjs'; process.stdout.write(JSON.stringify(exportPlan(targetNames)))")
	command.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	data, err := command.Output()
	require.NoError(t, err)
	var plan Plan
	require.NoError(t, json.Unmarshal(data, &plan))
	count := 0
	for name, target := range plan.Targets {
		for _, code := range TargetLocales(name) {
			fields := target.Translations[code]
			if code == "zh-CN" {
				fields = target.Source
			}
			if code == "en-US" {
				fields = target.English
			}
			keys := sortedKeys(fields)
			for start := 0; start < len(keys); start += 100 {
				b := Batch{Target: name, Locales: []string{code}, Source: map[string]string{}, English: map[string]string{}}
				selected := map[string]string{}
				for _, key := range keys[start:min(start+100, len(keys))] {
					b.Source[key] = target.Source[key]
					b.English[key] = target.English[key]
					selected[key] = fields[key]
					count++
				}
				require.NoError(t, ReplayLanguage(b, map[string]map[string]string{code: selected}, DefaultBrands), "%s/%s", name, code)
			}
		}
	}
	require.Greater(t, count, 10000)
}

func TestShortLabelsPersistAndResumeWithoutRequests(t *testing.T) {
	values := map[string]string{"a": "Nome", "b": "Sim"}
	b := languageBatch("pt-BR", values)
	target := Target{Source: b.Source, English: b.English, Translations: map[string]map[string]string{}}
	for _, code := range []string{"ja-JP", "es-ES", "ru-RU"} {
		target.Translations[code] = map[string]string{"a": "手动", "b": "手动"}
	}
	target.Hashes = map[string]string{"a": Hash(b.Source["a"]), "b": Hash(b.Source["b"])}
	plan := Plan{Targets: map[string]Target{"admin": target}}
	cache := ResumeCache{Directory: t.TempDir()}
	calls := 0
	result, err := Run(context.Background(), plan, func(context.Context, Batch) (map[string]map[string]string, error) {
		calls++
		return map[string]map[string]string{"pt-BR": values}, nil
	}, DefaultBrands, nil, cache.Save)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, values, result["admin"]["pt-BR"])
	restored, _, err := cache.Restore(plan, DefaultBrands, nil)
	require.NoError(t, err)
	require.Empty(t, Batches(restored))
}
