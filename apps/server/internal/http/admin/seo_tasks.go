package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) ClaimSeoTasks(ctx context.Context, r adminapi.ClaimSeoTasksRequestObject) (adminapi.ClaimSeoTasksResponseObject, error) {
	data, err := h.manage(ctx, "ClaimSeoTasks", r)
	return response[adminapi.ClaimSeoTasks200JSONResponse](data, err)
}
func (h *Handler) GetSeoTask(ctx context.Context, r adminapi.GetSeoTaskRequestObject) (adminapi.GetSeoTaskResponseObject, error) {
	data, err := h.manage(ctx, "GetSeoTask", r)
	return response[adminapi.GetSeoTask200JSONResponse](data, err)
}
func (h *Handler) CompleteSeoTask(ctx context.Context, r adminapi.CompleteSeoTaskRequestObject) (adminapi.CompleteSeoTaskResponseObject, error) {
	data, err := h.manage(ctx, "CompleteSeoTask", r)
	return response[adminapi.CompleteSeoTask200JSONResponse](data, err)
}
func (h *Handler) DeferSeoTask(ctx context.Context, r adminapi.DeferSeoTaskRequestObject) (adminapi.DeferSeoTaskResponseObject, error) {
	data, err := h.manage(ctx, "DeferSeoTask", r)
	return response[adminapi.DeferSeoTask200JSONResponse](data, err)
}
func (h *Handler) ReopenSeoTask(ctx context.Context, r adminapi.ReopenSeoTaskRequestObject) (adminapi.ReopenSeoTaskResponseObject, error) {
	data, err := h.manage(ctx, "ReopenSeoTask", r)
	return response[adminapi.ReopenSeoTask200JSONResponse](data, err)
}
func (h *Handler) NominateSeoListing(ctx context.Context, r adminapi.NominateSeoListingRequestObject) (adminapi.NominateSeoListingResponseObject, error) {
	data, err := h.manage(ctx, "NominateSeoListing", r)
	return response[adminapi.NominateSeoListing200JSONResponse](data, err)
}
func (h *Handler) NominateSeoTopic(ctx context.Context, r adminapi.NominateSeoTopicRequestObject) (adminapi.NominateSeoTopicResponseObject, error) {
	data, err := h.manage(ctx, "NominateSeoTopic", r)
	return response[adminapi.NominateSeoTopic200JSONResponse](data, err)
}
func (h *Handler) CreateSeoEvidence(ctx context.Context, r adminapi.CreateSeoEvidenceRequestObject) (adminapi.CreateSeoEvidenceResponseObject, error) {
	data, err := h.manage(ctx, "CreateSeoEvidence", r)
	return response[adminapi.CreateSeoEvidence200JSONResponse](data, err)
}
func (h *Handler) ListSeoActions(ctx context.Context, r adminapi.ListSeoActionsRequestObject) (adminapi.ListSeoActionsResponseObject, error) {
	data, err := h.manage(ctx, "ListSeoActions", r)
	return response[adminapi.ListSeoActions200JSONResponse](data, err)
}
func (h *Handler) ProposeSnippetTemplate(ctx context.Context, r adminapi.ProposeSnippetTemplateRequestObject) (adminapi.ProposeSnippetTemplateResponseObject, error) {
	data, err := h.manage(ctx, "ProposeSnippetTemplate", r)
	return response[adminapi.ProposeSnippetTemplate200JSONResponse](data, err)
}
func (h *Handler) CompensateSeoAction(ctx context.Context, r adminapi.CompensateSeoActionRequestObject) (adminapi.CompensateSeoActionResponseObject, error) {
	data, err := h.manage(ctx, "CompensateSeoAction", r)
	return response[adminapi.CompensateSeoAction200JSONResponse](data, err)
}
