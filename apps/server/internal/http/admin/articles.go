package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
)

func (h *Handler) ListAdminArticles(ctx context.Context, request adminapi.ListAdminArticlesRequestObject) (adminapi.ListAdminArticlesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAdminArticles", request)
	return response[adminapi.ListAdminArticles200JSONResponse](data, err)
}

func (h *Handler) CreateArticle(ctx context.Context, request adminapi.CreateArticleRequestObject) (adminapi.CreateArticleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateArticle", request)
	return response[adminapi.CreateArticle200JSONResponse](data, err)
}

func (h *Handler) GetAdminArticle(ctx context.Context, request adminapi.GetAdminArticleRequestObject) (adminapi.GetAdminArticleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAdminArticle", request)
	return response[adminapi.GetAdminArticle200JSONResponse](data, err)
}

func (h *Handler) UpdateArticle(ctx context.Context, request adminapi.UpdateArticleRequestObject) (adminapi.UpdateArticleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateArticle", request)
	return response[adminapi.UpdateArticle200JSONResponse](data, err)
}

func (h *Handler) SetArticlePackages(ctx context.Context, request adminapi.SetArticlePackagesRequestObject) (adminapi.SetArticlePackagesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetArticlePackages", request)
	return response[adminapi.SetArticlePackages200JSONResponse](data, err)
}

func (h *Handler) SubmitArticleReview(ctx context.Context, request adminapi.SubmitArticleReviewRequestObject) (adminapi.SubmitArticleReviewResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SubmitArticleReview", request)
	return response[adminapi.SubmitArticleReview200JSONResponse](data, err)
}

func (h *Handler) PublishArticle(ctx context.Context, request adminapi.PublishArticleRequestObject) (adminapi.PublishArticleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "PublishArticle", request)
	return response[adminapi.PublishArticle200JSONResponse](data, err)
}

func (h *Handler) WithdrawArticle(ctx context.Context, request adminapi.WithdrawArticleRequestObject) (adminapi.WithdrawArticleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "WithdrawArticle", request)
	return response[adminapi.WithdrawArticle200JSONResponse](data, err)
}

func (h *Handler) RestoreArticle(ctx context.Context, request adminapi.RestoreArticleRequestObject) (adminapi.RestoreArticleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RestoreArticle", request)
	return response[adminapi.RestoreArticle200JSONResponse](data, err)
}

func (h *Handler) RevertArticle(ctx context.Context, request adminapi.RevertArticleRequestObject) (adminapi.RevertArticleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RevertArticle", request)
	return response[adminapi.RevertArticle200JSONResponse](data, err)
}

func (h *Handler) SetArticleSlug(ctx context.Context, request adminapi.SetArticleSlugRequestObject) (adminapi.SetArticleSlugResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetArticleSlug", request)
	return response[adminapi.SetArticleSlug200JSONResponse](data, err)
}

func (h *Handler) SetArticleLocale(ctx context.Context, request adminapi.SetArticleLocaleRequestObject) (adminapi.SetArticleLocaleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetArticleLocale", request)
	return response[adminapi.SetArticleLocale200JSONResponse](data, err)
}

func (h *Handler) ListArticleAuthors(ctx context.Context, request adminapi.ListArticleAuthorsRequestObject) (adminapi.ListArticleAuthorsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListArticleAuthors", request)
	return response[adminapi.ListArticleAuthors200JSONResponse](data, err)
}

func (h *Handler) CreateArticleAuthor(ctx context.Context, request adminapi.CreateArticleAuthorRequestObject) (adminapi.CreateArticleAuthorResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateArticleAuthor", request)
	return response[adminapi.CreateArticleAuthor200JSONResponse](data, err)
}

func (h *Handler) UpdateArticleAuthor(ctx context.Context, request adminapi.UpdateArticleAuthorRequestObject) (adminapi.UpdateArticleAuthorResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateArticleAuthor", request)
	return response[adminapi.UpdateArticleAuthor200JSONResponse](data, err)
}
