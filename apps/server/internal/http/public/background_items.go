package public

import (
	"context"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) ListBackgroundItems(ctx context.Context, r publicapi.ListBackgroundItemsRequestObject) (publicapi.ListBackgroundItemsResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.BackgroundItems(requestContext(ctx), fallback(r.Params.Q, ""), locale(ctx, r.Params.Locale), fallback(r.Params.Current, 1), fallback(r.Params.Size, 24))
	return response[publicapi.ListBackgroundItems200JSONResponse](data, err)
}
func (h *Handler) GetBackgroundItem(ctx context.Context, r publicapi.GetBackgroundItemRequestObject) (publicapi.GetBackgroundItemResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.BackgroundItem(requestContext(ctx), r.Label, locale(ctx, r.Params.Locale))
	return response[publicapi.GetBackgroundItem200JSONResponse](data, err)
}
