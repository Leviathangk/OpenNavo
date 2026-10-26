package management

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) agentAffectedURLs(ctx context.Context, object string, in Input, refs []content.Ref, before, after, result any) ([]string, error) {
	urls := []string{}
	add := func(values []string) {
		for _, value := range values {
			if !slices.Contains(urls, value) {
				urls = append(urls, value)
			}
		}
	}
	if view, ok := result.(map[string]any); ok {
		encoded, _ := json.Marshal(view["affectedUrls"])
		var values []string
		_ = json.Unmarshal(encoded, &values)
		add(values)
	}
	decode := func(value any) map[string]store.HistoryState {
		raw, _ := json.Marshal(value)
		var states map[string]store.HistoryState
		_ = json.Unmarshal(raw, &states)
		return states
	}
	previous, next := decode(before), decode(after)
	for _, ref := range refs {
		if object == "article" { // article identity differs from the translation revision identity
			continue
		}
		key := ref.Entity + ":" + ref.Key()
		values, err := s.Store.HistoryURLs(ctx, ref, s.Config.WebBaseURL, previous[key], next[key])
		if err != nil {
			return nil, err
		}
		add(values)
	}
	base := strings.TrimRight(s.Config.WebBaseURL, "/")
	if base != "" {
		switch object {
		case "article":
			paths, err := s.Store.ArticlePaths(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			for _, path := range paths {
				add([]string{base + path})
			}
		case "category":
			add([]string{base + "/"})
		case "intent_pages", "package_seo":
			values, err := s.packagePageURLs(ctx, object, in)
			if err != nil {
				return nil, err
			}
			add(values)
		}
	}
	return urls, nil
}
