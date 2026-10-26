//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// 00112 adds the training crawlers that fetch at scale to a stored limited group, keeping the admin's mode,
// delay and bots, and bumps the settings revision like an admin save so cached site settings refresh.
func TestTrainingCrawlersJoinTheLimitedGroup(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 111)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	type settingsRow struct {
		Settings  string
		Revision  int64
		UpdatedAt time.Time
	}
	read := func() (map[string]any, settingsRow) {
		t.Helper()
		var row settingsRow
		require.NoError(t, st.DB.Raw("SELECT settings::text AS settings,revision,updated_at FROM seo_settings WHERE id=true").Scan(&row).Error)
		var settings map[string]any
		require.NoError(t, json.Unmarshal([]byte(row.Settings), &settings))
		return settings, row
	}
	training := func(settings map[string]any) map[string]any {
		return settings["crawlers"].(map[string]any)["training"].(map[string]any)
	}
	stored := func(crawlers string) {
		t.Helper()
		require.NoError(t, goose.DownToContext(ctx, db, ".", 111))
		if crawlers == "" {
			require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=settings-'crawlers',revision=7,updated_at='2026-10-20T00:00:00Z' WHERE id=true`).Error)
		} else {
			require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{crawlers}',?::jsonb),revision=7,updated_at='2026-10-20T00:00:00Z' WHERE id=true`, crawlers).Error)
		}
		require.NoError(t, st.DB.Exec(`DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
		require.NoError(t, goose.UpToContext(ctx, db, ".", 112))
	}
	invalidations := func() []string {
		t.Helper()
		var rows []string
		require.NoError(t, st.DB.Raw(`SELECT unique_key||' '||payload::text FROM job_outbox WHERE job_type='cache:invalidate' ORDER BY id`).Scan(&rows).Error)
		return rows
	}

	// Production on 2026-10-26: an admin had added GPTBot.
	production := `{"seoTools":{"bots":["MJ12bot","SerpstatBot"],"mode":"block","delaySeconds":10},"training":{"bots":["ClaudeBot","GPTBot"],"mode":"limit","delaySeconds":10}}`
	stored(production)
	settings, row := read()
	require.Equal(t, []any{"ClaudeBot", "GPTBot", "Amazonbot", "Bytespider", "CCBot", "Meta-ExternalAgent", "anthropic-ai", "MistralAI-Training"}, training(settings)["bots"])
	require.Equal(t, "limit", training(settings)["mode"])
	require.EqualValues(t, 10, training(settings)["delaySeconds"])
	require.Equal(t, map[string]any{"bots": []any{"MJ12bot", "SerpstatBot"}, "mode": "block", "delaySeconds": float64(10)}, settings["crawlers"].(map[string]any)["seoTools"])
	require.Equal(t, map[string]any{"usageEnabled": true}, settings["analytics"], "other settings stay")
	require.EqualValues(t, 8, row.Revision)
	require.True(t, row.UpdatedAt.After(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)))
	require.Equal(t, []string{`cache:invalidate:seo-settings ["c:site:*"]`}, invalidations())
	decoded, err := storeDecode(settings)
	require.NoError(t, err)
	require.Len(t, decoded.Crawlers.Training.Bots, 8)

	// Down removes what Up may have added, but not the admin's GPTBot; Up again is the same as before.
	require.NoError(t, goose.DownToContext(ctx, db, ".", 111))
	settings, row = read()
	require.Equal(t, []any{"ClaudeBot", "GPTBot"}, training(settings)["bots"])
	require.EqualValues(t, 9, row.Revision)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 112))
	settings, _ = read()
	require.Len(t, training(settings)["bots"], 8)

	// Names compare case-insensitively; the admin's mode and delay stay.
	stored(`{"training":{"bots":["claudebot","gptbot","CCBOT"],"mode":"block","delaySeconds":30}}`)
	settings, row = read()
	require.Equal(t, []any{"claudebot", "gptbot", "CCBOT", "Amazonbot", "Bytespider", "Meta-ExternalAgent", "anthropic-ai", "MistralAI-Training"}, training(settings)["bots"])
	require.Equal(t, "block", training(settings)["mode"])
	require.EqualValues(t, 30, training(settings)["delaySeconds"])
	require.EqualValues(t, 8, row.Revision)

	// Up to the server's 20 bots the list grows; beyond them it stays as it is, and nothing is announced.
	limit := []string{}
	for i := range 13 {
		limit = append(limit, "Bot"+string(rune('A'+i)))
	}
	raw, err := json.Marshal(map[string]any{"training": map[string]any{"bots": limit, "mode": "limit", "delaySeconds": 10}})
	require.NoError(t, err)
	stored(string(raw))
	settings, _ = read()
	require.Len(t, training(settings)["bots"], 20)
	full := []string{}
	for i := range 17 {
		full = append(full, "Bot"+string(rune('A'+i)))
	}
	raw, err = json.Marshal(map[string]any{"training": map[string]any{"bots": full, "mode": "limit", "delaySeconds": 10}})
	require.NoError(t, err)
	stored(string(raw))
	settings, row = read()
	require.Len(t, training(settings)["bots"], 17)
	require.EqualValues(t, 7, row.Revision)
	require.Empty(t, invalidations())

	// Nothing stored: the server's defaults apply, and they already list these bots.
	stored("")
	settings, row = read()
	require.NotContains(t, settings, "crawlers")
	require.EqualValues(t, 7, row.Revision)
	decoded, err = storeDecode(settings)
	require.NoError(t, err)
	require.Equal(t, []string{"ClaudeBot", "GPTBot", "Amazonbot", "Bytespider", "CCBot", "Meta-ExternalAgent", "anthropic-ai", "MistralAI-Training"}, decoded.Crawlers.Training.Bots)
	require.Equal(t, "limit", decoded.Crawlers.Training.Mode)
	require.Equal(t, 10, decoded.Crawlers.Training.DelaySeconds)
}

func storeDecode(settings map[string]any) (store.SEOSettings, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return store.SEOSettings{}, err
	}
	return store.DecodeSEOSettings(raw)
}
