package public

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seoexperiments"
)

func (h *Handler) GetSeoExperimentAssignment(ctx context.Context, r publicapi.GetSeoExperimentAssignmentRequestObject) (publicapi.GetSeoExperimentAssignmentResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	if c, ok := ctx.(*gin.Context); ok {
		c.Header("Cache-Control", "no-store")
	}
	data, err := (&seoexperiments.Service{DB: h.Catalog.Store.DB, WebURL: h.Catalog.WebBaseURL}).Assignment(requestContext(ctx), r.Params.Url)
	if errors.Is(err, seoexperiments.ErrSample) {
		err = domain.Validation()
	}
	return response[publicapi.GetSeoExperimentAssignment200JSONResponse](data, err)
}
