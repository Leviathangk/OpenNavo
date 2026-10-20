package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetSeoOverview(ctx context.Context, r adminapi.GetSeoOverviewRequestObject) (adminapi.GetSeoOverviewResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetSeoOverview", r)
	return response[adminapi.GetSeoOverview200JSONResponse](data, err)
}

func (h *Handler) ListSeoAlerts(ctx context.Context, r adminapi.ListSeoAlertsRequestObject) (adminapi.ListSeoAlertsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListSeoAlerts", r)
	return response[adminapi.ListSeoAlerts200JSONResponse](data, err)
}

func (h *Handler) GetSeoPage(ctx context.Context, r adminapi.GetSeoPageRequestObject) (adminapi.GetSeoPageResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetSeoPage", r)
	return response[adminapi.GetSeoPage200JSONResponse](data, err)
}

func (h *Handler) ListSeoKeywords(ctx context.Context, r adminapi.ListSeoKeywordsRequestObject) (adminapi.ListSeoKeywordsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListSeoKeywords", r)
	return response[adminapi.ListSeoKeywords200JSONResponse](data, err)
}

func (h *Handler) UpsertSeoKeywords(ctx context.Context, r adminapi.UpsertSeoKeywordsRequestObject) (adminapi.UpsertSeoKeywordsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpsertSeoKeywords", r)
	return response[adminapi.UpsertSeoKeywords200JSONResponse](data, err)
}
