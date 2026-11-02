package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/interfacesync"
	"github.com/stretchr/testify/require"
)

func TestGatewayDiagnosticsAndValidationRetry(t *testing.T) {
	for _, status := range []int{200, 400, 401, 402, 403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Chdir(t.TempDir())
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				if calls == 2 {
					require.Contains(t, body.Messages[0].Content, "changed literal: admin/ja-JP/path")
					require.Contains(t, body.Messages[0].Content, "Previous attempt failed validation")
				}
				w.Header().Set("Content-Type", "application/json")
				if status != 200 {
					w.WriteHeader(status)
					_, err := w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"private response body"}}`))
					require.NoError(t, err)
					return
				}
				translated := `{"ja-JP":{"path":"src/theme/changed.tsの変数"}}`
				if calls == 2 {
					translated = `{"ja-JP":{"path":"src/theme/settings.tsの変数"}}`
				}
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "unit", "object": "chat.completion", "model": "gpt-6-luna", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": translated}}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}}))
			}))
			defer server.Close()
			t.Setenv("LLM_BASE_URL", server.URL)
			t.Setenv("LLM_API_KEY", "unit-test-credential")
			t.Setenv("DATABASE_URL", "")
			source := "src/theme/settings.ts 中的变量"
			plan := interfacesync.Plan{Targets: map[string]interfacesync.Target{"admin": {Source: map[string]string{"path": source}, English: map[string]string{"path": "Variable in src/theme/settings.ts"}, Hashes: map[string]string{"path": interfacesync.Hash(source)}, Translations: map[string]map[string]string{"es-ES": {"path": "manual"}, "pt-BR": {"path": "manual"}, "ru-RU": {"path": "manual"}}}}}
			encoded, err := json.Marshal(plan)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile("input.json", encoded, 0600))
			previousFlags, previousArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine = previousFlags; os.Args = previousArgs })
			flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
			os.Args = []string{"test", "--input", "input.json", "--output", "output.json"}
			err = run()
			ledger, readErr := os.ReadFile("tmp/i18n-sync/calls.jsonl")
			require.NoError(t, readErr)
			require.NotContains(t, string(ledger), "unit-test-credential")
			require.NotContains(t, string(ledger), "private response body")
			require.NotContains(t, string(ledger), "Translate Simplified")
			lines := strings.Split(strings.TrimSpace(string(ledger)), "\n")
			var first call
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
			if status == 200 {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
				require.Len(t, lines, 2)
				require.Equal(t, "validation_failed", first.Status)
				require.Contains(t, first.Reason, "changed literal: admin/ja-JP/path")
				require.Contains(t, first.Reason, `source=["/theme/settings.ts"] translation=["/theme/changed.ts"]`)
				var second call
				require.NoError(t, json.Unmarshal([]byte(lines[1]), &second))
				require.Equal(t, 2, second.Attempt)
				require.Equal(t, "ok", second.Status)
				require.EqualValues(t, 11, second.Input)
			} else {
				require.ErrorContains(t, err, "failed keys: admin/ja-JP/path")
				require.Equal(t, 1, calls)
				require.Equal(t, status, first.HTTPStatus)
				require.Equal(t, "invalid_request_error", first.ErrorType)
			}
			resultBytes, readErr := os.ReadFile("output.json")
			require.NoError(t, readErr)
			var result interfacesync.Result
			require.NoError(t, json.Unmarshal(resultBytes, &result))
			require.Equal(t, "manual", result["admin"]["es-ES"]["path"])
		})
	}
}

func TestResumeProcessHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("INTERFACE_SYNC_RESUME_HELPER") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("resume-helper", flag.ContinueOnError)
	os.Args = []string{"resume-helper", "--input", "input.json", "--output", "output.json"}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func TestInterruptPersistsCompletedBatchBeforeProcessExit(t *testing.T) {
	t.Chdir(t.TempDir())
	var japanese, spanish atomic.Int32
	secondStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		var input struct {
			Languages map[string]any `json:"languages"`
		}
		require.NoError(t, json.Unmarshal([]byte(request.Messages[1].Content), &input))
		translated := `{"ja-JP":{"a":"開きます"}}`
		if _, ok := input.Languages["ja-JP"]; ok {
			japanese.Add(1)
		} else {
			require.Contains(t, input.Languages, "es-ES")
			if spanish.Add(1) == 1 {
				close(secondStarted)
				<-r.Context().Done()
				return
			}
			translated = `{"es-ES":{"a":"Abre la aplicación"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "unit", "object": "chat.completion", "model": "gpt-6-luna", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": translated}}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}}))
	}))
	defer server.Close()
	t.Setenv("INTERFACE_SYNC_RESUME_HELPER", "1")
	t.Setenv("LLM_BASE_URL", server.URL)
	t.Setenv("LLM_API_KEY", "unit-test-credential")
	t.Setenv("DATABASE_URL", "")
	plan := interfacesync.Plan{Targets: map[string]interfacesync.Target{"admin": {Source: map[string]string{"a": "打开"}, English: map[string]string{"a": "Open"}, Hashes: map[string]string{"a": interfacesync.Hash("打开")}, Translations: map[string]map[string]string{"pt-BR": {"a": "manual"}, "ru-RU": {"a": "manual"}}}}}
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("input.json", raw, 0600))
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, executable, "-test.run=^TestResumeProcessHelper$") // #nosec G204 -- Execute only the current test binary returned by os.Executable, with fixed arguments.
	var output bytes.Buffer
	process.Stdout = &output
	process.Stderr = &output
	require.NoError(t, process.Start())
	t.Cleanup(func() { _ = process.Process.Kill() })
	select {
	case <-secondStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("second batch did not start")
	}
	cacheFiles, err := filepath.Glob("tmp/i18n-sync/resume/*.json")
	require.NoError(t, err)
	require.Len(t, cacheFiles, 1, "The first batch must be persisted before the second request")
	require.NoError(t, process.Process.Signal(os.Interrupt))
	require.Error(t, process.Wait())
	// Do not read output.json; start a separate process directly from the original input.
	retry := exec.CommandContext(ctx, executable, "-test.run=^TestResumeProcessHelper$") // #nosec G204 -- The same controlled test binary verifies recovery after a real interruption.
	retryOutput, err := retry.CombinedOutput()
	require.NoError(t, err, string(retryOutput))
	require.EqualValues(t, 1, japanese.Load(), "Completed Japanese batches must not be billed again")
	require.EqualValues(t, 2, spanish.Load(), "Retry only the Spanish batch unfinished at interruption")
	raw, err = os.ReadFile("output.json")
	require.NoError(t, err)
	var result interfacesync.Result
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "開きます", result["admin"]["ja-JP"]["a"])
	require.Equal(t, "Abre la aplicación", result["admin"]["es-ES"]["a"])
}

func TestGatewayRetriesOnlyFailedKeyWithStructuredPlural(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat struct {
				JSONSchema struct {
					Schema map[string]any `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		var payload struct {
			Source        map[string]string         `json:"source"`
			ExpectedForms map[string]map[string]int `json:"expectedForms"`
		}
		require.NoError(t, json.Unmarshal([]byte(request.Messages[1].Content), &payload))
		require.Equal(t, 1, payload.ExpectedForms["ja-JP"]["count"])
		fields := request.ResponseFormat.JSONSchema.Schema["properties"].(map[string]any)["ja-JP"].(map[string]any)["properties"].(map[string]any)
		require.Equal(t, "array", fields["count"].(map[string]any)["type"])
		require.EqualValues(t, 1, fields["count"].(map[string]any)["minItems"])
		require.EqualValues(t, 1, fields["count"].(map[string]any)["maxItems"])
		translated := `{"ja-JP":{"label":"開きます","count":["{bad} 件の更新"]}}`
		if calls == 2 {
			require.Equal(t, map[string]string{"count": "{count} 个更新"}, payload.Source)
			require.NotContains(t, request.Messages[0].Content, "admin/ja-JP/label")
			require.Contains(t, request.Messages[0].Content, "changed literal: admin/ja-JP/count")
			cached, err := os.ReadFile(filepath.Join("tmp/i18n-sync/resume", interfacesync.Hash("admin")+".json"))
			require.NoError(t, err)
			require.Contains(t, string(cached), "開きます")
			translated = `{"ja-JP":{"count":["{count} 件の更新"]}}`
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "unit", "object": "chat.completion", "model": "gpt-6-luna", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": translated}}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}}))
	}))
	defer gateway.Close()
	t.Setenv("LLM_BASE_URL", gateway.URL)
	t.Setenv("LLM_API_KEY", "unit-test-credential")
	t.Setenv("DATABASE_URL", "")
	source := map[string]string{"label": "打开", "count": "{count} 个更新"}
	en := map[string]string{"label": "Open", "count": "{count} update | {count} updates"}
	translations := map[string]map[string]string{}
	for _, code := range []string{"es-ES", "pt-BR", "ru-RU"} {
		count := 2
		if code == "ru-RU" {
			count = 4
		}
		forms := make([]string, count)
		for i := range forms {
			forms[i] = "{count} manual"
		}
		translations[code] = map[string]string{"label": "manual", "count": strings.Join(forms, " | ")}
	}
	plan := interfacesync.Plan{Targets: map[string]interfacesync.Target{"admin": {Source: source, English: en, Hashes: map[string]string{"label": interfacesync.Hash(source["label"]), "count": interfacesync.Hash(source["count"])}, Translations: translations}}}
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("input.json", raw, 0600))
	previousFlags, previousArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine = previousFlags; os.Args = previousArgs })
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"test", "--input", "input.json", "--output", "output.json"}
	require.NoError(t, run())
	require.Equal(t, 2, calls)
	raw, err = os.ReadFile("output.json")
	require.NoError(t, err)
	var result interfacesync.Result
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "開きます", result["admin"]["ja-JP"]["label"])
	require.Equal(t, "{count} 件の更新", result["admin"]["ja-JP"]["count"])
	raw, err = os.ReadFile("tmp/i18n-sync/calls.jsonl")
	require.NoError(t, err)
	require.NotContains(t, string(raw), "unit-test-credential")
	require.Contains(t, string(raw), "changed literal: admin/ja-JP/count")
	require.NotContains(t, string(raw), "admin/ja-JP/label")
}

// The model is configurable: a gateway that cannot serve the built-in default (or a deployment that
// pins a different one) must be able to name its own. The request body is what proves it reached the
// wire; the unset case pins the default so an existing run keeps its behaviour.
func TestModelComesFromTheEnvironmentAndDefaultsWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "set", value: "newapi/deepseek-v4.1-flash", want: "newapi/deepseek-v4.1-flash"},
		{name: "unset", value: "", want: "gpt-6-luna"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			seen := make([]string, 0, 1)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model string `json:"model"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				seen = append(seen, request.Model)
				translated := `{"ja-JP":{"path":"src/theme/settings.tsの変数"}}`
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "unit", "object": "chat.completion", "model": request.Model, "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": translated}}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}}))
			}))
			defer gateway.Close()
			t.Setenv("LLM_BASE_URL", gateway.URL)
			t.Setenv("LLM_API_KEY", "unit-test-credential")
			t.Setenv("LLM_MODEL", tc.value)
			t.Setenv("DATABASE_URL", "")
			source := "src/theme/settings.ts 中的变量"
			target := interfacesync.Target{
				Source:       map[string]string{"path": source},
				English:      map[string]string{"path": "Variable in src/theme/settings.ts"},
				Hashes:       map[string]string{"path": interfacesync.Hash(source)},
				Translations: map[string]map[string]string{"es-ES": {"path": "manual"}, "pt-BR": {"path": "manual"}, "ru-RU": {"path": "manual"}},
			}
			encoded, err := json.Marshal(interfacesync.Plan{Targets: map[string]interfacesync.Target{"admin": target}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile("input.json", encoded, 0600))
			previousFlags, previousArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine = previousFlags; os.Args = previousArgs })
			flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
			os.Args = []string{"test", "--input", "input.json", "--output", "output.json"}
			require.NoError(t, run())
			require.Equal(t, []string{tc.want}, seen, "the request carries the configured model")
		})
	}
}

func TestOfflineReplayDoesNotReadCredentialsOrWriteResults(t *testing.T) {
	t.Chdir(t.TempDir())
	source := map[string]string{"count": "{count}/{max}"}
	target := interfacesync.Target{Source: source, English: source, Translations: map[string]map[string]string{}}
	for _, code := range []string{"ja-JP", "es-ES", "pt-BR", "ru-RU"} {
		target.Translations[code] = source
	}
	encoded, err := json.Marshal(interfacesync.Plan{Targets: map[string]interfacesync.Target{"admin": target}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("input.json", encoded, 0600))
	require.NoError(t, os.WriteFile(".env.local", []byte("invalid credential configuration"), 0600))
	previousFlags, previousArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine = previousFlags; os.Args = previousArgs })
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"test", "--input", "input.json", "--output", "output.json", "--replay"}
	require.NoError(t, run())
	_, err = os.Stat("output.json")
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat("tmp/i18n-sync/calls.jsonl")
	require.True(t, os.IsNotExist(err))
}
