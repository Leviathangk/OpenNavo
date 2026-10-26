package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

// Site indexing reads and the per-app intent page switches and overview overrides that replace the
// in-server SEO automation: agents read decisions here and change only catalog content.

func (h *Handler) GetPageDecision(ctx context.Context, r adminapi.GetPageDecisionRequestObject) (adminapi.GetPageDecisionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetPageDecision", r)
	return response[adminapi.GetPageDecision200JSONResponse](data, err)
}

func (h *Handler) GetSiteIndexSettings(ctx context.Context, r adminapi.GetSiteIndexSettingsRequestObject) (adminapi.GetSiteIndexSettingsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetSiteIndexSettings", r)
	return response[adminapi.GetSiteIndexSettings200JSONResponse](data, err)
}

func (h *Handler) GetPackageIntentPages(ctx context.Context, r adminapi.GetPackageIntentPagesRequestObject) (adminapi.GetPackageIntentPagesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetPackageIntentPages", r)
	return response[adminapi.GetPackageIntentPages200JSONResponse](data, err)
}

func (h *Handler) SetPackageIntentPages(ctx context.Context, r adminapi.SetPackageIntentPagesRequestObject) (adminapi.SetPackageIntentPagesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageIntentPages", r)
	return response[adminapi.SetPackageIntentPages200JSONResponse](data, err)
}

func (h *Handler) ListIntentPages(ctx context.Context, r adminapi.ListIntentPagesRequestObject) (adminapi.ListIntentPagesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListIntentPages", r)
	return response[adminapi.ListIntentPages200JSONResponse](data, err)
}

func (h *Handler) GetPackageSeo(ctx context.Context, r adminapi.GetPackageSeoRequestObject) (adminapi.GetPackageSeoResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetPackageSeo", r)
	return response[adminapi.GetPackageSeo200JSONResponse](data, err)
}

func (h *Handler) SetPackageSeo(ctx context.Context, r adminapi.SetPackageSeoRequestObject) (adminapi.SetPackageSeoResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageSeo", r)
	return response[adminapi.SetPackageSeo200JSONResponse](data, err)
}

func (h *Handler) ListDueFactRecords(ctx context.Context, r adminapi.ListDueFactRecordsRequestObject) (adminapi.ListDueFactRecordsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListDueFactRecords", r)
	return response[adminapi.ListDueFactRecords200JSONResponse](data, err)
}
