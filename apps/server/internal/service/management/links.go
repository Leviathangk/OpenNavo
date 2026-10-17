package management

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Build links from runtime configuration; RawMessage preserves database integer precision and explicit nulls.
func (s *Service) withWebURL(raw json.RawMessage, entity string) (json.RawMessage, error) {
	var state struct {
		Kind, Token, Slug, Status string
		Hidden, Removed           bool
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode admin %s visibility: %w", entity, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode admin %s fields: %w", entity, err)
	}
	var link *string
	base := strings.TrimRight(s.Config.WebBaseURL, "/")
	if base != "" {
		path := ""
		switch entity {
		case "package":
			if !state.Hidden && !state.Removed {
				switch state.Kind {
				case "cask", "link":
					path = "/apps/" + url.PathEscape(state.Token)
				case "formula":
					path = "/cli/" + url.PathEscape(state.Token)
				}
			}
		case "collection":
			if state.Status == "published" || state.Status == "scheduled" {
				path = "/collections/" + url.PathEscape(state.Slug)
			}
		}
		if path != "" {
			value := base + path
			link = &value
		}
	}
	encoded, err := json.Marshal(link)
	if err != nil {
		return nil, fmt.Errorf("encode admin web link: %w", err)
	}
	fields["webUrl"] = encoded
	return json.Marshal(fields)
}
