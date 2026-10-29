package sourcefacts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGitHubReposPreferHomepageThenDownloadThenRecorded(t *testing.T) {
	require.Equal(t, []string{"rxhanson/Rectangle"}, GitHubRepos("https://rectangleapp.com/", "https://github.com/rxhanson/Rectangle/releases/download/v2.0.3/Rectangle2.0.3.dmg", ""))
	require.Equal(t, []string{"foo/app", "foo/app-releases", "microsoft/vscode"}, GitHubRepos("https://github.com/foo/app", "https://github.com/foo/app-releases/releases/latest/download/App.zip", "https://github.com/microsoft/vscode.git"))
	// Same repository in another case, an organization page, site pages and other hosts give nothing new.
	require.Equal(t, []string{"Foo/App"}, GitHubRepos("https://github.com/Foo/App/", "https://github.com/foo/app/archive/v1.zip", ""))
	require.Empty(t, GitHubRepos("https://github.com/microsoft", "https://github.com/sponsors/someone/x", "https://gitlab.com/a/b"))
	require.Empty(t, GitHubRepos("https://foo.github.io/app/", "ftp://github.com/a/b", "not a url"))
	require.Equal(t, []string{"owner/repo"}, GitHubRepos("", "https://raw.githubusercontent.com/owner/repo/main/App.zip", ""))
}

func artifacts(t *testing.T, entries ...map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"apps": []string{"App.app"}, "entries": entries})
	require.NoError(t, err)
	return raw
}

func TestBundleIDsRankQuitThenContainersThenPreferences(t *testing.T) {
	raw := artifacts(t,
		map[string]any{"type": "app", "phase": "install", "declaration": map[string]any{"app": []string{"Rectangle.app"}}},
		map[string]any{"type": "uninstall", "phase": "uninstall", "declaration": map[string]any{"uninstall": []any{map[string]any{"login_item": "Rectangle", "quit": "com.knollsoft.Rectangle"}}}},
		map[string]any{"type": "zap", "phase": "cleanup", "declaration": map[string]any{"zap": []any{map[string]any{"trash": []any{
			"~/Library/Application Scripts/com.knollsoft.RectangleLauncher",
			"~/Library/Application Support/Rectangle",
			"~/Library/Caches/com.knollsoft.Rectangle",
			"~/Library/Containers/com.knollsoft.RectangleLauncher",
			"~/Library/HTTPStorages/com.knollsoft.Rectangle",
			"~/Library/Preferences/com.knollsoft.Rectangle.plist",
			"~/Library/Preferences/com.apple.sharedfilelist.plist",
			"~/Library/Group Containers/group.com.knollsoft",
			"~/Library/Caches/com.knollsoft.*",
			"~/Library/Preferences/ByHost/com.knollsoft.Rectangle.ABC.plist",
		}}}}},
	)
	require.Equal(t, []string{"com.knollsoft.Rectangle", "com.knollsoft.RectangleLauncher"}, BundleIDs(raw))
	// Two-part identifiers count; plain folder names, globs and missing data do not.
	raw = artifacts(t, map[string]any{"type": "zap", "phase": "cleanup", "declaration": map[string]any{"zap": map[string]any{"trash": "~/Library/Preferences/md.obsidian.plist", "delete": []any{"~/Library/Caches/Obsidian", 3}}}})
	require.Equal(t, []string{"md.obsidian"}, BundleIDs(raw))
	// Team-prefixed shared folders, shared crash reporters and cookie files reduce to the app itself.
	raw = artifacts(t, map[string]any{"type": "zap", "phase": "cleanup", "declaration": map[string]any{"zap": []any{map[string]any{"trash": []any{
		"~/Library/Application Scripts/2BUA8C4S2C.com.1password.browser-helper",
		"~/Library/Caches/com.plausiblelabs.crashreporter.data/ru.keepcoder.Telegram",
		"~/Library/HTTPStorages/com.googlecode.iterm2.binarycookies",
		"~/Library/HTTPStorages/com.googlecode.iterm2",
	}}}}})
	require.Equal(t, []string{"com.googlecode.iterm2"}, BundleIDs(raw))
	require.Empty(t, BundleIDs(nil))
	require.Empty(t, BundleIDs(json.RawMessage(`{"apps":[]}`)))
	// At most four, strongest first.
	quit := []any{"a.b.one", "a.b.two", "a.b.three", "a.b.four", "a.b.five"}
	raw = artifacts(t, map[string]any{"type": "uninstall", "phase": "uninstall", "declaration": map[string]any{"uninstall": []any{map[string]any{"quit": quit}}}},
		map[string]any{"type": "zap", "phase": "cleanup", "declaration": map[string]any{"zap": []any{map[string]any{"trash": "~/Library/Containers/a.b.five"}}}})
	require.Equal(t, []string{"a.b.five", "a.b.one", "a.b.two", "a.b.three"}, BundleIDs(raw))
}

func TestNameMatchesNeedsContainmentNotAVendorWord(t *testing.T) {
	require.True(t, NameMatches([]string{"Slack", "slack"}, "Slack for Desktop"))
	require.True(t, NameMatches([]string{"Telegram Desktop"}, "Telegram"))
	require.True(t, NameMatches([]string{"The Unarchiver"}, "The Unarchiver"))
	require.True(t, NameMatches([]string{"1Password 7"}, "1Password 7 - Password Manager"))
	require.False(t, NameMatches([]string{"Microsoft Visual Studio Code"}, "Microsoft Word"))
	require.False(t, NameMatches([]string{"Go"}, "Go Flashcards"))
	require.False(t, NameMatches([]string{"Keka"}, ""))
}

func repository(t *testing.T, raw string) Repository {
	t.Helper()
	var r Repository
	require.NoError(t, json.Unmarshal([]byte(raw), &r))
	return r
}

func TestOpenSourceNeedsCodeAndAnOSILicense(t *testing.T) {
	fact, ok := repository(t, `{"full_name":"gnachman/iTerm2","html_url":"https://github.com/gnachman/iTerm2","language":"Objective-C","license":{"spdx_id":"GPL-2.0"},"archived":false,"stargazers_count":15678,"pushed_at":"2026-10-13T08:00:00Z"}`).OpenSource()
	require.True(t, ok)
	require.Equal(t, OpenSource{Repo: "gnachman/iTerm2", URL: "https://github.com/gnachman/iTerm2", License: "GPL-2.0"}, fact)
	github, ok := repository(t, `{"full_name":"gnachman/iTerm2","html_url":"https://github.com/gnachman/iTerm2","language":"Objective-C","license":{"spdx_id":"GPL-2.0"},"archived":false,"stargazers_count":15678,"pushed_at":"2026-10-13T08:00:00Z"}`).GitHubFact()
	require.True(t, ok)
	pushed := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	require.Equal(t, "gnachman/iTerm2", github.Repo)
	require.Equal(t, int64(15678), *github.Stars)
	require.Equal(t, pushed, *github.PushedAt)
	for name, raw := range map[string]string{
		"releases only":     `{"full_name":"obsidianmd/obsidian-releases","html_url":"https://github.com/obsidianmd/obsidian-releases","language":null,"license":null}`,
		"unrecognized":      `{"full_name":"rxhanson/Rectangle","html_url":"https://github.com/rxhanson/Rectangle","language":"Swift","license":{"spdx_id":"NOASSERTION"}}`,
		"not OSI":           `{"full_name":"a/b","html_url":"https://github.com/a/b","language":"Go","license":{"spdx_id":"CC-BY-NC-4.0"}}`,
		"private":           `{"full_name":"a/b","html_url":"https://github.com/a/b","language":"Go","private":true,"license":{"spdx_id":"MIT"}}`,
		"mismatched answer": `{"full_name":"a/b","html_url":"https://github.com/c/d","language":"Go","license":{"spdx_id":"MIT"}}`,
	} {
		_, ok := repository(t, raw).OpenSource()
		require.False(t, ok, name)
	}
}

type fakeGetter struct {
	calls   []string
	etags   []string
	answers []fakeAnswer
}
type fakeAnswer struct {
	status int
	body   string
	header http.Header
}

func (f *fakeGetter) Get(_ context.Context, target string, _ int64, headers http.Header) ([]byte, int, http.Header, error) {
	f.calls = append(f.calls, target)
	f.etags = append(f.etags, headers.Get("If-None-Match"))
	answer := f.answers[0]
	f.answers = f.answers[1:]
	if answer.header == nil {
		answer.header = http.Header{}
	}
	return []byte(answer.body), answer.status, answer.header, nil
}

func TestGitHubRepoStatuses(t *testing.T) {
	ctx := context.Background()
	getter := &fakeGetter{answers: []fakeAnswer{{status: 200, body: `{"full_name":"a/b","html_url":"https://github.com/a/b","language":"Go","license":{"spdx_id":"MIT"}}`, header: http.Header{"Etag": {`W/"1"`}, "X-Ratelimit-Remaining": {"4999"}}}}}
	result, err := (&GitHub{HTTP: getter}).Repo(ctx, "a/b", "")
	require.NoError(t, err)
	require.Equal(t, []string{"https://api.github.com/repos/a/b"}, getter.calls)
	require.Equal(t, 1, result.Requests)
	require.Equal(t, `W/"1"`, result.ETag)
	require.Equal(t, int64(4999), result.Remaining)
	require.Equal(t, "a/b", result.Repository.FullName)

	getter = &fakeGetter{answers: []fakeAnswer{{status: 304}}}
	result, err = (&GitHub{HTTP: getter}).Repo(ctx, "a/b", `W/"1"`)
	require.NoError(t, err)
	require.True(t, result.NotModified)
	require.Equal(t, []string{`W/"1"`}, getter.etags)

	getter = &fakeGetter{answers: []fakeAnswer{{status: 404}}}
	result, err = (&GitHub{HTTP: getter}).Repo(ctx, "a/b", "")
	require.NoError(t, err)
	require.True(t, result.NotFound)

	// Renamed repositories are followed once, without the old ETag.
	getter = &fakeGetter{answers: []fakeAnswer{
		{status: 301, header: http.Header{"Location": {"https://api.github.com/repositories/42"}}},
		{status: 200, body: `{"full_name":"a/c","html_url":"https://github.com/a/c","language":"Go"}`},
	}}
	result, err = (&GitHub{HTTP: getter}).Repo(ctx, "a/b", `W/"1"`)
	require.NoError(t, err)
	require.Equal(t, []string{"https://api.github.com/repos/a/b", "https://api.github.com/repositories/42"}, getter.calls)
	require.Equal(t, 2, result.Requests)
	require.Equal(t, []string{`W/"1"`, ""}, getter.etags)
	require.Equal(t, "a/c", result.Repository.FullName)

	getter = &fakeGetter{answers: []fakeAnswer{{status: 301, header: http.Header{"Location": {"https://evil.example/repos/a/b"}}}}}
	_, err = (&GitHub{HTTP: getter}).Repo(ctx, "a/b", "")
	require.Error(t, err)

	getter = &fakeGetter{answers: []fakeAnswer{{status: 403, header: http.Header{"X-Ratelimit-Remaining": {"0"}}}}}
	_, err = (&GitHub{HTTP: getter}).Repo(ctx, "a/b", "")
	require.ErrorIs(t, err, ErrRateLimited)
	getter = &fakeGetter{answers: []fakeAnswer{{status: 403, header: http.Header{"X-Ratelimit-Remaining": {"12"}}}}}
	_, err = (&GitHub{HTTP: getter}).Repo(ctx, "a/b", "")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRateLimited)
}

func TestAppStoreLookupKeepsMacListings(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"resultCount":3,"results":[
			{"kind":"mac-software","bundleId":"com.aone.keka","trackName":"Keka","trackViewUrl":"https://apps.apple.com/us/app/keka/id470158793?mt=12&uo=4","price":6.49,"currency":"USD","languageCodesISO2A":["EN","ZH","zh","JA"],"sellerName":"Jorge Garcia Armero"},
			{"kind":"software","bundleId":"net.whatsapp.WhatsApp","trackName":"WhatsApp Messenger","trackViewUrl":"https://apps.apple.com/us/app/whatsapp-messenger/id310633997?uo=4","price":0,"currency":"USD"},
			{"kind":"mac-software","bundleId":"x.y.z","trackName":"Bad link","trackViewUrl":"http://example.com/app","price":0,"currency":"USD"}
		]}`))
	}))
	defer server.Close()
	store := &AppStore{Client: server.Client(), Base: server.URL, UserAgent: "test"}
	listings, err := store.Lookup(context.Background(), []string{"com.aone.keka", "net.whatsapp.WhatsApp", "x.y.z"})
	require.NoError(t, err)
	require.Contains(t, query, "bundleId=com.aone.keka%2Cnet.whatsapp.WhatsApp%2Cx.y.z")
	require.Contains(t, query, "country=us")
	require.Equal(t, map[string]Listing{"com.aone.keka": {Name: "Keka", URL: "https://apps.apple.com/us/app/keka/id470158793?mt=12", BundleID: "com.aone.keka",
		Price: 6.49, Currency: "USD", Region: "US", Languages: []string{"EN", "ZH", "JA"}, Seller: "Jorge Garcia Armero"}}, listings)

	listing, ok := Match([]string{"com.aone.KEKA"}, []string{"Keka"}, listings)
	require.True(t, ok)
	require.Equal(t, "Keka", listing.Name)
	_, ok = Match([]string{"com.aone.keka"}, []string{"Archive Utility"}, listings)
	require.False(t, ok)

	_, err = store.Lookup(context.Background(), make([]string, MaxLookupIDs+1))
	require.Error(t, err)
	empty, err := store.Lookup(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestAppStoreLookupRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	_, err := (&AppStore{Client: server.Client(), Base: server.URL}).Lookup(context.Background(), []string{"a.b"})
	require.ErrorIs(t, err, ErrRateLimited)
}
