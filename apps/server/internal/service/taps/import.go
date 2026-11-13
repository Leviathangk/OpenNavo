package taps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// ImportOptions bounds one whole-tap import. Zero values import every cask in the repository.
type ImportOptions struct {
	DryRun bool
	Limit  int      // stop after this many cask reads; zero reads all of them
	Tokens []string // import only these tokens; empty imports every cask the repository lists
}

// ImportIssue is one cask the import did not add: the machine reason mirrors tap error messages and
// message is the editor's explanation (an unsupported stanza with its line, a taken token, and so on).
type ImportIssue struct {
	Token   string `json:"token"`
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// ImportResult reports one import. Added and Existing are the catalog outcomes, Skipped holds casks a
// rule refuses (official token, another tap owns it) and Failed holds reads that broke.
type ImportResult struct {
	Tap        string        `json:"tap"`
	Repository string        `json:"repository"`
	Branch     string        `json:"branch"`
	HeadSHA    string        `json:"headSha"`
	DryRun     bool          `json:"dryRun"`
	Listed     int           `json:"listed"`
	Added      []string      `json:"added"`
	Existing   []string      `json:"existing"`
	Skipped    []ImportIssue `json:"skipped"`
	Failed     []ImportIssue `json:"failed"`
}

var (
	errAlreadyTracked = errors.New("the cask is already tracked by this tap")
	// treeLimit bounds GitHub's recursive tree response; a bigger repository is refused instead of truncated.
	treeLimit = int64(16 << 20)
)

// Import registers every cask a tap repository lists, as if an editor had added each one. It is
// idempotent: casks already tracked land in Existing, so re-running after a merge only adds the new
// ones. Casks are read without their history to keep the request budget proportional to the cask
// count; the first tap sync backfills history_sha.
func (s *Service) Import(ctx context.Context, tap string, opts ImportOptions) (*ImportResult, error) {
	tap, err := homebrew.ParseTap(tap)
	if err != nil {
		return nil, invalid("tap_name_invalid", "%s: %v", tap, err)
	}
	result := &ImportResult{Tap: tap, Repository: homebrew.TapRepository(tap), DryRun: opts.DryRun,
		Added: []string{}, Existing: []string{}, Skipped: []ImportIssue{}, Failed: []ImportIssue{}}
	if result.Branch, err = s.defaultBranch(ctx, tap); err != nil {
		return nil, err
	}
	if result.HeadSHA, err = s.branchHead(ctx, tap, result.Branch); err != nil {
		return nil, err
	}
	listed, err := s.caskPaths(ctx, tap, result.Branch)
	if err != nil {
		return nil, err
	}
	tokens, err := importSelection(listed, opts.Tokens)
	if err != nil {
		return nil, err
	}
	if opts.Limit > 0 && len(tokens) > opts.Limit {
		tokens = tokens[:opts.Limit]
	}
	result.Listed = len(listed)
	for _, token := range tokens {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		outcome, issue, err := s.importCask(ctx, tap, token, opts.DryRun)
		switch {
		case err != nil:
			result.Failed = append(result.Failed, issue)
		case outcome == "added":
			result.Added = append(result.Added, token)
		case outcome == "existing":
			result.Existing = append(result.Existing, token)
		default:
			result.Skipped = append(result.Skipped, issue)
		}
	}
	return result, nil
}

// importSelection narrows the listed tokens to the requested ones, sorted and deduplicated. A requested
// token the repository does not list is a usage error: the caller asked for a file that is not there.
func importSelection(listed []string, want []string) ([]string, error) {
	available := make(map[string]bool, len(listed))
	for _, token := range listed {
		available[token] = true
	}
	if len(want) == 0 {
		return listed, nil
	}
	seen, out := map[string]bool{}, make([]string, 0, len(want))
	for _, raw := range want {
		token := strings.ToLower(strings.TrimSpace(raw))
		if _, _, err := homebrew.ParseTapCask("tap/tap/" + token); err != nil || !available[token] {
			return nil, invalid("cask_not_found:"+token, "%s is not a cask file in this tap", token)
		}
		if !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}
	sort.Strings(out)
	return out, nil
}

// importCask adds one cask, returning "added", "existing", or a skipped issue. Failures (an unreadable
// or unsupported file, a database error) come back as the error with its issue.
func (s *Service) importCask(ctx context.Context, tap, token string, dryRun bool) (string, ImportIssue, error) {
	issue := ImportIssue{Token: token}
	fullToken := tap + "/" + token
	tracked, err := s.Store.TrackedCaskOwner(ctx, fullToken)
	if err != nil {
		issue.Reason, issue.Message = "database_error", err.Error()
		return "", issue, err
	}
	if tracked != nil {
		return "existing", issue, nil
	}
	candidate, err := s.read(ctx, fullToken, false)
	if err != nil {
		issue.Reason, issue.Message = importReason(err)
		return "", issue, err
	}
	owner, err := s.Store.CaskOwner(ctx, candidate.Package.Token)
	if err != nil {
		issue.Reason, issue.Message = "database_error", err.Error()
		return "", issue, err
	}
	if err := Conflict(owner, candidate.Tap, candidate.Package.Token); err != nil {
		issue.Reason, issue.Message = importReason(err)
		return "skipped", issue, nil
	}
	if dryRun {
		return "added", issue, nil
	}
	if err := s.registerImported(ctx, candidate); err != nil {
		if errors.Is(err, errAlreadyTracked) {
			return "existing", issue, nil
		}
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			issue.Reason, issue.Message = importReason(err)
			return "skipped", issue, nil
		}
		issue.Reason, issue.Message = "database_error", err.Error()
		return "", issue, err
	}
	return "added", issue, nil
}

// registerImported writes the catalog row and its registration in one transaction, re-checking ownership
// under the catalog lock because another import or the official sync may have taken the token meanwhile.
func (s *Service) registerImported(ctx context.Context, candidate *Candidate) error {
	return s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		tracked, err := st.TrackedCaskOwner(ctx, candidate.Package.FullToken)
		if err != nil {
			return err
		}
		if tracked != nil {
			return errAlreadyTracked
		}
		owner, err := st.CaskOwner(ctx, candidate.Package.Token)
		if err != nil {
			return err
		}
		if err := Conflict(owner, candidate.Tap, candidate.Package.Token); err != nil {
			return err
		}
		now := s.now()
		id, err := Write(ctx, st, candidate, now)
		if err != nil {
			return err
		}
		// An empty historySHA leaves the backfill to the first tap sync, which reads it per changed cask.
		return st.RegisterTapCask(ctx, id, candidate.Tap, candidate.SourcePath, candidate.ETag, candidate.HeadSHA, "", now)
	})
}

// importReason splits a tap error into its machine reason ("cask_unsupported:line_12") and its message.
func importReason(err error) (string, string) {
	reason := "error"
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		reason = strings.TrimPrefix(appErr.Msg, "rejected: ")
		if reason == "" {
			reason = "error"
		}
	}
	return reason, Explanation(err)
}

// defaultBranch reads the repository's default branch, which is also how a missing repository surfaces.
func (s *Service) defaultBranch(ctx context.Context, tap string) (string, error) {
	repository := homebrew.TapRepository(tap)
	body, status, _, err := s.GitHub.Get(ctx, s.apiBase()+"/repos/"+repository, 1<<20, nil)
	if err != nil {
		return "", upstream("github_unavailable", "GitHub request failed: %v", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", invalid("repository_not_found:"+repository, "GitHub repository %s was not found", repository)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "", upstream("github_rate_limited", "GitHub rate limit reached; try again later")
	default:
		return "", upstream("github_http_error:"+fmt.Sprint(status), "GitHub returned HTTP %d", status)
	}
	var repositoryInfo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &repositoryInfo); err != nil || repositoryInfo.DefaultBranch == "" {
		return "", upstream("github_response_invalid", "GitHub returned an unexpected repository")
	}
	return repositoryInfo.DefaultBranch, nil
}

// branchHead reads the commit the branch points at, recorded in the result so an import can be traced
// back to the repository state it read.
func (s *Service) branchHead(ctx context.Context, tap, branch string) (string, error) {
	target := s.apiBase() + "/repos/" + homebrew.TapRepository(tap) + "/git/ref/heads/" + url.PathEscape(branch)
	body, status, _, err := s.GitHub.Get(ctx, target, 1<<20, nil)
	if err != nil {
		return "", upstream("github_unavailable", "GitHub request failed: %v", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", upstream("github_branch_missing:"+branch, "branch %s was not found", branch)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "", upstream("github_rate_limited", "GitHub rate limit reached; try again later")
	default:
		return "", upstream("github_http_error:"+fmt.Sprint(status), "GitHub returned HTTP %d", status)
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(body, &ref); err != nil || len(ref.Object.SHA) != 40 {
		return "", upstream("github_response_invalid", "GitHub returned an unexpected branch")
	}
	return ref.Object.SHA, nil
}

// caskPaths lists the cask files the repository holds at branch, sorted by token. GitHub truncates a
// recursive tree for repositories above about 100k files; that answer is refused rather than imported
// partially, because a partial import would look like a successful one.
func (s *Service) caskPaths(ctx context.Context, tap, branch string) ([]string, error) {
	repository := homebrew.TapRepository(tap)
	target := s.apiBase() + "/repos/" + repository + "/git/trees/" + url.PathEscape(branch) + "?recursive=1"
	body, status, _, err := s.GitHub.Get(ctx, target, treeLimit, nil)
	if err != nil {
		return nil, upstream("github_unavailable", "GitHub request failed: %v", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, invalid("repository_not_found:"+repository, "GitHub repository %s has no branch %s", repository, branch)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, upstream("github_rate_limited", "GitHub rate limit reached; try again later")
	default:
		return nil, upstream("github_http_error:"+fmt.Sprint(status), "GitHub returned HTTP %d", status)
	}
	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, upstream("github_response_invalid", "GitHub returned an unexpected tree")
	}
	if tree.Truncated {
		return nil, upstream("github_tree_truncated:"+repository, "GitHub truncated the tree of %s; import the remaining casks with -token", repository)
	}
	seen := map[string]bool{}
	tokens := make([]string, 0, len(tree.Tree))
	for _, entry := range tree.Tree {
		if entry.Type != "blob" {
			continue
		}
		token, ok := homebrew.TapCaskTokenFromPath(entry.Path)
		if !ok || seen[token] {
			continue
		}
		seen[token] = true
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens, nil
}
