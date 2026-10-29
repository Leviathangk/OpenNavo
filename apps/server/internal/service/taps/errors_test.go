package taps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

// reasonFormat is what agents receive: tap_<reason>, then an optional detail of letters, digits and _.-@+/.
var reasonFormat = regexp.MustCompile(`^rejected: tap_[a-z_]+(:[A-Za-z0-9_.@+/-]+)?$`)

// scriptedGitHub answers commit lists and raw files for owner/homebrew-tap.
type scriptedGitHub struct {
	commits map[string]func() ([]byte, int, error) // by cask path
	files   map[string]func() ([]byte, int, error) // by sha/path
}

func (g scriptedGitHub) Get(_ context.Context, target string, _ int64, _ http.Header) ([]byte, int, http.Header, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, 0, nil, err
	}
	answer := func(fn func() ([]byte, int, error)) ([]byte, int, http.Header, error) {
		if fn == nil {
			return nil, http.StatusNotFound, http.Header{}, nil
		}
		body, status, err := fn()
		return body, status, http.Header{}, err
	}
	if parsed.Host == "api.github.com" {
		return answer(g.commits[parsed.Query().Get("path")])
	}
	return answer(g.files[strings.TrimPrefix(parsed.Path, "/owner/homebrew-tap/")])
}

func respond(body string, status int) func() ([]byte, int, error) {
	return func() ([]byte, int, error) { return []byte(body), status, nil }
}

func caskSource(extra string) string {
	return `cask "app" do
  version "1.0"
  sha256 "` + strings.Repeat("0", 64) + `"

  url "https://example.com/App-#{version}.dmg"
  name "App"
  desc "Example application"
  homepage "https://example.com/app"
` + extra + `
  app "App.app"
end
`
}

func TestReadNamesTheRejectionReason(t *testing.T) {
	sha := strings.Repeat("a", 40)
	history, err := json.Marshal([]any{map[string]any{"sha": sha, "commit": map[string]any{"committer": map[string]any{"date": time.Now().UTC().Add(-time.Hour)}}}})
	require.NoError(t, err)
	flat := "Casks/app.rb"
	for _, tc := range []struct {
		name, cask string
		github     scriptedGitHub
		code       string
		reason     string
		explains   string
	}{
		{name: "not owner/tap/token", cask: "app", code: domain.CodeValidation, reason: "tap_cask_name_invalid:cask", explains: "expected owner/tap/token"},
		{name: "official tap", cask: "homebrew/cask/app", code: domain.CodeValidation, reason: "tap_official_tap:cask", explains: "synced automatically"},
		{name: "missing repository", cask: "owner/tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond("", http.StatusNotFound)}},
			code: domain.CodeValidation, reason: "tap_repository_not_found:owner/homebrew-tap", explains: "owner/homebrew-tap was not found"},
		{name: "rate limited", cask: "owner/tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond("", http.StatusTooManyRequests)}},
			code: domain.CodeUpstreamUnavailable, reason: "tap_github_rate_limited", explains: "rate limit"},
		{name: "server error", cask: "owner/tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond("", http.StatusBadGateway)}},
			code: domain.CodeUpstreamUnavailable, reason: "tap_github_http_error:502", explains: "HTTP 502"},
		{name: "unreachable", cask: "owner/tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: func() ([]byte, int, error) { return nil, 0, errors.New("dial tcp: refused") }}},
			code: domain.CodeUpstreamUnavailable, reason: "tap_github_unavailable", explains: "dial tcp: refused"},
		{name: "unexpected list", cask: "owner/tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond(`{"message":"odd"}`, http.StatusOK)}},
			code: domain.CodeUpstreamUnavailable, reason: "tap_github_response_invalid", explains: "unexpected commit list"},
		{name: "no cask file", cask: "Owner/homebrew-tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond(`[]`, http.StatusOK), "Casks/a/app.rb": respond(`[]`, http.StatusOK)}},
			code: domain.CodeValidation, reason: "tap_cask_not_found:owner/tap/app", explains: "has no cask file"},
		{name: "unsupported stanza", cask: "owner/tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond(string(history), http.StatusOK)},
			files: map[string]func() ([]byte, int, error){sha + "/" + flat: respond(caskSource(`  on_arch_conditional arm: "arm64", intel: "x64"`), http.StatusOK)}},
			code: domain.CodeValidation, reason: "tap_cask_unsupported:line_9", explains: "line 9: `on_arch_conditional`"},
		{name: "file unreadable", cask: "owner/tap/app", github: scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond(string(history), http.StatusOK)},
			files: map[string]func() ([]byte, int, error){sha + "/" + flat: respond("", http.StatusInternalServerError)}},
			code: domain.CodeUpstreamUnavailable, reason: "tap_github_http_error:500", explains: "returned HTTP 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Service{GitHub: tc.github}).Read(context.Background(), tc.cask)
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, tc.code, app.Code)
			require.Equal(t, "rejected: "+tc.reason, app.Msg)
			require.Regexp(t, reasonFormat, app.Msg)
			require.NotContains(t, app.Msg, "//")
			require.Contains(t, Explanation(err), tc.explains, "editors keep the explanation")
		})
	}
	// A cask the reader accepts has no rejection at all.
	accepted := scriptedGitHub{commits: map[string]func() ([]byte, int, error){flat: respond(string(history), http.StatusOK)},
		files: map[string]func() ([]byte, int, error){sha + "/" + flat: respond(caskSource(""), http.StatusOK)}}
	candidate, err := (&Service{GitHub: accepted}).Read(context.Background(), "owner/tap/app")
	require.NoError(t, err)
	require.Equal(t, "owner/tap", candidate.Tap)
}

func TestReadDoubleShotFlightGuard(t *testing.T) {
	// The read path used by onboarding needs no store or live GitHub connection.
	source, err := os.ReadFile("../../homebrew/testdata/casks/double-shot.rb")
	require.NoError(t, err)
	const head = "68fb39413dbaa57e20ffa4c3b977f7ac76b9f53f"
	const path = "Casks/double-shot.rb"
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	history, err := json.Marshal([]any{map[string]any{"sha": head, "commit": map[string]any{"committer": map[string]any{"date": now}}}})
	require.NoError(t, err)
	github := scriptedGitHub{
		commits: map[string]func() ([]byte, int, error){path: respond(string(history), http.StatusOK)},
		files:   map[string]func() ([]byte, int, error){head + "/" + path: respond(string(source), http.StatusOK)},
	}
	svc := &Service{GitHub: github, Now: func() time.Time { return now }}
	candidate, err := svc.Read(context.Background(), "owner/tap/double-shot")
	require.NoError(t, err)
	require.Equal(t, "Double Shot", candidate.Package.Name)
	require.Equal(t, "1.0.6", candidate.Package.Version)
	require.Equal(t, path, candidate.SourcePath)
	require.Equal(t, head, candidate.HeadSHA)
	require.Contains(t, string(candidate.Package.Artifacts), `"postflight_steps"`)
	require.NotContains(t, string(candidate.Package.Artifacts), `"postflight"`)
	require.Len(t, candidate.History, 1)

	// Metadata inside either branch must retain the onboarding validation error.
	unsafe := strings.Replace(string(source), "  else\n", "  else\n    version \"2.0\"\n", 1)
	github.files[head+"/"+path] = respond(unsafe, http.StatusOK)
	_, err = svc.Read(context.Background(), "owner/tap/double-shot")
	var appErr *domain.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, domain.CodeValidation, appErr.Code)
	require.Equal(t, "rejected: tap_cask_unsupported:line_27", appErr.Msg)
}

func TestConflictNamesTheTokenOwner(t *testing.T) {
	removed := time.Now()
	for _, tc := range []struct {
		name   string
		owner  *store.CaskOwner
		reason string
	}{
		{name: "free token"},
		{name: "same tap", owner: &store.CaskOwner{ID: 1, Tap: "owner/tap", Tracked: true}, reason: "tap_cask_already_added:owner/tap/app"},
		{name: "other tap", owner: &store.CaskOwner{ID: 1, Tap: "other/tap", Tracked: true}, reason: "tap_token_taken:app"},
		{name: "official cask", owner: &store.CaskOwner{ID: 1, Tap: "homebrew/cask"}, reason: "tap_token_official:app"},
		{name: "removed official cask", owner: &store.CaskOwner{ID: 1, Tap: "homebrew/cask", RemovedAt: &removed}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Conflict(tc.owner, "owner/tap", "app")
			if tc.reason == "" {
				require.NoError(t, err)
				return
			}
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, domain.CodeConflict, app.Code)
			require.Equal(t, "rejected: "+tc.reason, app.Msg)
			require.Regexp(t, reasonFormat, app.Msg)
		})
	}
	require.Equal(t, "plain failure", Explanation(errors.New("plain failure")))
}
