package management

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) updateFeedOperation(ctx context.Context, op string, in Input) (any, error) {
	var input domain.UpdateFeedInput
	raw, err := json.Marshal(in.Body)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, domain.Rejected("body_invalid")
	}
	if op == "DisableUpdateFeed" {
		err = input.UpdateFeedKey.Validate()
	} else {
		err = input.Validate()
	}
	if err != nil {
		return nil, err
	}
	before, err := s.Store.UpdateFeed(ctx, input.UpdateFeedKey)
	if err != nil {
		return nil, err
	}
	if op == "DisableUpdateFeed" && before == nil {
		return nil, fail(domain.CodeNotFound)
	}
	if op != "DisableUpdateFeed" {
		if err := s.Store.ValidateUpdateFeedPackage(ctx, input); err != nil {
			return nil, err
		}
	}
	var after any = input
	action := "update"
	if before == nil {
		action = "create"
	}
	if op == "DisableUpdateFeed" {
		next := *before
		next.Enabled = false
		next.Origin = "manual"
		after = next
	}
	if domain.CurrentOperation(ctx).DryRun {
		return map[string]any{"dryRun": true, "requestId": domain.CurrentOperation(ctx).RequestID, "affectedUrls": []string{}, "changes": []any{map[string]any{"object": map[string]any{"entity": "update_feed", "objectKey": input.Source + ":" + input.BundleID}, "action": action, "before": before, "after": after}}}, nil
	}
	in.Key = input.Source + ":" + input.BundleID
	return s.write(ctx, in, op, "update_feed", func(st *store.Store) (any, error) {
		if op == "DisableUpdateFeed" {
			err = st.DisableUpdateFeed(ctx, input.UpdateFeedKey)
		} else {
			err = st.WriteUpdateFeed(ctx, input, "manual")
		}
		if err != nil {
			return nil, err
		}
		return st.UpdateFeed(ctx, input.UpdateFeedKey)
	})
}
