package management

import (
	"context"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
)

// pageDecision answers how the site treats one of its URLs from the same facts the pages render from
// (catalog.LookupPage); it never reads measurement tables. The lookup bypasses the public caches.
func (s *Service) pageDecision(ctx context.Context, in Input) (any, error) {
	raw := text(in.Params, "url")
	if raw == "" {
		return nil, domain.Rejected("field_required:url")
	}
	return (&catalog.PublicService{Store: s.Store, WebBaseURL: s.Config.WebBaseURL, Now: s.Now}).LookupPage(ctx, raw)
}
