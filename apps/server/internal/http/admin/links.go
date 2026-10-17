package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) ListLinkPackages(ctx context.Context, request adminapi.ListLinkPackagesRequestObject) (adminapi.ListLinkPackagesResponseObject, error) {
	data, err := h.manage(ctx, "ListLinkPackages", request)
	return response[adminapi.ListLinkPackages200JSONResponse](data, err)
}
func (h *Handler) AddLinkPackage(ctx context.Context, request adminapi.AddLinkPackageRequestObject) (adminapi.AddLinkPackageResponseObject, error) {
	data, err := h.manage(ctx, "AddLinkPackage", request)
	return response[adminapi.AddLinkPackage200JSONResponse](data, err)
}
func (h *Handler) UpdateLinkPackage(ctx context.Context, request adminapi.UpdateLinkPackageRequestObject) (adminapi.UpdateLinkPackageResponseObject, error) {
	data, err := h.manage(ctx, "UpdateLinkPackage", request)
	return response[adminapi.UpdateLinkPackage200JSONResponse](data, err)
}
func (h *Handler) RemoveLinkPackage(ctx context.Context, request adminapi.RemoveLinkPackageRequestObject) (adminapi.RemoveLinkPackageResponseObject, error) {
	data, err := h.manage(ctx, "RemoveLinkPackage", request)
	return response[adminapi.RemoveLinkPackage200JSONResponse](data, err)
}
