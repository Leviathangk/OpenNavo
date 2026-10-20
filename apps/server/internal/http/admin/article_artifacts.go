package admin

import (
	"context"
	"github.com/opennavo/opennavo/server/gen/adminapi"
)

func (h *Handler) ListArticleTestArtifacts(ctx context.Context, r adminapi.ListArticleTestArtifactsRequestObject) (adminapi.ListArticleTestArtifactsResponseObject, error) {
	data, err := h.manage(ctx, "ListArticleTestArtifacts", r)
	return response[adminapi.ListArticleTestArtifacts200JSONResponse](data, err)
}
func (h *Handler) CreateArticleTestArtifact(ctx context.Context, r adminapi.CreateArticleTestArtifactRequestObject) (adminapi.CreateArticleTestArtifactResponseObject, error) {
	data, err := h.manage(ctx, "CreateArticleTestArtifact", r)
	return response[adminapi.CreateArticleTestArtifact200JSONResponse](data, err)
}
