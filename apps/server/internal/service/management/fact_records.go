package management

import (
	"context"
	"errors"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) factRecords(ctx context.Context, op string, in Input) (any, error) {
	read := func(st *store.Store) (any, error) {
		if _, found, err := st.PackageFacts(ctx, in.ID); err != nil {
			return nil, err
		} else if !found {
			return nil, fail(domain.CodeNotFound)
		}
		records, err := st.FactRecords(ctx, in.ID)
		return map[string]any{"items": records}, err
	}
	if op == "GetPackageFactRecords" {
		return read(s.Store)
	}
	var body struct {
		Items []facts.Record `json:"items"`
	}
	if err := facts.Decode(in.Body, &body); err != nil {
		return nil, err
	}
	if len(body.Items) == 0 || len(body.Items) > 100 {
		return nil, domain.Rejected("record_items_invalid")
	}
	now := s.now()
	seen := map[string]bool{}
	for i := range body.Items {
		r := &body.Items[i]
		key := r.Kind + "\x00" + r.Key
		if seen[key] {
			return nil, domain.Rejected("record_duplicate")
		}
		normalized, err := facts.NormalizeRecord(*r, now)
		if err != nil {
			return nil, err
		}
		*r = normalized
		seen[key] = true
	}
	return s.write(ctx, in, op, "package", func(st *store.Store) (any, error) {
		if _, found, err := st.PackageFacts(ctx, in.ID); err != nil {
			return nil, err
		} else if !found {
			return nil, fail(domain.CodeNotFound)
		}
		if err := st.UpsertFactRecords(ctx, in.ID, body.Items, now); err != nil {
			var app *domain.AppError
			if errors.As(err, &app) {
				return nil, err
			}
			return nil, validationError(err)
		}
		return read(st)
	})
}
