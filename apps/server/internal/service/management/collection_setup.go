package management

import (
	"context"
	"slices"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) saveCollectionCriteria(ctx context.Context, op string, in Input) (any, error) {
	var body store.CollectionCriteria
	if facts.Decode(in.Body, &body) != nil || !slices.Contains(i18n.SiteLocales[:], i18n.LocaleCode(body.Locale)) || strings.TrimSpace(body.Criteria) == "" || len(body.Criteria) > 10000 || body.Evidence.Validate(s.now()) != nil {
		return nil, domain.Validation()
	}
	body.Evidence = body.Evidence.WithDefaultValidity()
	return s.write(ctx, in, op, "collection", func(st *store.Store) (any, error) {
		if err := st.SaveCollectionCriteria(ctx, in.ID, body); err != nil {
			return nil, store.MapAdminError(err)
		}
		return body, nil
	})
}
