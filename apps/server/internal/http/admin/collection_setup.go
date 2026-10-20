package admin

import (
	"context"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) SetCollectionSetupCriteria(ctx context.Context, r adminapi.SetCollectionSetupCriteriaRequestObject) (adminapi.SetCollectionSetupCriteriaResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetCollectionSetupCriteria", r)
	return response[adminapi.SetCollectionSetupCriteria200JSONResponse](data, err)
}
