package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) SetBackgroundItem(ctx context.Context, r adminapi.SetBackgroundItemRequestObject) (adminapi.SetBackgroundItemResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetBackgroundItem", r)
	return response[adminapi.SetBackgroundItem200JSONResponse](data, err)
}
