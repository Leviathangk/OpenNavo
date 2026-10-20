package admin

import (
	"context"
	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) GetSeoListingCandidate(ctx context.Context, r adminapi.GetSeoListingCandidateRequestObject) (adminapi.GetSeoListingCandidateResponseObject, error) {
	data, err := h.manage(ctx, "GetSeoListingCandidate", r)
	return response[adminapi.GetSeoListingCandidate200JSONResponse](data, err)
}
func (h *Handler) UpsertSeoListingCandidate(ctx context.Context, r adminapi.UpsertSeoListingCandidateRequestObject) (adminapi.UpsertSeoListingCandidateResponseObject, error) {
	data, err := h.manage(ctx, "UpsertSeoListingCandidate", r)
	return response[adminapi.UpsertSeoListingCandidate200JSONResponse](data, err)
}
func (h *Handler) LockSeoListingBaseline(ctx context.Context, r adminapi.LockSeoListingBaselineRequestObject) (adminapi.LockSeoListingBaselineResponseObject, error) {
	data, err := h.manage(ctx, "LockSeoListingBaseline", r)
	return response[adminapi.LockSeoListingBaseline200JSONResponse](data, err)
}
