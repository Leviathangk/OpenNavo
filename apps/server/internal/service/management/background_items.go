package management

import (
	"context"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) saveBackgroundItem(ctx context.Context, op string, in Input) (any, error) {
	var body facts.BackgroundContent
	if !facts.ValidBackgroundLabel(in.Key) || facts.Decode(in.Body, &body) != nil || body.Validate(s.now()) != nil {
		return nil, domain.Validation()
	}
	return s.write(ctx, in, op, "background_item", func(st *store.Store) (any, error) {
		if err := st.SaveBackgroundContent(ctx, in.Key, body); err != nil {
			return nil, store.MapAdminError(err)
		}
		return body, nil
	})
}
