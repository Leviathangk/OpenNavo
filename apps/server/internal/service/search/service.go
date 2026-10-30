package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/searchtext"
)

type Repository interface {
	SearchSynonyms(context.Context, string) ([]string, error)
	Search(context.Context, domain.SearchInput, []string, float64, float64) ([]domain.SearchMatch, int64, error)
	SearchPenalty(context.Context, string, float64) float64
	Suggest(context.Context, string, string, int) ([]domain.Suggestion, error)
	ReserveQueryID(context.Context) (int64, error)
	SaveSearchQuery(context.Context, domain.SearchQueryLog) error
	SaveSearchClick(context.Context, int64, string, string, int) error
}
type Cache interface {
	Load(context.Context, string) ([]byte, error)
	Save(context.Context, string, []byte, time.Duration) error
}
type Service struct {
	Repo      Repository
	Cache     Cache
	logger    *slog.Logger
	telemetry chan func(context.Context) error
	done      chan struct{}
	mutex     sync.RWMutex
	closed    bool
	cancel    context.CancelFunc
}

func New(ctx context.Context, repo Repository, cache Cache, logger *slog.Logger) *Service {
	background, cancel := context.WithCancel(context.WithoutCancel(ctx))
	service := &Service{Repo: repo, Cache: cache, logger: logger, telemetry: make(chan func(context.Context) error, 1024), done: make(chan struct{}), cancel: cancel}
	go service.consume(background)
	return service
}
func (s *Service) consume(parent context.Context) {
	defer close(s.done)
	for operation := range s.telemetry {
		ctx, cancel := context.WithTimeout(parent, 2*time.Second)
		err := operation(ctx)
		cancel()
		if err != nil {
			s.logger.Warn("search telemetry write failed")
		}
	}
}
func (s *Service) enqueue(operation func(context.Context) error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.telemetry <- operation:
	default:
		s.logger.Warn("search telemetry queue full")
	}
}
func (s *Service) Close() {
	s.mutex.Lock()
	if !s.closed {
		s.closed = true
		close(s.telemetry)
	}
	s.mutex.Unlock()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.cancel()
		<-s.done
	}
	s.cancel()
}

func (s *Service) Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	var result domain.SearchResult
	if input.Kind == "formula" {
		return result, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	rawQuery := input.Query
	input.Query = searchtext.Normalize(input.Query)
	if input.Query == "" || (input.Kind != "" && input.Kind != "cask" && input.Kind != "formula" && input.Kind != "link") || input.Current < 1 || input.Size < 1 || input.Size > 100 || !i18n.Valid(input.Locale) || (input.Platform != "web" && input.Platform != "desktop") {
		return result, domain.Validation()
	}
	synonyms, err := s.Repo.SearchSynonyms(ctx, input.Query)
	if err != nil {
		return result, fmt.Errorf("expand search: %w", err)
	}
	terms := []string{input.Query}
	seen := map[string]bool{input.Query: true}
	result.ExpandedTerms = []string{}
	for _, synonym := range synonyms {
		synonym = searchtext.Normalize(synonym)
		if synonym != "" && !seen[synonym] {
			seen[synonym] = true
			terms = append(terms, synonym)
			result.ExpandedTerms = append(result.ExpandedTerms, synonym)
		}
	}
	fontPenalty := s.Repo.SearchPenalty(ctx, "search.fontPenalty", 0.5)
	libraryPenalty := s.Repo.SearchPenalty(ctx, "search.libraryPenalty", 0.7)
	result.Records, result.Total, err = s.Repo.Search(ctx, input, terms, fontPenalty, libraryPenalty)
	if err != nil {
		return result, fmt.Errorf("search packages: %w", err)
	}
	id, err := s.Repo.ReserveQueryID(ctx)
	if err != nil {
		return result, err
	}
	result.QueryID = strconv.FormatInt(id, 10)
	query := domain.SearchQueryLog{ID: id, Query: rawQuery, Normalized: input.Query, Locale: input.Locale, Platform: input.Platform, ResultCount: result.Total}
	s.enqueue(func(ctx context.Context) error { return s.Repo.SaveSearchQuery(ctx, query) })
	return result, nil
}

func (s *Service) Suggest(ctx context.Context, query, locale string, limit int) ([]domain.Suggestion, error) {
	query = searchtext.Normalize(query)
	if query == "" || limit < 1 || limit > 10 || !i18n.Valid(locale) {
		return nil, domain.Validation()
	}
	key := fmt.Sprintf("c:sug:c2:%s:%s", locale, query) + domain.LinksCacheKey(ctx)
	backend := cache.Scope(ctx, s.Cache)
	if backend != nil {
		data, err := backend.Load(ctx, key)
		if err == nil {
			var items []domain.Suggestion
			if json.Unmarshal(data, &items) == nil {
				return items[:min(limit, len(items))], nil
			}
		}
	}
	// Cache the maximum count allowed by the API so an initial smaller limit cannot affect later requests.
	items, err := s.Repo.Suggest(ctx, query, locale, 10)
	if err != nil {
		return nil, fmt.Errorf("suggest packages: %w", err)
	}
	if backend != nil {
		if data, err := json.Marshal(items); err == nil {
			_ = backend.Save(ctx, key, data, 10*time.Minute)
		}
	}
	return items[:min(limit, len(items))], nil
}

func (s *Service) Click(ctx context.Context, queryID, kind, token string, position int) error {
	if kind == "formula" {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	id, err := strconv.ParseInt(queryID, 10, 64)
	if err != nil || id < 1 || (kind != "cask" && kind != "formula" && kind != "link") || token == "" || position < 1 {
		return domain.Validation()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Share the query FIFO so clicks cannot overtake their corresponding query INSERT.
	s.enqueue(func(ctx context.Context) error { return s.Repo.SaveSearchClick(ctx, id, kind, token, position) })
	return nil
}

var ErrCacheMiss = errors.New("cache miss")
