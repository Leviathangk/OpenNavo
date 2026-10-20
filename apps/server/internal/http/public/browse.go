package public

import (
	"context"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetBrowse(ctx context.Context, request publicapi.GetBrowseRequestObject) (publicapi.GetBrowseResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	p := request.Params
	data, err := h.Catalog.Browse(requestContext(ctx), string(fallback(p.Kind, "apps")), fallback(p.Bucket, ""), locale(ctx, p.Locale), fallback(p.Page, 1))
	return response[publicapi.GetBrowse200JSONResponse](data, err)
}
