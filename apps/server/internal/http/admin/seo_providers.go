package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) GetSeoProvider(ctx context.Context, r adminapi.GetSeoProviderRequestObject) (adminapi.GetSeoProviderResponseObject, error) {
	data, err := h.manage(ctx, "GetSeoProvider", r)
	return response[adminapi.GetSeoProvider200JSONResponse](data, err)
}
func (h *Handler) UpdateSeoProvider(ctx context.Context, r adminapi.UpdateSeoProviderRequestObject) (adminapi.UpdateSeoProviderResponseObject, error) {
	data, err := h.manage(ctx, "UpdateSeoProvider", r)
	return response[adminapi.UpdateSeoProvider200JSONResponse](data, err)
}
