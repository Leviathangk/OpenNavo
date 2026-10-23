package interfacesync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResumeAfterInterruptedRunRequestsOnlyRemainingBatch(t *testing.T) {
	plan := fixture()
	delete(plan.Targets["server"].Translations["ja-JP"], "a")
	delete(plan.Targets["server"].Translations["es-ES"], "a")
	cache := ResumeCache{Directory: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	firstCalls := 0
	_, err := Run(ctx, plan, func(context.Context, Batch) (map[string]map[string]string, error) {
		firstCalls++
		return map[string]map[string]string{"ja-JP": {"a": "開きます"}}, nil
	}, DefaultBrands, nil, func(b Batch, out map[string]map[string]string) error {
		if saveErr := cache.Save(b, out); saveErr != nil {
			return saveErr
		}
		cancel()
		return nil
	})
	require.Error(t, err)
	require.Equal(t, 1, firstCalls)
	// Ignore the first result to simulate interruption before either language packs or output were written.
	resumed, notices, err := cache.Restore(plan, DefaultBrands, nil)
	require.NoError(t, err)
	require.Contains(t, notices, "Cache restored: server/ja-JP/a")
	require.NotContains(t, plan.Targets["server"].Translations["ja-JP"], "a")
	secondCalls := 0
	result, err := Run(context.Background(), resumed, func(_ context.Context, b Batch) (map[string]map[string]string, error) {
		secondCalls++
		require.Equal(t, []string{"es-ES"}, b.Locales)
		return map[string]map[string]string{"es-ES": {"a": "Abre la aplicación"}}, nil
	}, DefaultBrands, nil, cache.Save)
	require.NoError(t, err)
	require.Equal(t, 1, secondCalls)
	require.Equal(t, "開きます", result["server"]["ja-JP"]["a"])
	require.Equal(t, "Abre la aplicación", result["server"]["es-ES"]["a"])
}

func TestResumeInvalidatesSourceEnglishAndRevalidatesTranslation(t *testing.T) {
	for _, change := range []string{"source", "english", "literal", "brand", "plural", "language", "glossary"} {
		t.Run(change, func(t *testing.T) {
			plan := fixture()
			target := plan.Targets["server"]
			target.Source["a"] = "OpenNavo 打开 {count}"
			target.English["a"] = "OpenNavo open {count}"
			delete(target.Translations["ja-JP"], "a")
			plan.Targets["server"] = target
			cache := ResumeCache{Directory: t.TempDir()}
			b := Batch{Target: "server", Locales: []string{"ja-JP"}, Source: map[string]string{"a": target.Source["a"]}, English: map[string]string{"a": target.English["a"]}}
			require.NoError(t, cache.Save(b, map[string]map[string]string{"ja-JP": {"a": "OpenNavo を開きます {count}"}}))
			var glossary map[string]map[string]string
			if change == "source" {
				target.Source["a"] = "OpenNavo 新的源文 {count}"
			}
			if change == "english" {
				target.English["a"] = "OpenNavo launch {count}"
			}
			if change == "glossary" {
				glossary = map[string]map[string]string{"打开": {"ja-JP": "起動します"}}
			}
			if change == "literal" || change == "brand" || change == "plural" || change == "language" {
				raw, err := os.ReadFile(cache.path("server"))
				require.NoError(t, err)
				var saved resumeFile
				require.NoError(t, json.Unmarshal(raw, &saved))
				entry := saved.Entries["ja-JP"]["a"]
				switch change {
				case "literal":
					entry.Translation = "OpenNavo を開きます {bad}"
				case "brand":
					entry.Translation = "オープンナボを開きます {count}"
				case "plural":
					entry.Translation = "OpenNavo を開きます {count} | OpenNavo を開きます {count}"
				case "language":
					entry.Translation = "OpenNavo open the application {count}"
				}
				saved.Entries["ja-JP"]["a"] = entry
				raw, err = json.Marshal(saved)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(cache.path("server"), raw, 0600))
			}
			plan.Targets["server"] = target
			restored, _, err := cache.Restore(plan, DefaultBrands, glossary)
			require.NoError(t, err)
			require.NotContains(t, restored.Targets["server"].Translations["ja-JP"], "a")
		})
	}
}

func TestResumeInvalidatesOnlyChangedKey(t *testing.T) {
	plan := fixture()
	target := plan.Targets["server"]
	target.Translations["ja-JP"] = map[string]string{}
	plan.Targets["server"] = target
	cache := ResumeCache{Directory: t.TempDir()}
	require.NoError(t, cache.Save(Batch{Target: "server", Locales: []string{"ja-JP"}, Source: target.Source, English: target.English}, map[string]map[string]string{"ja-JP": {"a": "開きます", "b": "閉じます"}}))
	target.English["a"] = "Launch"
	resumed, _, err := cache.Restore(plan, DefaultBrands, nil)
	require.NoError(t, err)
	require.Equal(t, "閉じます", resumed.Targets["server"].Translations["ja-JP"]["b"])
	batches := Batches(resumed)
	require.Len(t, batches, 1)
	require.Equal(t, map[string]string{"a": "打开"}, batches[0].Source)
}

func TestResumeConcurrentBatchesMergeAtomically(t *testing.T) {
	cache := ResumeCache{Directory: t.TempDir()}
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	for _, key := range []string{"a", "b"} {
		workers.Go(func() {
			b := Batch{Target: "server", Locales: []string{"ja-JP"}, Source: map[string]string{key: "打开"}, English: map[string]string{key: "Open"}}
			errors <- cache.Save(b, map[string]map[string]string{"ja-JP": {key: "開きます"}})
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	saved, err := cache.read("server")
	require.NoError(t, err)
	require.Len(t, saved.Entries["ja-JP"], 2)
	stat, err := os.Stat(cache.path("server"))
	require.NoError(t, err)
	require.EqualValues(t, 0600, stat.Mode().Perm())
	temporary, err := filepath.Glob(filepath.Join(cache.Directory, ".resume-*"))
	require.NoError(t, err)
	require.Empty(t, temporary)
	require.Equal(t, Hash("打开"), saved.Entries["ja-JP"]["a"].SourceHash)
	require.Equal(t, Hash("Open"), saved.Entries["ja-JP"]["a"].EnglishHash)
}

func TestRejectedBatchIsNeverPersisted(t *testing.T) {
	plan := fixture()
	delete(plan.Targets["server"].Translations["ja-JP"], "a")
	cache := ResumeCache{Directory: t.TempDir()}
	calls := 0
	_, err := Run(context.Background(), plan, func(context.Context, Batch) (map[string]map[string]string, error) {
		calls++
		return map[string]map[string]string{"ja-JP": {"a": "開きます {changed}"}}, nil
	}, DefaultBrands, nil, cache.Save)
	require.Error(t, err)
	require.Equal(t, 2, calls)
	files, err := filepath.Glob(filepath.Join(cache.Directory, "*.json"))
	require.NoError(t, err)
	require.Empty(t, files)
	resumed, _, err := cache.Restore(plan, DefaultBrands, nil)
	require.NoError(t, err)
	require.Len(t, Batches(resumed), 1)
}
