package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetDashboardUsageIntraday(ctx context.Context, request adminapi.GetDashboardUsageIntradayRequestObject) (adminapi.GetDashboardUsageIntradayResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetDashboardUsageIntraday", request)
	return response[adminapi.GetDashboardUsageIntraday200JSONResponse](data, err)
}
