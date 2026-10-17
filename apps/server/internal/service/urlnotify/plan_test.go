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

func TestBuildPlanCoversTokensAndAnnouncements(t *testing.T) {
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
	require.Contains(t, plan.PurgePrefixes, "opennavo.example/ja/apps/new-name")
	require.Len(t, plan.PurgePrefixes, 5*6)

	// Live apps announce indexable languages; former tokens and removed apps announce all six.
	require.Contains(t, plan.IndexNowURLs, "https://opennavo.example/apps/editor")
	require.Contains(t, plan.IndexNowURLs, "https://opennavo.example/es/apps/old-name")
	require.Contains(t, plan.IndexNowURLs, "https://opennavo.example/pt/apps/gone")
	require.NotContains(t, plan.IndexNowURLs, "https://opennavo.example/apps/hidden-app")
	require.Len(t, plan.IndexNowURLs, 6*4)

	enforced := BuildPlan(changes[:1], current, index, seo.Policy{Mode: "enforce", Scope: "all"}, "https://opennavo.example")
	require.Equal(t, []string{"https://opennavo.example/apps/editor", "https://opennavo.example/zh/apps/editor"}, enforced.IndexNowURLs)
}

func TestBuildPlanPurgesEveryTokenByPrefixInAllLanguages(t *testing.T) {
	at := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	changes := []store.URLChange{
		{Seq: 1, PackageID: 1, Token: "zed", Reason: "homebrew", CreatedAt: at},
		// Renamed in this batch and again since: the current token is in no change.
		{Seq: 2, PackageID: 2, Token: "new-name", Reason: "homebrew", EventType: text("renamed"), PreviousToken: text("old-name"), CreatedAt: at},
		// A second change to the same app adds nothing.
		{Seq: 3, PackageID: 1, Token: "zed", Reason: "asset", CreatedAt: at},
	}
	current := map[int64]store.URLChange{1: {Token: "zed"}, 2: {Token: "newest-name"}}
	plan := BuildPlan(changes, current, map[int64]store.IndexRow{}, seo.Policy{Mode: "off"}, "https://opennavo.example/")

	require.Len(t, plan.PurgePrefixes, 4*6, "zed, newest-name, new-name and old-name in six languages")
	for _, prefix := range []string{"opennavo.example/apps/zed", "opennavo.example/zh/apps/zed", "opennavo.example/ru/apps/newest-name", "opennavo.example/ja/apps/new-name", "opennavo.example/pt/apps/old-name"} {
		require.Contains(t, plan.PurgePrefixes, prefix)
	}
	// Each prefix is a page-cache path on the site host, without scheme, tab or query string: it
	// covers the app's tabs, query variants and payloads at Cloudflare.
	require.Len(t, plan.Paths, len(plan.PurgePrefixes))
	for i, path := range plan.Paths {
		require.Equal(t, "opennavo.example"+path, plan.PurgePrefixes[i])
	}
}

func TestSectionPrefixesCoverLongCachedPagesInAllLanguages(t *testing.T) {
	prefixes := sectionPrefixes("https://opennavo.example/")
	require.Len(t, prefixes, 3*6)
	require.Equal(t, []string{"opennavo.example/apps", "opennavo.example/articles", "opennavo.example/authors"}, prefixes[:3])
	require.Contains(t, prefixes, "opennavo.example/zh/apps")
	require.Contains(t, prefixes, "opennavo.example/ru/authors")
	// Never the whole host or a whole language, which would drop share images and build assets too.
	for _, prefix := range prefixes {
		require.Regexp(t, `^opennavo\.example(/[a-z]{2})?/(apps|articles|authors)$`, prefix)
	}
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
		{Seq: 2, Paths: []string{"/authors/lin-wei", "/articles"}, Announce: false},
	}, "https://opennavo.example/")
	require.Len(t, plan.Paths, 3*6)
	require.Contains(t, plan.Paths, "/zh/articles/editors")
	require.Len(t, plan.PurgePrefixes, 2*6)
	require.Contains(t, plan.PurgePrefixes, "opennavo.example/ru/authors/lin-wei")
	for _, locale := range seo.Locales {
		require.NotContains(t, plan.PurgePrefixes, "opennavo.example"+localePrefix(locale)+"/articles")
		require.Contains(t, plan.Paths, localePrefix(locale)+"/articles")
	}
	require.Len(t, plan.IndexNowURLs, 2*6, "authors here were not announced")
	merged := Plan{Paths: []string{"/apps/editor", "/articles"}, PurgePrefixes: []string{"opennavo.example/apps/editor", "opennavo.example/articles"}}.Merge(plan)
	require.Equal(t, "/apps/editor", merged.Paths[0])
	require.Len(t, merged.Paths, 1+3*6)
	require.Equal(t, "opennavo.example/apps/editor", merged.PurgePrefixes[0])
	require.Len(t, merged.PurgePrefixes, 2+2*6, "each prefix once")

	// The home page is refreshed, but as a prefix it would purge the whole host.
	home := ContentPlan([]store.ContentURLChange{{Seq: 3, Paths: []string{"/"}}}, "https://opennavo.example")
	require.Len(t, home.Paths, 6)
	require.Empty(t, home.PurgePrefixes)
}
