package urlnotify

import (
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func text(value string) *string { return &value }

func indexRow(id int64, token string) store.IndexRow {
	return store.IndexRow{ID: id, Token: token, Name: token, Summary: "Edits code", Version: "1.0", Override: seo.OverrideAuto, SourceLocale: "en-US", PublishedLocales: []string{"en-US", "zh-CN"}}
}

func TestBuildPlanCoversTokensTabsAndAnnouncements(t *testing.T) {
	at := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	changes := []store.URLChange{
		{Seq: 1, PackageID: 1, Token: "editor", Reason: "homebrew", EventType: text("updated"), CreatedAt: at},
		{Seq: 2, PackageID: 2, Token: "new-name", Reason: "homebrew", EventType: text("renamed"), PreviousToken: text("old-name"), CreatedAt: at},
		{Seq: 3, PackageID: 3, Token: "gone", Op: "delete", Reason: "homebrew", EventType: text("removed"), CreatedAt: at},
		{Seq: 4, PackageID: 4, Token: "hidden-app", Reason: "homebrew", CreatedAt: at},
	}
	current := map[int64]store.URLChange{1: {Token: "editor"}, 2: {Token: "new-name"}, 3: {Token: "gone", Op: "delete"}, 4: {Token: "hidden-app"}}
	hidden := indexRow(4, "hidden-app")
	hidden.Hidden = true
	index := map[int64]store.IndexRow{1: indexRow(1, "editor"), 2: indexRow(2, "new-name"), 4: hidden}
	plan := BuildPlan(changes, current, index, seo.Policy{Mode: "shadow", Scope: "all"}, "https://opennavo.example/")

	require.Contains(t, plan.Paths, "/apps/editor")
	require.Contains(t, plan.Paths, "/zh/apps/old-name")
	require.Contains(t, plan.Paths, "/ru/apps/gone")
	require.Len(t, plan.Paths, 5*6, "editor, new-name, old-name, gone, hidden-app in six languages")
	require.Contains(t, plan.PurgeURLs, "https://opennavo.example/ja/apps/new-name/versions")
	require.Len(t, plan.PurgeURLs, 5*6*4)

	// Live apps announce indexable languages; former tokens and removed apps announce all six.
	require.Contains(t, plan.IndexNowURLs, "https://opennavo.example/apps/editor")
	require.Contains(t, plan.IndexNowURLs, "https://opennavo.example/es/apps/old-name")
	require.Contains(t, plan.IndexNowURLs, "https://opennavo.example/pt/apps/gone")
	require.NotContains(t, plan.IndexNowURLs, "https://opennavo.example/apps/hidden-app")
	require.Len(t, plan.IndexNowURLs, 6*4)

	enforced := BuildPlan(changes[:1], current, index, seo.Policy{Mode: "enforce", Scope: "all"}, "https://opennavo.example")
	require.Equal(t, []string{"https://opennavo.example/apps/editor", "https://opennavo.example/zh/apps/editor"}, enforced.IndexNowURLs)
}

func TestBuildPlanRefreshesButDoesNotAnnounceBulkContentRewrites(t *testing.T) {
	at := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	changes := []store.URLChange{}
	current := map[int64]store.URLChange{}
	index := map[int64]store.IndexRow{}
	for id := int64(1); id <= bulkContentRows+1; id++ {
		token := "app-" + string(rune('a'+id%26)) + string(rune('a'+id/26))
		changes = append(changes, store.URLChange{Seq: id, PackageID: id, Token: token, Reason: "content", CreatedAt: at})
		current[id] = store.URLChange{Token: token}
		index[id] = indexRow(id, token)
	}
	// One app also changed on its own, so it is still announced.
	changes = append(changes, store.URLChange{Seq: 99, PackageID: 1, Token: current[1].Token, Reason: "asset", CreatedAt: at.Add(time.Minute)})
	plan := BuildPlan(changes, current, index, seo.Policy{Mode: "off"}, "https://opennavo.example")
	require.Len(t, plan.Paths, (bulkContentRows+1)*6)
	require.Len(t, plan.IndexNowURLs, 6)
}

func TestContentPlanCoversAllLanguagesAndMerges(t *testing.T) {
	plan := ContentPlan([]store.ContentURLChange{
		{Seq: 1, Paths: []string{"/articles/editors", "/articles"}, Announce: true},
		{Seq: 2, Paths: []string{"/authors/lin-wei"}, Announce: false},
	}, "https://opennavo.example/")
	require.Len(t, plan.Paths, 3*6)
	require.Contains(t, plan.Paths, "/zh/articles/editors")
	require.Contains(t, plan.PurgeURLs, "https://opennavo.example/ru/authors/lin-wei")
	require.Len(t, plan.IndexNowURLs, 2*6, "authors here were not announced")
	merged := Plan{Paths: []string{"/apps/editor", "/articles"}}.Merge(plan)
	require.Equal(t, "/apps/editor", merged.Paths[0])
	require.Len(t, merged.Paths, 1+3*6)
}
