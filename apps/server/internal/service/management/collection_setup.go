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
	if err := facts.Decode(in.Body, &body); err != nil {
		return nil, rejectedOr(err, "body_invalid")
	}
	if !slices.Contains(i18n.SiteLocales[:], i18n.LocaleCode(body.Locale)) {
		return nil, domain.RejectedDetail("locale_invalid", body.Locale)
	}
	if strings.TrimSpace(body.Criteria) == "" {
		return nil, domain.Rejected("field_required:criteria")
	}
	if len(body.Criteria) > 10000 {
		return nil, domain.Rejected("field_too_long:criteria")
	}
	if err := body.Evidence.Validate(s.now()); err != nil {
		return nil, rejectedOr(err, "evidence_invalid")
	}
	body.Evidence = body.Evidence.WithDefaultValidity()
	return s.write(ctx, in, op, "collection", func(st *store.Store) (any, error) {
		if err := st.SaveCollectionCriteria(ctx, in.ID, body); err != nil {
			return nil, store.MapAdminError(err)
		}
		return body, nil
	})
}
