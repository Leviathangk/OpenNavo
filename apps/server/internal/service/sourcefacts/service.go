// Package sourcefacts runs the hourly facts:sources job: for each app it finds the GitHub
// repository and bundle identifiers in the Homebrew definition (rules in internal/sourcefacts),
// asks GitHub for repository signals and the 30-day OSI license decision, and asks Apple's lookup
// API for a matching Mac App Store listing. One run stays inside a time budget, leaves GitHub
// quota for the changelog pipeline and paces App Store lookups; whatever is left waits for the
// next run.
package sourcefacts

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"time"

	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/store"
)

const (
	GitHub   = "github"
	AppStore = "app_store"

	recheck       = 30 * 24 * time.Hour
	signalRecheck = 24 * time.Hour
	priceRecheck  = 24 * time.Hour
	maxBackoff    = 7 * 24 * time.Hour
)

type GitHubSource interface {
	Repo(ctx context.Context, repo, etag string) (sources.RepoResult, error)
}
type AppStoreSource interface {
	Lookup(ctx context.Context, ids []string) (map[string]sources.Listing, error)
}

type Service struct {
	NVD      NVDSource
	Store    *store.Store
	GitHub   GitHubSource
	AppStore AppStoreSource
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration) error
	Logger   *slog.Logger
	// Budget bounds one run (default 10 minutes; the job timeout is 20). GitHub gets the first half.
	Budget time.Duration
	// GitHubPause and AppStorePause space requests; Apple allows roughly 20 lookups a minute.
	GitHubPause, AppStorePause time.Duration
}

type pending struct {
	pkg         store.SourceFactPackage
	input       string
	items       []string
	row         *store.SourceFact
	signalsOnly bool
}

type Stats struct {
	Due               map[string]int    `json:"due"`
	Checked           map[string]int    `json:"checked"`
	Found             map[string]int    `json:"found"`
	Failed            map[string]int    `json:"failed"`
	Changed           int               `json:"changed"`
	Removed           int               `json:"removed"`
	Lookups           int               `json:"lookups"`
	GitHubRequests    int               `json:"githubRequests"`
	GitHubNotModified int               `json:"githubNotModified"`
	GitHubRemaining   int64             `json:"githubRemaining"`
	Stopped           map[string]string `json:"stopped,omitempty"`
	Reserved          bool              `json:"githubQuotaReserved,omitempty"`
}

func (s *Service) Run(ctx context.Context) (map[string]any, error) {
	start := s.now()
	stats := &Stats{Due: map[string]int{}, Checked: map[string]int{}, Found: map[string]int{}, Failed: map[string]int{}, Stopped: map[string]string{}, GitHubRemaining: -1}
	defer func() {
		s.logger().InfoContext(ctx, "source facts GitHub refresh", "requests", stats.GitHubRequests, "notModified", stats.GitHubNotModified, "remaining", stats.GitHubRemaining)
	}()
	packages, err := s.Store.SourceFactPackages(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.SourceFacts(ctx)
	if err != nil {
		return nil, err
	}
	queues := map[string][]pending{}
	for _, p := range packages {
		for _, source := range []string{GitHub, AppStore} {
			items := candidates(source, p)
			input := strings.Join(items, " ")
			key := store.SourceFactKey{PackageID: p.ID, Source: source}
			row, exists := rows[key]
			if input == "" {
				if exists {
					if err := s.Store.DeleteSourceFact(ctx, key, p.Token, publicSourceFact(&row)); err != nil {
						return nil, err
					}
					stats.Removed++
				}
				continue
			}
			signalsOnly := source == GitHub && exists && row.Input == input && row.Subject != "" && row.FailCount == 0 && githubSignalsDue(row, start) && row.NextCheckAt.After(start)
			if exists && row.Input == input && row.NextCheckAt.After(start) && !signalsOnly {
				continue
			}
			if signalsOnly {
				// The stored subject is the repository whose ETag was checked. A daily
				// refresh must spend one request on that repository only.
				items = []string{row.Subject}
			}
			item := pending{pkg: p, input: input, items: items, signalsOnly: signalsOnly}
			if exists {
				item.row = &row
			}
			queues[source] = append(queues[source], item)
		}
	}
	stats.Due[GitHub], stats.Due[AppStore] = len(queues[GitHub]), len(queues[AppStore])
	budget := s.Budget
	if budget <= 0 {
		budget = 10 * time.Minute
	}
	if err := s.runGitHub(ctx, queues[GitHub], start.Add(budget/2), stats); err != nil {
		return nil, err
	}
	if err := s.runAppStore(ctx, queues[AppStore], start.Add(budget), stats); err != nil {
		return nil, err
	}
	if err := s.runNVD(ctx, start.Add(budget)); err != nil {
		return nil, err
	}
	var out map[string]any
	encoded, err := json.Marshal(stats)
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(encoded, &out)
}

func candidates(source string, p store.SourceFactPackage) []string {
	if source == GitHub {
		return sources.GitHubRepos(text(p.Homepage), text(p.DownloadURL), text(p.RepoURL))
	}
	if len(p.BundleIDs) > 0 {
		return p.BundleIDs[:min(4, len(p.BundleIDs))]
	}
	return sources.BundleIDs(p.Artifacts)
}

func (s *Service) runGitHub(ctx context.Context, queue []pending, deadline time.Time, stats *Stats) error {
	for index, item := range queue {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.now().After(deadline) {
			stats.Stopped[GitHub] = "budget"
			return nil
		}
		if index > 0 {
			if err := s.sleep(ctx, s.GitHubPause); err != nil {
				return err
			}
		}
		fact, low, err := s.checkGitHub(ctx, item, stats)
		if errors.Is(err, sources.ErrRateLimited) {
			stats.Stopped[GitHub] = "rate_limited"
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			stats.Failed[GitHub]++
			if item.signalsOnly {
				if err := s.failSignals(ctx, item); err != nil {
					return err
				}
			} else if err := s.fail(ctx, item, GitHub); err != nil {
				return err
			}
		} else if err := s.save(ctx, item, fact, stats); err != nil {
			return err
		}
		if low {
			stats.Stopped[GitHub], stats.Reserved = "quota", true
			return nil
		}
	}
	return nil
}

// checkGitHub tries the repositories in order until one publishes a license. A 304 for the
// repository already found keeps it without spending quota. low reports that the quota is down
// to its last tenth, which the changelog pipeline keeps; the caller stops after this app.
func (s *Service) checkGitHub(ctx context.Context, item pending, stats *Stats) (store.SourceFact, bool, error) {
	now := s.now()
	fullCheck := !item.signalsOnly
	fact := store.SourceFact{PackageID: item.pkg.ID, Source: GitHub, Input: item.input, Status: "none", CheckedAt: now, NextCheckAt: next(now, recheck, item.pkg.ID)}
	if item.signalsOnly && item.row != nil {
		fact = *item.row
		fact.Input = item.input
	}
	low := false
	var first sources.GitHubFact
	var firstETag string
	haveFirst := false
	for _, repo := range item.items {
		etag := ""
		if item.row != nil && item.row.ETag != nil && strings.EqualFold(item.row.Subject, repo) {
			etag = *item.row.ETag
		}
		result, err := s.GitHub.Repo(ctx, repo, etag)
		requests := result.Requests
		if requests <= 0 {
			requests = 1
		}
		stats.GitHubRequests += requests
		if result.NotModified {
			stats.GitHubNotModified++
		}
		if result.Remaining >= 0 && (stats.GitHubRemaining < 0 || result.Remaining < stats.GitHubRemaining) {
			stats.GitHubRemaining = result.Remaining
		}
		if result.Remaining >= 0 && result.Limit > 0 && result.Remaining*10 <= result.Limit {
			low = true
		}
		if err != nil {
			return fact, low, err
		}
		if result.NotModified && etag != "" {
			if item.row == nil || !hasGitHubRepository(*item.row) {
				continue
			}
			fact = *item.row
			fact.Input = item.input
			fact.Subject = item.row.Subject
			fact.SignalsCheckedAt = timePointer(now)
			fact.SignalsNextCheckAt = timePointer(now.Add(signalRecheck))
			fact.SignalsFailCount = 0
			fact.FailCount = 0
			if fullCheck {
				fact.CheckedAt = now
				fact.NextCheckAt = next(now, recheck, item.pkg.ID)
			}
			return fact, low, nil
		}
		if result.NotFound {
			continue
		}
		current, public := result.Repository.GitHubFact()
		if !public {
			continue
		}
		if !haveFirst {
			first, firstETag, haveFirst = current, result.ETag, true
		}
		if !fullCheck {
			// A daily signal refresh never changes the OSI judgment. It updates the
			// repository signals and keeps status, license data and the 30-day
			// schedule from the existing row.
			data, mergeErr := mergeGitHubSignals(item.row.Data, result.Repository)
			if mergeErr != nil {
				return fact, low, mergeErr
			}
			fact.Status, fact.Subject, fact.Data = item.row.Status, current.Repo, data
			fact.CheckedAt = item.row.CheckedAt
			fact.NextCheckAt = item.row.NextCheckAt
			fact.SignalsCheckedAt = timePointer(now)
			fact.SignalsNextCheckAt = timePointer(now.Add(signalRecheck))
			fact.SignalsFailCount = 0
			fact.FailCount = 0
			if result.ETag != "" {
				fact.ETag = &result.ETag
			} else {
				fact.ETag = item.row.ETag
			}
			return fact, low, nil
		}
		if open, ok := result.Repository.OpenSource(); ok {
			current.License = open.License
			data, marshalErr := json.Marshal(current)
			if marshalErr != nil {
				return fact, low, marshalErr
			}
			fact.Status, fact.Subject, fact.Data = "found", current.Repo, data
			fact.SignalsCheckedAt = timePointer(now)
			fact.SignalsNextCheckAt = timePointer(now.Add(signalRecheck))
			fact.SignalsFailCount = 0
			fact.FailCount = 0
			if result.ETag != "" {
				fact.ETag = &result.ETag
			}
			return fact, low, nil
		}
	}
	if !fullCheck && item.row != nil && hasGitHubRepository(*item.row) {
		// A deleted candidate during a signal-only pass does not invalidate the 30-day fact.
		fact = *item.row
		fact.Input = item.input
		fact.SignalsCheckedAt = timePointer(now)
		fact.SignalsNextCheckAt = timePointer(now.Add(signalRecheck))
		fact.SignalsFailCount = 0
		return fact, low, nil
	}
	if haveFirst {
		fact.Status, fact.Subject = "none", first.Repo
		data, err := json.Marshal(first)
		if err != nil {
			return fact, low, err
		}
		fact.Data = data
		fact.SignalsCheckedAt = timePointer(now)
		fact.SignalsNextCheckAt = timePointer(now.Add(signalRecheck))
		fact.SignalsFailCount = 0
		fact.FailCount = 0
		if firstETag != "" {
			fact.ETag = &firstETag
		}
	}
	// Every candidate was read (at most three requests), so "none" is a complete answer even when
	// the quota turned low on the way; the run stops after this app.
	return fact, low, nil
}

func (s *Service) runAppStore(ctx context.Context, queue []pending, deadline time.Time, stats *Stats) error {
	for start := 0; start < len(queue); {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.now().After(deadline) {
			stats.Stopped[AppStore] = "budget"
			return nil
		}
		// Keep each app's identifiers in one lookup.
		end, ids := start, []string{}
		for end < len(queue) && (end == start || len(ids)+len(queue[end].items) <= sources.MaxLookupIDs) {
			ids = append(ids, queue[end].items...)
			end++
		}
		if len(ids) > sources.MaxLookupIDs {
			ids = ids[:sources.MaxLookupIDs]
		}
		if stats.Lookups > 0 {
			if err := s.sleep(ctx, max(s.AppStorePause, 3*time.Second)); err != nil {
				return err
			}
		}
		stats.Lookups++
		listings, err := s.AppStore.Lookup(ctx, ids)
		if errors.Is(err, sources.ErrRateLimited) {
			stats.Stopped[AppStore] = "rate_limited"
			return nil
		}
		for _, item := range queue[start:end] {
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				stats.Failed[AppStore]++
				if err := s.fail(ctx, item, AppStore); err != nil {
					return err
				}
				continue
			}
			now := s.now()
			fact := store.SourceFact{PackageID: item.pkg.ID, Source: AppStore, Input: item.input, Status: "none", CheckedAt: now, NextCheckAt: next(now, recheck, item.pkg.ID)}
			if listing, ok := sources.Match(item.items, item.pkg.Names, listings); ok {
				data, err := json.Marshal(listing)
				if err != nil {
					return err
				}
				fact.Status, fact.Subject, fact.Data, fact.NextCheckAt = "found", listing.BundleID, data, now.Add(priceRecheck)
				if err := s.Store.SaveAppStorePriceSnapshot(ctx, item.pkg.ID, listing, now); err != nil {
					return err
				}
			}
			if err := s.save(ctx, item, fact, stats); err != nil {
				return err
			}
		}
		start = end
	}
	return nil
}

func (s *Service) save(ctx context.Context, item pending, fact store.SourceFact, stats *Stats) error {
	stats.Checked[fact.Source]++
	if fact.Status == "found" {
		stats.Found[fact.Source]++
	}
	changed := !samePublic(item.row, &fact)
	if changed {
		stats.Changed++
	}
	return s.Store.SaveSourceFact(ctx, fact, item.pkg.Token, changed)
}

func (s *Service) fail(ctx context.Context, item pending, source string) error {
	failures := 0
	if item.row != nil {
		failures = item.row.FailCount
	}
	wait := time.Hour << min(failures, 8)
	now := s.now()
	return s.Store.RecordSourceFactFailure(ctx, store.SourceFactKey{PackageID: item.pkg.ID, Source: source}, item.input, now, now.Add(min(wait, maxBackoff)))
}

func (s *Service) failSignals(ctx context.Context, item pending) error {
	failures := 0
	if item.row != nil {
		failures = item.row.SignalsFailCount
	}
	wait := time.Hour << min(failures, 8)
	now := s.now()
	return s.Store.RecordSourceFactSignalFailure(ctx, store.SourceFactKey{PackageID: item.pkg.ID, Source: GitHub}, now.Add(min(wait, maxBackoff)))
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func githubSignalsDue(row store.SourceFact, now time.Time) bool {
	return row.Subject != "" && (row.SignalsNextCheckAt == nil || !row.SignalsNextCheckAt.After(now))
}

func mergeGitHubSignals(data json.RawMessage, repository sources.Repository) ([]byte, error) {
	var fact sources.GitHubFact
	if len(data) > 0 && string(data) != "null" && string(data) != "{}" {
		if err := json.Unmarshal(data, &fact); err != nil {
			return nil, err
		}
	}
	current, ok := repository.GitHubFact()
	if !ok {
		return nil, errors.New("github repository is not public")
	}
	current.License = fact.License
	return json.Marshal(current)
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

// samePublic compares what a page would show: found source facts and public
// GitHub repository rows with a stored subject are both visible.
func samePublic(before, after *store.SourceFact) bool {
	shown := func(row *store.SourceFact) (string, any) {
		if !publicSourceFact(row) {
			return "", nil
		}
		var data any
		_ = json.Unmarshal(row.Data, &data)
		return row.Subject, data
	}
	subjectBefore, dataBefore := shown(before)
	subjectAfter, dataAfter := shown(after)
	return subjectBefore == subjectAfter && reflect.DeepEqual(dataBefore, dataAfter)
}

func hasGitHubRepository(row store.SourceFact) bool {
	return row.Source == GitHub && row.Subject != ""
}

func publicSourceFact(row *store.SourceFact) bool {
	return row != nil && (row.Status == "found" || hasGitHubRepository(*row))
}

// next spreads rechecks over a day so a first fill does not come due all at once later.
func next(now time.Time, after time.Duration, id int64) time.Time {
	return now.Add(after + time.Duration(id%96)*15*time.Minute)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) sleep(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return ctx.Err()
	}
	if s.Sleep != nil {
		return s.Sleep(ctx, wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
