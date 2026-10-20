package management

import (
	"context"

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
	if facts.Decode(in.Body, &body) != nil || len(body.Items) == 0 || len(body.Items) > 100 {
		return nil, domain.Validation()
	}
	now := s.now()
	seen := map[string]bool{}
	for _, r := range body.Items {
		key := r.Kind + "\x00" + r.Key
		if seen[key] || facts.ValidateRecord(r, now) != nil {
			return nil, domain.Validation()
		}
		seen[key] = true
	}
	return s.write(ctx, in, op, "package", func(st *store.Store) (any, error) {
		if _, found, err := st.PackageFacts(ctx, in.ID); err != nil {
			return nil, err
		} else if !found {
			return nil, fail(domain.CodeNotFound)
		}
		if err := st.UpsertFactRecords(ctx, in.ID, body.Items, now); err != nil {
			return nil, validationError(err)
		}
		return read(st)
	})
}
