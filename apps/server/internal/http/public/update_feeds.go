package public

import (
	"context"
	"strings"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetCatalogUpdateFeeds(ctx context.Context, request publicapi.GetCatalogUpdateFeedsRequestObject) (publicapi.GetCatalogUpdateFeedsResponseObject, error) {
	if h.Snapshot == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Snapshot.UpdateFeeds(requestContext(ctx))
	if err != nil {
		return nil, err
	}
	if request.Params.IfNoneMatch != nil {
		for _, tag := range strings.Split(*request.Params.IfNoneMatch, ",") {
			if tag = strings.TrimSpace(tag); tag == "*" || strings.TrimPrefix(tag, "W/") == data.ETag {
				return publicapi.GetCatalogUpdateFeeds304Response{Headers: publicapi.GetCatalogUpdateFeeds304ResponseHeaders{ETag: &data.ETag}}, nil
			}
		}
	}
	body, err := response[publicapi.CatalogUpdateFeedsResponse](data, nil)
	return publicapi.GetCatalogUpdateFeeds200JSONResponse{Body: body, Headers: publicapi.GetCatalogUpdateFeeds200ResponseHeaders{ETag: &data.ETag}}, err
}
