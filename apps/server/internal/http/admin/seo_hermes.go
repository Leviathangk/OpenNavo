package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) GetSeoAutomation(ctx context.Context, request adminapi.GetSeoAutomationRequestObject) (adminapi.GetSeoAutomationResponseObject, error) {
	data, err := h.manage(ctx, "GetSeoAutomation", request)
	return response[adminapi.GetSeoAutomation200JSONResponse](data, err)
}
func (h *Handler) UpdateSeoAutomation(ctx context.Context, request adminapi.UpdateSeoAutomationRequestObject) (adminapi.UpdateSeoAutomationResponseObject, error) {
	data, err := h.manage(ctx, "UpdateSeoAutomation", request)
	return response[adminapi.UpdateSeoAutomation200JSONResponse](data, err)
}
func (h *Handler) ImportSeoTasks(ctx context.Context, request adminapi.ImportSeoTasksRequestObject) (adminapi.ImportSeoTasksResponseObject, error) {
	data, err := h.manage(ctx, "ImportSeoTasks", request)
	return response[adminapi.ImportSeoTasks200JSONResponse](data, err)
}
func (h *Handler) GetTranslationsSource(ctx context.Context, request adminapi.GetTranslationsSourceRequestObject) (adminapi.GetTranslationsSourceResponseObject, error) {
	data, err := h.manage(ctx, "GetTranslationsSource", request)
	return response[adminapi.GetTranslationsSource200JSONResponse](data, err)
}
func (h *Handler) WriteTranslations(ctx context.Context, request adminapi.WriteTranslationsRequestObject) (adminapi.WriteTranslationsResponseObject, error) {
	data, err := h.manage(ctx, "WriteTranslations", request)
	return response[adminapi.WriteTranslations200JSONResponse](data, err)
}
