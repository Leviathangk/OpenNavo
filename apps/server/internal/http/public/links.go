package public

import (
	"context"
	"strings"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) GetCatalogLinks(ctx context.Context, request publicapi.GetCatalogLinksRequestObject) (publicapi.GetCatalogLinksResponseObject, error) {
	if h.Snapshot == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Snapshot.Links(requestContext(ctx))
	if err != nil {
		return nil, err
	}
	if request.Params.IfNoneMatch != nil {
		for _, tag := range strings.Split(*request.Params.IfNoneMatch, ",") {
			if tag = strings.TrimSpace(tag); tag == "*" || strings.TrimPrefix(tag, "W/") == data.ETag {
				return publicapi.GetCatalogLinks304Response{Headers: publicapi.GetCatalogLinks304ResponseHeaders{ETag: &data.ETag}}, nil
			}
		}
	}
	body, err := response[publicapi.CatalogLinksResponse](data, nil)
	return publicapi.GetCatalogLinks200JSONResponse{Body: body, Headers: publicapi.GetCatalogLinks200ResponseHeaders{ETag: &data.ETag}}, err
}
