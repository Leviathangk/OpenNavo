package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetPackageFactRecords(ctx context.Context, r adminapi.GetPackageFactRecordsRequestObject) (adminapi.GetPackageFactRecordsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetPackageFactRecords", r)
	return response[adminapi.GetPackageFactRecords200JSONResponse](data, err)
}
func (h *Handler) SetPackageFactRecords(ctx context.Context, r adminapi.SetPackageFactRecordsRequestObject) (adminapi.SetPackageFactRecordsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageFactRecords", r)
	return response[adminapi.SetPackageFactRecords200JSONResponse](data, err)
}
