package public

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (h *Handler) RecordUsageActivity(ctx context.Context, request publicapi.RecordUsageActivityRequestObject) (publicapi.RecordUsageActivityResponseObject, error) {
	if h.Catalog == nil || request.Body == nil {
		return nil, domain.Internal(nil)
	}
	if c, ok := ctx.(*gin.Context); ok {
		c.Header("Cache-Control", "no-store")
	}
	a := request.Body
	if string(a.Platform) == "web" {
		settings, err := h.Catalog.Store.SEOSettings(requestContext(ctx))
		if err != nil {
			return nil, err
		}
		if !settings.Settings.Analytics.UsageEnabled {
			return usageResponse(store.UsageActivity{OccurredAt: a.OccurredAt}.ReportingDay(a.OccurredAt)), nil
		}
	}
	activity := store.UsageActivity{AnonymousID: a.AnonymousId.String(), Platform: string(a.Platform), OccurredAt: a.OccurredAt, Version: a.Version, Locale: a.Locale}
	if c, ok := ctx.(*gin.Context); ok {
		activity.Country = c.GetString("usageCountry")
	}
	now := time.Now()
	err := h.Catalog.Store.RecordUsage(requestContext(ctx), activity, now)
	if err != nil {
		return nil, err
	}
	return usageResponse(activity.ReportingDay(now)), nil
}

func usageResponse(day string) publicapi.RecordUsageActivity204Response {
	return publicapi.RecordUsageActivity204Response{Headers: publicapi.RecordUsageActivity204ResponseHeaders{XUsageDay: day}}
}
