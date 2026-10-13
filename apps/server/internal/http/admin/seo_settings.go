package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetSeoSettings(ctx context.Context, request adminapi.GetSeoSettingsRequestObject) (adminapi.GetSeoSettingsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetSeoSettings", request)
	return response[adminapi.GetSeoSettings200JSONResponse](data, err)
}

func (h *Handler) UpdateSeoSettings(ctx context.Context, request adminapi.UpdateSeoSettingsRequestObject) (adminapi.UpdateSeoSettingsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateSeoSettings", request)
	return response[adminapi.UpdateSeoSettings200JSONResponse](data, err)
}
