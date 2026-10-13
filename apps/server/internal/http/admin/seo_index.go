package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetPackageIndexStatus(ctx context.Context, request adminapi.GetPackageIndexStatusRequestObject) (adminapi.GetPackageIndexStatusResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetPackageIndexStatus", request)
	return response[adminapi.GetPackageIndexStatus200JSONResponse](data, err)
}

func (h *Handler) SetPackageIndexStatus(ctx context.Context, request adminapi.SetPackageIndexStatusRequestObject) (adminapi.SetPackageIndexStatusResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageIndexStatus", request)
	return response[adminapi.SetPackageIndexStatus200JSONResponse](data, err)
}

func (h *Handler) ListIndexReport(ctx context.Context, request adminapi.ListIndexReportRequestObject) (adminapi.ListIndexReportResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListIndexReport", request)
	return response[adminapi.ListIndexReport200JSONResponse](data, err)
}
