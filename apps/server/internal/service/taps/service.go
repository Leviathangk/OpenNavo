// Package taps tracks casks from third-party Homebrew taps that editors add explicitly. The cask file
// is read from the tap's GitHub repository and evaluated statically (homebrew.ParseCaskSource), then
// written to the catalog like an official cask.
package taps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// GitHub is changelog.Resolver.Get: it adds the configured token for api.github.com only, observes the
// quota and never follows redirects.
type GitHub interface {
	Get(ctx context.Context, target string, maximum int64, headers http.Header) ([]byte, int, http.Header, error)
}

type Service struct {
	Store   *store.Store
	GitHub  GitHub
	Now     func() time.Time
	APIBase string // https://api.github.com
	RawBase string // https://raw.githubusercontent.com
}

const caskFileLimit = 256 << 10

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) apiBase() string {
	if s.APIBase != "" {
		return strings.TrimRight(s.APIBase, "/")
	}
	return "https://api.github.com"
}

func (s *Service) rawBase() string {
	if s.RawBase != "" {
		return strings.TrimRight(s.RawBase, "/")
	}
	return "https://raw.githubusercontent.com"
}

// Candidate is a tap cask read from GitHub and normalized, ready to be written to the catalog.
// History holds the versions its file's recent commits declared, read up to HistorySHA; an empty
// HistorySHA means the read failed and the tap sync reads it later.
type Candidate struct {
	Tap, Token, SourcePath string
	ETag, HeadSHA          string
	CommittedAt            time.Time
	Package                homebrew.Package
	History                []store.TapVersion
	HistorySHA             string
}

func invalid(format string, args ...any) error {
	return &domain.AppError{Code: domain.CodeValidation, HTTPStatus: http.StatusBadRequest, Msg: fmt.Sprintf(format, args...)}
}

func upstream(format string, args ...any) error {
	return &domain.AppError{Code: domain.CodeUpstreamUnavailable, HTTPStatus: http.StatusBadGateway, Msg: fmt.Sprintf(format, args...)}
}

type commit struct {
	SHA, ETag string
	Date      time.Time
	Missing   bool // the path has no history on the default branch
}

// historyLimit bounds a history read to one page of GitHub's commit list; historyReaders bounds the
// file reads in flight.
const (
	historyLimit   = 100
	historyReaders = 8
)

// commits lists the newest default-branch commits touching path, newest first. A 304 returns notModified.
func (s *Service) commits(ctx context.Context, tap, path, etag string, limit int) ([]commit, string, bool, error) {
	target := s.apiBase() + "/repos/" + homebrew.TapRepository(tap) + "/commits?" + url.Values{"path": {path}, "per_page": {strconv.Itoa(limit)}}.Encode()
	headers := http.Header{}
	if etag != "" {
		headers.Set("If-None-Match", etag)
	}
	body, status, responseHeaders, err := s.GitHub.Get(ctx, target, 8<<20, headers)
	if err != nil {
		return nil, "", false, upstream("GitHub request failed: %v", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotModified:
		return nil, etag, true, nil
	case http.StatusNotFound:
		return nil, "", false, invalid("GitHub repository %s was not found", homebrew.TapRepository(tap))
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, "", false, upstream("GitHub rate limit reached; try again later")
	default:
		return nil, "", false, upstream("GitHub returned HTTP %d", status)
	}
	var entries []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, "", false, upstream("GitHub returned an unexpected commit list")
	}
	out := make([]commit, 0, len(entries))
	for _, entry := range entries {
		if len(entry.SHA) != 40 {
			return nil, "", false, upstream("GitHub returned an unexpected commit list")
		}
		out = append(out, commit{SHA: entry.SHA, Date: entry.Commit.Committer.Date})
	}
	return out, responseHeaders.Get("ETag"), false, nil
}

// lastCommit returns the newest default-branch commit touching path. A 304 returns a zero commit.
func (s *Service) lastCommit(ctx context.Context, tap, path, etag string) (commit, bool, error) {
	list, etag, notModified, err := s.commits(ctx, tap, path, etag, 1)
	if err != nil || notModified {
		return commit{ETag: etag}, notModified, err
	}
	if len(list) == 0 {
		return commit{ETag: etag, Missing: true}, false, nil
	}
	head := list[0]
	head.ETag = etag
	return head, false, nil
}

// history reads the versions the cask file at path declared in its newest commits, stopping before stop,
// the newest commit read last time. known holds versions already read (the head's). Each version comes
// with the oldest commit in this window that declared it, oldest first. Revisions the full reader would
// reject still count, since only their version stanza is read.
func (s *Service) history(ctx context.Context, tap, path, stop string, known map[string]string) ([]store.TapVersion, error) {
	list, _, _, err := s.commits(ctx, tap, path, "", historyLimit)
	if err != nil {
		return nil, err
	}
	for i, entry := range list {
		if entry.SHA == stop {
			list = list[:i]
			break
		}
	}
	versions, failures := make([]string, len(list)), make([]error, len(list))
	slots := make(chan struct{}, historyReaders)
	var readers sync.WaitGroup
	for i, entry := range list {
		if version, ok := known[entry.SHA]; ok {
			versions[i] = version
			continue
		}
		readers.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			source, err := s.readFile(ctx, tap, entry.SHA, path)
			if errors.Is(err, errDeleted) {
				return // this commit removed the file
			}
			versions[i], failures[i] = homebrew.CaskSourceVersion(source), err
		})
	}
	readers.Wait()
	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}
	out, seen := []store.TapVersion{}, map[string]bool{}
	for i := len(list) - 1; i >= 0; i-- {
		if version := versions[i]; version != "" && version != "latest" && !seen[version] {
			seen[version] = true
			out = append(out, store.TapVersion{Version: version, SHA: list[i].SHA, CommittedAt: list[i].Date})
		}
	}
	return out, nil
}

var errDeleted = errors.New("the cask file was deleted from the tap")

func (s *Service) readFile(ctx context.Context, tap, sha, path string) (string, error) {
	body, status, _, err := s.GitHub.Get(ctx, s.rawBase()+"/"+homebrew.TapRepository(tap)+"/"+sha+"/"+path, caskFileLimit, nil)
	if err != nil {
		return "", upstream("reading %s failed: %v", path, err)
	}
	switch status {
	case http.StatusOK:
		return string(body), nil
	case http.StatusNotFound:
		return "", errDeleted
	}
	return "", upstream("reading %s returned HTTP %d", path, status)
}

// Read locates a cask in its tap, reads it at the newest commit and evaluates it. Errors carry
// messages meant for the editor (unsupported DSL with its line, missing repository or file).
func (s *Service) Read(ctx context.Context, fullToken string) (*Candidate, error) {
	tap, token, err := homebrew.ParseTapCask(fullToken)
	if err != nil {
		return nil, invalid("%s: %v", fullToken, err)
	}
	for _, path := range homebrew.TapCaskPaths(token) {
		head, _, err := s.lastCommit(ctx, tap, path, "")
		if err != nil {
			return nil, err
		}
		if head.Missing {
			continue
		}
		candidate, err := s.evaluate(ctx, tap, token, path, head)
		if errors.Is(err, errDeleted) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Older versions let editors write notes for them right away. A failed read is not fatal.
		if versions, err := s.history(ctx, tap, path, "", map[string]string{head.SHA: candidate.Package.Version}); err == nil {
			candidate.History, candidate.HistorySHA = versions, head.SHA
		}
		return candidate, nil
	}
	return nil, invalid("%s has no cask file %s in %s", fullToken, strings.Join(homebrew.TapCaskPaths(token), " or "), homebrew.TapRepository(tap))
}

func (s *Service) evaluate(ctx context.Context, tap, token, path string, head commit) (*Candidate, error) {
	source, err := s.readFile(ctx, tap, head.SHA, path)
	if err != nil {
		return nil, err
	}
	raw, err := homebrew.ParseCaskSource(source, homebrew.CaskSource{Tap: tap, Token: token, Path: path, GitHead: head.SHA}, s.now())
	if err != nil {
		var unsupported *homebrew.UnsupportedCaskError
		if errors.As(err, &unsupported) {
			return nil, invalid("%s/%s: %s", tap, token, unsupported.Error())
		}
		return nil, err
	}
	item, err := homebrew.Normalize("cask", raw)
	if err != nil {
		return nil, invalid("%s/%s: %v", tap, token, err)
	}
	return &Candidate{Tap: tap, Token: token, SourcePath: path, ETag: head.ETag, HeadSHA: head.SHA, CommittedAt: head.Date, Package: item}, nil
}

// Conflict explains why a token cannot be added for tap, or returns nil when it can.
func Conflict(owner *store.CaskOwner, tap string) error {
	switch {
	case owner == nil:
		return nil
	case owner.Tracked && owner.Tap == tap:
		return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict, Msg: "this cask was already added; package_refresh reads it again"}
	case owner.Tracked:
		return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict, Msg: "the token is already used by a cask from " + owner.Tap}
	case homebrew.IsOfficialTap(owner.Tap) && owner.RemovedAt == nil:
		return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict, Msg: "an official Homebrew cask already uses this token"}
	}
	// A removed official cask, or a tap cask that was removed earlier, is revived with the tap's data.
	return nil
}

// Write stores a candidate in the catalog inside st's transaction and returns the package ID.
func Write(ctx context.Context, st *store.Store, candidate *Candidate, now time.Time) (int64, error) {
	previous, err := st.CatalogRow(ctx, "cask", candidate.Token)
	if err != nil {
		return 0, err
	}
	if _, err := st.ApplyCatalogBatchWithLifecycle(ctx, []store.CatalogMutation{{Package: candidate.Package, Previous: previous}}, now); err != nil {
		return 0, err
	}
	row, err := st.CatalogRow(ctx, "cask", candidate.Token)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, errors.New("tap cask was not written")
	}
	if candidate.HeadSHA != "" && !candidate.CommittedAt.IsZero() {
		if err := st.RecordTapVersionCommit(ctx, row.ID, candidate.Package.Version, candidate.HeadSHA, candidate.CommittedAt); err != nil {
			return 0, err
		}
	}
	return row.ID, nil
}

// Run reads every tracked tap cask again (or one package) and writes the ones whose file changed.
func (s *Service) Run(ctx context.Context, packageID int64) (map[string]any, error) {
	stats := map[string]any{"checked": 0, "updated": 0, "unchanged": 0, "failed": 0, "removed": 0, "versions": 0}
	count := func(key string) { stats[key] = stats[key].(int) + 1 }
	err := s.Store.WithAdvisoryLock(ctx, "taps:sync", func(ctx context.Context) error {
		casks, err := s.Store.TapCasks(ctx)
		if err != nil {
			return err
		}
		for _, cask := range casks {
			if packageID > 0 && cask.PackageID != packageID {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			count("checked")
			outcome, versions, err := s.refresh(ctx, cask)
			stats["versions"] = stats["versions"].(int) + versions
			if err != nil {
				count("failed")
				message := err.Error()
				var appErr *domain.AppError
				if errors.As(err, &appErr) && appErr.Msg != "" {
					message = appErr.Msg
				}
				if recordErr := s.recordFailure(ctx, cask, message); recordErr != nil {
					return recordErr
				}
				continue
			}
			count(outcome)
		}
		return nil
	})
	return stats, err
}

func (s *Service) refresh(ctx context.Context, cask store.TapCask) (string, int, error) {
	if cask.PackageTap != cask.Tap {
		// Homebrew adopted the token in homebrew/cask; the official sync owns the package now.
		return "", 0, fmt.Errorf("the catalog now lists this token from %s; remove the tap cask", cask.PackageTap)
	}
	current := cask.NormalizationVersion == fmt.Sprint(homebrew.CaskNormalizationVersion)
	etag := cask.ETag
	if !current {
		etag = ""
	}
	head, notModified, err := s.lastCommit(ctx, cask.Tap, cask.SourcePath, etag)
	if err != nil {
		return "", 0, err
	}
	// Lifecycle dates can become active even when GitHub's content has not changed.
	today := s.now().Format("2006-01-02")
	due := (!cask.Disabled && cask.DisableDate != "" && cask.DisableDate <= today) ||
		(!cask.Deprecated && cask.DeprecationDate != "" && cask.DeprecationDate <= today)
	if notModified {
		head.SHA, head.ETag = cask.HeadSHA, cask.ETag
	}
	if head.Missing {
		return s.refreshMoved(ctx, cask, head)
	}
	if (notModified || head.SHA == cask.HeadSHA) && current && cask.RemovedAt == nil && !due {
		return s.finishRefresh(ctx, cask, nil, head, false, s.readHistory(ctx, cask, cask.SourcePath, head, nil))
	}
	candidate, err := s.evaluate(ctx, cask.Tap, cask.Token, cask.SourcePath, head)
	if errors.Is(err, errDeleted) {
		return s.refreshMoved(ctx, cask, head)
	}
	if err != nil {
		return "", 0, err
	}
	return s.finishRefresh(ctx, cask, candidate, head, false, s.readHistory(ctx, cask, cask.SourcePath, head, candidate))
}

// tapHistory is one read of a cask file's history, up to the commit SHA.
type tapHistory struct {
	SHA      string
	Versions []store.TapVersion
}

// readHistory reads the commits after the newest one read before, when head is newer. It returns nil
// when there is nothing new or the read failed; history_sha then stays behind and the next sync retries.
func (s *Service) readHistory(ctx context.Context, cask store.TapCask, path string, head commit, candidate *Candidate) *tapHistory {
	if head.SHA == "" || head.SHA == cask.HistorySHA {
		return nil
	}
	known := map[string]string{}
	if candidate != nil {
		known[head.SHA] = candidate.Package.Version
	}
	versions, err := s.history(ctx, cask.Tap, path, cask.HistorySHA, known)
	if err != nil {
		return nil
	}
	return &tapHistory{SHA: head.SHA, Versions: versions}
}

// A deleted path can be a move between Homebrew's supported layouts. Only remove the
// package after all alternate paths have been checked; upstream/parse failures keep it intact.
func (s *Service) refreshMoved(ctx context.Context, cask store.TapCask, oldHead commit) (string, int, error) {
	for _, path := range homebrew.TapCaskPaths(cask.Token) {
		if path == cask.SourcePath {
			continue
		}
		head, _, err := s.lastCommit(ctx, cask.Tap, path, "")
		if err != nil {
			return "", 0, err
		}
		if head.Missing {
			continue
		}
		candidate, err := s.evaluate(ctx, cask.Tap, cask.Token, path, head)
		if errors.Is(err, errDeleted) {
			continue
		}
		if err != nil {
			return "", 0, err
		}
		return s.finishRefresh(ctx, cask, candidate, head, false, s.readHistory(ctx, cask, path, head, candidate))
	}
	return s.finishRefresh(ctx, cask, nil, oldHead, true, nil)
}

// Network reads happen outside the transaction; every result must revalidate ownership under the
// same catalog lock used by registration, removal, and the official sync.
func (s *Service) finishRefresh(ctx context.Context, cask store.TapCask, candidate *Candidate, head commit, deleted bool, history *tapHistory) (string, int, error) {
	outcome, versions := "unchanged", 0
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		live, err := st.TapCask(ctx, cask.PackageID)
		if err != nil {
			return err
		}
		if !sameRegistration(live, cask) || live.PackageTap != cask.Tap {
			return nil
		}
		message := ""
		if deleted {
			if live.RemovedAt == nil {
				if err := st.RemoveCatalogRows(ctx, []*store.CatalogRow{{ID: live.PackageID, Kind: "cask", Token: live.Token, Version: live.Version}}); err != nil {
					return err
				}
			}
			outcome, message = "removed", errDeleted.Error()
		} else if candidate != nil && (candidate.Package.RawHash != live.RawHash || live.RemovedAt != nil ||
			live.NormalizationVersion != fmt.Sprint(homebrew.CaskNormalizationVersion)) {
			if _, err := Write(ctx, st, candidate, s.now()); err != nil {
				return err
			}
			outcome = "updated"
		}
		if history != nil && !deleted {
			if versions, err = st.RecordTapVersions(ctx, cask.PackageID, history.Versions, s.now()); err != nil {
				return err
			}
			if err := st.RecordTapHistory(ctx, cask.PackageID, history.SHA); err != nil {
				return err
			}
		}
		path := cask.SourcePath
		if candidate != nil {
			path = candidate.SourcePath
		}
		return st.RecordTapCheck(ctx, cask.PackageID, path, head.ETag, head.SHA, message, s.now())
	})
	return outcome, versions, err
}

// A package ID is reused after removal. Compare the registration generation as well as its source.
func sameRegistration(live *store.TapCask, previous store.TapCask) bool {
	return live != nil && live.Tap == previous.Tap && live.PackageTap == previous.PackageTap &&
		live.SourcePath == previous.SourcePath && live.CreatedAt.Equal(previous.CreatedAt)
}

func (s *Service) recordFailure(ctx context.Context, cask store.TapCask, message string) error {
	return s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		live, err := st.TapCask(ctx, cask.PackageID)
		if err != nil {
			return err
		}
		if !sameRegistration(live, cask) {
			return nil
		}
		// Failure carries no new source metadata; retain the live registration's values.
		return st.RecordTapCheck(ctx, live.PackageID, live.SourcePath, live.ETag, live.HeadSHA, message, s.now())
	})
}
