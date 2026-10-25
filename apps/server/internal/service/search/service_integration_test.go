//go:build integration

package search

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDocumentedSearchAcceptanceFiltersSuggestionAndClicks(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	mutations := []store.CatalogMutation{}
	for _, example := range []struct{ Kind, Raw string }{
		{"cask", `{"token":"visual-studio-code","name":["Visual Studio Code","VS Code"],"version":"1","desc":"Code editor"}`},
		{"cask", `{"token":"wechat","name":["WeChat","微信"],"version":"1"}`},
		{"cask", `{"token":"docker-desktop","name":["Docker Desktop"],"version":"1"}`},
		{"cask", `{"token":"font-jetbrains-mono","name":["JetBrains Mono"],"version":"1"}`},
		{"cask", `{"token":"disabled","name":["Disabled"],"version":"1","disabled":true}`},
		{"cask", `{"token":"hidden","version":"1"}`},
		{"formula", `{"name":"ripgrep","versions":{"stable":"1"},"executables":["rg"]}`},
		{"formula", `{"name":"python@3.13","aliases":["python"],"versions":{"stable":"3.13"}}`},
		{"formula", `{"name":"python@3.14","aliases":["python"],"versions":{"stable":"3.14"}}`},
	} {
		item, err := homebrew.Normalize(example.Kind, json.RawMessage(example.Raw))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: item})
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutations))
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE packages SET popularity=CASE WHEN token='python@3.14' THEN 9 ELSE 1 END").Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO package_meta (package_id,hidden) SELECT id,true FROM packages WHERE token='hidden'").Error)
	service := New(ctx, st, cache.Redis{Client: st.Redis}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(service.Close)
	for _, example := range []struct {
		Query, Token string
		Position     int
	}{{"vscode", "visual-studio-code", 1}, {"微信", "wechat", 1}, {"weixin", "wechat", 1}, {"docker", "docker-desktop", 3}, {"rg", "ripgrep", 3}, {"python", "python@3.14", 1}, {"jetbrains mono", "font-jetbrains-mono", 3}} {
		t.Run(example.Query, func(t *testing.T) {
			input := validInput()
			input.Query = example.Query
			input.Size = 3
			result, err := service.Search(ctx, input)
			require.NoError(t, err)
			tokens := []string{}
			for _, match := range result.Records {
				var token string
				require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT token FROM packages WHERE id=?", match.ID).Scan(&token).Error)
				tokens = append(tokens, token)
			}
			switch {
			case example.Query == "rg" || example.Query == "python":
				require.Empty(t, tokens)
			case example.Position == 1:
				require.NotEmpty(t, tokens)
				require.Equal(t, example.Token, tokens[0])
			default:
				require.Contains(t, tokens, example.Token)
			}
		})
	}
	input := validInput()
	input.Query = "hidden"
	result, err := service.Search(ctx, input)
	require.NoError(t, err)
	require.Empty(t, result.Records)
	input.Query = "disabled"
	result, err = service.Search(ctx, input)
	require.NoError(t, err)
	require.Empty(t, result.Records)
	input.IncludeDisabled = true
	result, err = service.Search(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	input = validInput()
	input.Query = "wx"
	result, err = service.Search(ctx, input)
	require.NoError(t, err)
	require.NoError(t, service.Click(ctx, result.QueryID, "cask", "wechat", 1))
	id, err := strconv.ParseInt(result.QueryID, 10, 64)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var clicks int64
		err := st.DB.WithContext(ctx).Table("search_queries").Where("id=? AND clicked_package_id IS NOT NULL AND clicked_position=1", id).Count(&clicks).Error
		return err == nil && clicks == 1
	}, time.Second, 10*time.Millisecond)
	suggestions, err := service.Suggest(ctx, "weix", "zh-CN", 8)
	require.NoError(t, err)
	require.NotEmpty(t, suggestions)
	require.Equal(t, "wechat", *suggestions[0].Token)
	value, err := st.Redis.Get(ctx, "c:sug:c2:zh-CN:weix").Result()
	require.NoError(t, err)
	require.NotEmpty(t, value)
	input.Query = "'; SELECT pg_sleep(30); --"
	_, err = service.Search(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 0.5, st.SearchPenalty(ctx, "missing", 0.5))
}
