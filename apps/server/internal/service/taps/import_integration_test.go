//go:build integration

package taps_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// importGitHub serves the repository metadata a whole-tap import reads on top of fakeGitHub's commits and
// files, so one fake describes both the listing and every cask read.
type importGitHub struct {
	*fakeGitHub
	branch      string
	head        string
	paths       []string
	truncated   bool
	missingRepo bool
}

func (g *importGitHub) Get(ctx context.Context, target string, maximum int64, headers http.Header) ([]byte, int, http.Header, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, 0, nil, err
	}
	repository := "/repos/" + g.repository
	if parsed.Host == "api.github.com" && parsed.Path == repository {
		if g.missingRepo {
			return nil, http.StatusNotFound, http.Header{}, nil
		}
		body, _ := json.Marshal(map[string]any{"default_branch": g.branch})
		return body, http.StatusOK, http.Header{}, nil
	}
	if parsed.Host == "api.github.com" && parsed.Path == repository+"/git/ref/heads/"+g.branch {
		body, _ := json.Marshal(map[string]any{"object": map[string]any{"sha": g.head}})
		return body, http.StatusOK, http.Header{}, nil
	}
	if parsed.Host == "api.github.com" && parsed.Path == repository+"/git/trees/"+g.branch {
		tree := make([]any, 0, len(g.paths))
		for _, path := range g.paths {
			tree = append(tree, map[string]any{"path": path, "type": "blob"})
		}
		body, _ := json.Marshal(map[string]any{"truncated": g.truncated, "tree": tree})
		return body, http.StatusOK, http.Header{}, nil
	}
	return g.fakeGitHub.Get(ctx, target, maximum, headers)
}

// importCask builds a minimal but complete cask file for the given token.
func importCask(token, version, extra string) string {
	return `cask "` + token + `" do
  version "` + version + `"
  sha256 "` + strings.Repeat("1", 64) + `"

  url "https://example.com/` + token + `-#{version}.dmg"
  name "` + strings.ToUpper(token[:1]) + token[1:] + `"
  desc "Fixture app for whole-tap import"
  homepage "https://example.com/` + token + `"
` + extra + `
  app "` + token + `.app"
end
`
}

func TestImportWholeTap(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	day := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	github := newFakeGitHub()
	github.repository = "opennavo/homebrew-tap"
	github.commit("Casks/g/ghostty.rb", sha(0), importCask("ghostty", "1.2.0", ""), day)
	github.commit("Casks/b/broken.rb", sha(1), importCask("broken", "1.0.0", "  language :en do\n    name \"Broken\"\n  end\n"), day)
	github.commit("Casks/t/taken.rb", sha(2), importCask("taken", "1.0.0", ""), day)
	github.commit("Casks/n/newapp.rb", sha(3), importCask("newapp", "1.0.0", ""), day)
	// An official cask already owns the `taken` token; the import must not shadow it.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES('cask','taken','taken','homebrew/cask','Taken','1','1','{}','fixture')`).Error)

	gh := &importGitHub{fakeGitHub: github, branch: "main", head: sha(9), paths: []string{
		"Casks/g/ghostty.rb", "Casks/b/broken.rb", "Casks/t/taken.rb", "Casks/n/newapp.rb",
		"README.md", "Casks/notes.txt", "Casks/g/../escape.rb",
	}}
	service := &taps.Service{Store: st, GitHub: gh}

	// A dry run evaluates every cask but writes nothing.
	preview, err := service.Import(ctx, "opennavo/homebrew-tap", taps.ImportOptions{DryRun: true, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, "opennavo/tap", preview.Tap)
	require.Equal(t, "opennavo/homebrew-tap", preview.Repository)
	require.Equal(t, "main", preview.Branch)
	require.Equal(t, sha(9), preview.HeadSHA)
	require.True(t, preview.DryRun)
	require.Equal(t, 4, preview.Listed)
	require.Equal(t, []string{"ghostty"}, preview.Added)
	require.Equal(t, []string{"broken"}, []string{preview.Failed[0].Token})
	var count int
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM packages WHERE tap='opennavo/tap'`).Scan(&count).Error)
	require.Zero(t, count)

	// The real import adds one cask, refuses the unsupported file and the official token.
	result, err := service.Import(ctx, "opennavo/tap", taps.ImportOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"ghostty", "newapp"}, result.Added)
	require.Empty(t, result.Existing)
	require.Len(t, result.Failed, 1)
	require.Equal(t, "broken", result.Failed[0].Token)
	require.Contains(t, result.Failed[0].Reason, "cask_unsupported")
	require.Contains(t, result.Failed[0].Message, "not supported")
	require.Len(t, result.Skipped, 1)
	require.Equal(t, "taken", result.Skipped[0].Token)
	require.Contains(t, result.Skipped[0].Reason, "token_official")

	var tap, headSHA string
	require.NoError(t, st.DB.Raw(`SELECT p.tap,t.head_sha FROM packages p JOIN tap_casks t ON t.package_id=p.id WHERE p.token='ghostty'`).Row().Scan(&tap, &headSHA))
	require.Equal(t, "opennavo/tap", tap)
	require.Equal(t, sha(0), headSHA)
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM packages WHERE token='broken'`).Scan(&count).Error)
	require.Zero(t, count)

	// Re-running only reports what is already tracked; it never duplicates or rewrites.
	again, err := service.Import(ctx, "opennavo/tap", taps.ImportOptions{})
	require.NoError(t, err)
	require.Empty(t, again.Added)
	require.Equal(t, []string{"ghostty", "newapp"}, again.Existing)

	// A token filter narrows the run and rejects tokens the repository does not list.
	filtered, err := service.Import(ctx, "opennavo/tap", taps.ImportOptions{Tokens: []string{"GHOSTTY"}})
	require.NoError(t, err)
	require.Equal(t, []string{"ghostty"}, filtered.Existing)
	_, err = service.Import(ctx, "opennavo/tap", taps.ImportOptions{Tokens: []string{"absent"}})
	require.ErrorContains(t, err, "cask_not_found:absent")
}

func TestImportRejectsUnusableRepository(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	github := newFakeGitHub()
	github.repository = "opennavo/homebrew-tap"

	missing := &importGitHub{fakeGitHub: github, branch: "main", head: sha(0), missingRepo: true}
	_, err := (&taps.Service{Store: st, GitHub: missing}).Import(ctx, "opennavo/tap", taps.ImportOptions{})
	require.ErrorContains(t, err, "repository_not_found")
	require.Equal(t, domain.CodeValidation, appCode(t, err), "a missing repository is a validation failure, not an upstream one")

	truncated := &importGitHub{fakeGitHub: github, branch: "main", head: sha(0), paths: []string{"Casks/a/a.rb"}, truncated: true}
	_, err = (&taps.Service{Store: st, GitHub: truncated}).Import(ctx, "opennavo/tap", taps.ImportOptions{})
	require.ErrorContains(t, err, "tree_truncated")

	// Official taps are never imported; the official sync owns them.
	_, err = (&taps.Service{Store: st, GitHub: github}).Import(ctx, "homebrew/cask", taps.ImportOptions{})
	require.ErrorContains(t, err, "tap_name_invalid")
}
