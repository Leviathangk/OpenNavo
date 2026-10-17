package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) UpsertUpdateFeed(ctx context.Context, request adminapi.UpsertUpdateFeedRequestObject) (adminapi.UpsertUpdateFeedResponseObject, error) {
	data, err := h.manage(ctx, "UpsertUpdateFeed", request)
	return response[adminapi.UpsertUpdateFeed200JSONResponse](data, err)
}
func (h *Handler) DisableUpdateFeed(ctx context.Context, request adminapi.DisableUpdateFeedRequestObject) (adminapi.DisableUpdateFeedResponseObject, error) {
	data, err := h.manage(ctx, "DisableUpdateFeed", request)
	return response[adminapi.DisableUpdateFeed200JSONResponse](data, err)
}
