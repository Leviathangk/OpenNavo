package management

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) linkOperation(ctx context.Context, op string, in Input) (any, error) {
	if op == "AddLinkPackage" && domain.CurrentOperation(ctx).AgentClientID > 0 {
		return nil, &agentgate.Violation{Reason: "candidate_required"}
	}
	if op == "ListLinkPackages" {
		rows, err := s.Store.LinkApps(ctx, false)
		if err != nil {
			return nil, err
		}
		current, size, err := page(in)
		if err != nil {
			return nil, err
		}
		q := strings.ToLower(strings.TrimSpace(text(in.Params, "q")))
		filtered := []store.LinkApp{}
		for _, row := range rows {
			if q == "" || strings.Contains(strings.ToLower(row.Token+" "+row.Name), q) {
				filtered = append(filtered, row)
			}
		}
		start := min((current-1)*size, len(filtered))
		end := min(start+size, len(filtered))
		return map[string]any{"records": filtered[start:end], "total": len(filtered), "current": current, "size": size}, nil
	}
	var previous *store.LinkApp
	var err error
	if op != "AddLinkPackage" {
		previous, err = s.Store.LinkApp(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if previous == nil || previous.Kind != "link" || previous.Removed {
			return nil, fail(domain.CodeNotFound)
		}
	}
	input := domain.LinkInput{}
	if previous != nil {
		input = previous.LinkInput
	}
	if op != "RemoveLinkPackage" {
		// PATCH merges nullable fields, while preserving omitted fields and immutable token.
		if previous != nil {
			if v, ok := in.Body["token"]; ok && v != previous.Token {
				return nil, domain.Validation()
			}
		}
		body, err := json.Marshal(in.Body)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &input); err != nil {
			return nil, domain.Validation()
		}
		if err := input.Validate(); err != nil {
			return nil, err
		}
		if err := s.Store.LinkTokenAvailable(ctx, input.Token, in.ID); err != nil {
			return nil, err
		}
	}
	if domain.CurrentOperation(ctx).DryRun {
		action, key := "create", input.Token
		var after any = input
		if previous != nil {
			action, key = "update", fmt.Sprint(in.ID)
		}
		if op == "RemoveLinkPackage" {
			action, after = "delete", nil
		}
		return map[string]any{"dryRun": true, "requestId": domain.CurrentOperation(ctx).RequestID, "affectedUrls": []string{}, "changes": []any{map[string]any{"object": map[string]any{"entity": "package", "objectKey": key}, "action": action, "before": previous, "after": after}}}, nil
	}
	return s.write(ctx, in, op, "link_app", func(st *store.Store) (any, error) {
		if op == "RemoveLinkPackage" {
			return nil, st.RemoveLinkApp(ctx, in.ID)
		}
		// Re-read PATCH under the lock so concurrent editorial patches cannot lose fields.
		if err := store.LockCatalogWrites(ctx, st.DB); err != nil {
			return nil, err
		}
		if previous != nil {
			row, err := st.LinkApp(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if row == nil || row.Kind != "link" || row.Removed {
				return nil, fail(domain.CodeNotFound)
			}
			input = row.LinkInput
			body, err := json.Marshal(in.Body)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(body, &input); err != nil {
				return nil, err
			}
		}
		id, err := st.WriteLinkApp(ctx, in.ID, input)
		if err != nil {
			return nil, err
		}
		row, err := st.LinkApp(ctx, id)
		if err != nil {
			return nil, err
		}
		// The generic audit writer reads the new ID from map results.
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		err = json.Unmarshal(raw, &result)
		if err == nil {
			result["id"] = id
		}
		return result, err
	})
}
