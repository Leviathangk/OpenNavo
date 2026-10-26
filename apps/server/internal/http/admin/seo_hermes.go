package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) GetTranslationsSource(ctx context.Context, request adminapi.GetTranslationsSourceRequestObject) (adminapi.GetTranslationsSourceResponseObject, error) {
	data, err := h.manage(ctx, "GetTranslationsSource", request)
	return response[adminapi.GetTranslationsSource200JSONResponse](data, err)
}
func (h *Handler) WriteTranslations(ctx context.Context, request adminapi.WriteTranslationsRequestObject) (adminapi.WriteTranslationsResponseObject, error) {
	data, err := h.manage(ctx, "WriteTranslations", request)
	return response[adminapi.WriteTranslations200JSONResponse](data, err)
}
