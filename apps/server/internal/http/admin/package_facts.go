package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetPackageFacts(ctx context.Context, request adminapi.GetPackageFactsRequestObject) (adminapi.GetPackageFactsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetPackageFacts", request)
	return response[adminapi.GetPackageFacts200JSONResponse](data, err)
}

func (h *Handler) SetPackagePrice(ctx context.Context, request adminapi.SetPackagePriceRequestObject) (adminapi.SetPackagePriceResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackagePrice", request)
	return response[adminapi.SetPackagePrice200JSONResponse](data, err)
}

func (h *Handler) SetPackageMacosSupport(ctx context.Context, request adminapi.SetPackageMacosSupportRequestObject) (adminapi.SetPackageMacosSupportResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageMacosSupport", request)
	return response[adminapi.SetPackageMacosSupport200JSONResponse](data, err)
}

func (h *Handler) SetPackageAlternatives(ctx context.Context, request adminapi.SetPackageAlternativesRequestObject) (adminapi.SetPackageAlternativesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageAlternatives", request)
	return response[adminapi.SetPackageAlternatives200JSONResponse](data, err)
}
