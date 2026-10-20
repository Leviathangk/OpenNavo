package admin

import (
	"context"
	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) RecordSeoGeo(ctx context.Context, r adminapi.RecordSeoGeoRequestObject) (adminapi.RecordSeoGeoResponseObject, error) {
	data, err := h.manage(ctx, "RecordSeoGeo", r)
	return response[adminapi.RecordSeoGeo200JSONResponse](data, err)
}
func (h *Handler) ListSeoGeo(ctx context.Context, r adminapi.ListSeoGeoRequestObject) (adminapi.ListSeoGeoResponseObject, error) {
	data, err := h.manage(ctx, "ListSeoGeo", r)
	return response[adminapi.ListSeoGeo200JSONResponse](data, err)
}
func (h *Handler) GetSeoGeo(ctx context.Context, r adminapi.GetSeoGeoRequestObject) (adminapi.GetSeoGeoResponseObject, error) {
	data, err := h.manage(ctx, "GetSeoGeo", r)
	return response[adminapi.GetSeoGeo200JSONResponse](data, err)
}
