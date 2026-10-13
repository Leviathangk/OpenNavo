package public

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/service/search"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Replace Handler placeholder methods individually in the corresponding business tasks.
type Handler struct {
	Desktop  *desktop.Service
	Snapshot *snapshot.Service
	Catalog  *catalog.PublicService
	Search   *search.Service
}

func requestContext(ctx context.Context) context.Context {
	if c, ok := ctx.(*gin.Context); ok {
		return c.Request.Context()
	}
	return ctx
}
func locale(ctx context.Context, value *publicapi.Locale) string {
	if value != nil {
		return string(*value)
	}
	if c, ok := ctx.(*gin.Context); ok {
		return respond.Locale(c)
	}
	return "en-US"
}
func fallback[T ~string | ~int | ~bool](value *T, defaultValue T) T {
	if value != nil {
		return *value
	}
	return defaultValue
}
func response[T any](data any, err error) (T, error) {
	var out T
	if err != nil {
		return out, err
	}
	encoded, err := json.Marshal(respond.Envelope{Code: domain.CodeOK, Msg: "ok", Data: data})
	if err != nil {
		return out, err
	}
	err = respond.DecodeResponse(encoded, &out)
	return out, err
}

var _ publicapi.StrictServerInterface = (*Handler)(nil)

func (h *Handler) ListCatalogChanges(ctx context.Context, request publicapi.ListCatalogChangesRequestObject) (publicapi.ListCatalogChangesResponseObject, error) {
	if h.Snapshot == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Snapshot.Changes(requestContext(ctx), request.Params.Since, fallback(request.Params.Limit, 500))
	return response[publicapi.ListCatalogChanges200JSONResponse](data, err)
}

func (h *Handler) GetCatalogSnapshot(ctx context.Context, request publicapi.GetCatalogSnapshotRequestObject) (publicapi.GetCatalogSnapshotResponseObject, error) {
	if h.Snapshot == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Snapshot.Latest(requestContext(ctx))
	return response[publicapi.GetCatalogSnapshot200JSONResponse](data, err)
}

func (h *Handler) ListCategories(ctx context.Context, request publicapi.ListCategoriesRequestObject) (publicapi.ListCategoriesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.Categories(requestContext(ctx), locale(ctx, request.Params.Locale))
	return response[publicapi.ListCategories200JSONResponse](data, err)
}

func (h *Handler) ListCollections(ctx context.Context, request publicapi.ListCollectionsRequestObject) (publicapi.ListCollectionsResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	if err := h.contentCache(ctx); err != nil {
		return nil, err
	}
	data, err := h.Catalog.Collections(requestContext(ctx), locale(ctx, request.Params.Locale), fallback(request.Params.Current, 1), fallback(request.Params.Size, 20))
	return response[publicapi.ListCollections200JSONResponse](data, err)
}

func (h *Handler) GetCollection(ctx context.Context, request publicapi.GetCollectionRequestObject) (publicapi.GetCollectionResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	if err := h.contentCache(ctx); err != nil {
		return nil, err
	}
	data, err := h.Catalog.Collection(requestContext(ctx), request.Slug, locale(ctx, request.Params.Locale))
	return response[publicapi.GetCollection200JSONResponse](data, err)
}

func (h *Handler) GetCollectionBrewfile(ctx context.Context, request publicapi.GetCollectionBrewfileRequestObject) (publicapi.GetCollectionBrewfileResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	if err := h.contentCache(ctx); err != nil {
		return nil, err
	}
	data, err := h.Catalog.Brewfile(requestContext(ctx), request.Slug)
	if err != nil {
		return nil, err
	}
	return publicapi.GetCollectionBrewfile200TextResponse(data), nil
}

func (h *Handler) GetClientConfig(ctx context.Context, request publicapi.GetClientConfigRequestObject) (publicapi.GetClientConfigResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.Config(requestContext(ctx), string(request.Params.Platform), fallback(request.Params.Version, ""), locale(ctx, request.Params.Locale))
	return response[publicapi.GetClientConfig200JSONResponse](data, err)
}

func (h *Handler) GetLatestDesktopRelease(ctx context.Context, request publicapi.GetLatestDesktopReleaseRequestObject) (publicapi.GetLatestDesktopReleaseResponseObject, error) {
	if h.Desktop == nil {
		return nil, domain.Internal(nil)
	}
	channel := "stable"
	if request.Params.Channel != nil {
		channel = string(*request.Params.Channel)
	}
	data, err := h.Desktop.Latest(requestContext(ctx), channel, locale(ctx, request.Params.Locale))
	return response[publicapi.GetLatestDesktopRelease200JSONResponse](data, err)
}

func (h *Handler) CreateFeedback(ctx context.Context, request publicapi.CreateFeedbackRequestObject) (publicapi.CreateFeedbackResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	id, err := h.Catalog.Feedback(requestContext(ctx), *request.Body)
	return response[publicapi.CreateFeedback200JSONResponse](struct {
		ID int64 `json:"id"`
	}{id}, err)
}

func (h *Handler) GetHome(ctx context.Context, request publicapi.GetHomeRequestObject) (publicapi.GetHomeResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	if err := h.contentCache(ctx); err != nil {
		return nil, err
	}
	data, err := h.Catalog.Home(requestContext(ctx), locale(ctx, request.Params.Locale))
	return response[publicapi.GetHome200JSONResponse](data, err)
}

func (h *Handler) ListPackages(ctx context.Context, request publicapi.ListPackagesRequestObject) (publicapi.ListPackagesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	p := request.Params
	data, err := h.Catalog.List(requestContext(ctx), store.PublicFilter{Kind: string(fallback(p.Kind, "")), Category: fallback(p.Category, ""), Sort: string(fallback(p.Sort, "popular")), Locale: locale(ctx, p.Locale), Current: fallback(p.Current, 1), Size: fallback(p.Size, 24), IncludeFonts: fallback(p.IncludeFonts, true), IncludeLibraries: fallback(p.IncludeLibraries, false), IncludeDisabled: fallback(p.IncludeDisabled, false)})
	return response[publicapi.ListPackages200JSONResponse](data, err)
}

func (h *Handler) LookupPackages(ctx context.Context, request publicapi.LookupPackagesRequestObject) (publicapi.LookupPackagesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	data, err := h.Catalog.Lookup(requestContext(ctx), request.Body.Items, locale(ctx, request.Params.Locale))
	return response[publicapi.LookupPackages200JSONResponse](data, err)
}

func (h *Handler) GetPackage(ctx context.Context, request publicapi.GetPackageRequestObject) (publicapi.GetPackageResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.Detail(requestContext(ctx), string(request.Kind), request.Token, locale(ctx, request.Params.Locale))
	return response[publicapi.GetPackage200JSONResponse](data, err)
}

func (h *Handler) GetPackageDependencies(ctx context.Context, request publicapi.GetPackageDependenciesRequestObject) (publicapi.GetPackageDependenciesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.Dependencies(requestContext(ctx), string(request.Kind), request.Token, locale(ctx, request.Params.Locale), fallback(request.Params.Depth, 2), fallback(request.Params.Platform, ""))
	return response[publicapi.GetPackageDependencies200JSONResponse](data, err)
}

func (h *Handler) ListRelatedPackages(ctx context.Context, request publicapi.ListRelatedPackagesRequestObject) (publicapi.ListRelatedPackagesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.Related(requestContext(ctx), string(request.Kind), request.Token, locale(ctx, request.Params.Locale), fallback(request.Params.Limit, 6))
	return response[publicapi.ListRelatedPackages200JSONResponse](data, err)
}

func (h *Handler) ListPackageReleases(ctx context.Context, request publicapi.ListPackageReleasesRequestObject) (publicapi.ListPackageReleasesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	l := locale(ctx, request.Params.Locale)
	// The UI still calls this "Original"; consistently prefer English release notes.
	if fallback(request.Params.Original, false) {
		l = "en-US"
	}
	data, err := h.Catalog.ReleasePage(requestContext(ctx), string(request.Kind), request.Token, l, fallback(request.Params.Current, 1), fallback(request.Params.Size, 20), fallback(request.Params.OnlyWithNotes, false))
	return response[publicapi.ListPackageReleases200JSONResponse](data, err)
}

func (h *Handler) ListRankings(ctx context.Context, request publicapi.ListRankingsRequestObject) (publicapi.ListRankingsResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	p := request.Params
	data, err := h.Catalog.Rankings(requestContext(ctx), store.PublicFilter{Kind: string(p.Kind), Period: string(fallback(p.Period, "30d")), Category: fallback(p.Category, ""), Locale: locale(ctx, p.Locale), Current: fallback(p.Current, 1), Size: fallback(p.Size, 24), IncludeFonts: fallback(p.IncludeFonts, true)})
	return response[publicapi.ListRankings200JSONResponse](data, err)
}

func (h *Handler) SearchPackages(ctx context.Context, request publicapi.SearchPackagesRequestObject) (publicapi.SearchPackagesResponseObject, error) {
	if h.Catalog == nil || h.Search == nil {
		return nil, domain.Internal(nil)
	}
	p := request.Params
	l := locale(ctx, p.Locale)
	result, err := h.Search.Search(requestContext(ctx), domain.SearchInput{Query: p.Q, Kind: string(fallback(p.Kind, "")), Category: fallback(p.Category, ""), Locale: l, Platform: string(fallback(p.Platform, "web")), Current: fallback(p.Current, 1), Size: fallback(p.Size, 24), IncludeDisabled: fallback(p.IncludeDisabled, false)})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(result.Records))
	for _, r := range result.Records {
		ids = append(ids, r.ID)
	}
	packages, err := h.Catalog.SummariesByID(requestContext(ctx), ids, l)
	if err != nil {
		return nil, err
	}
	out := publicapi.SearchResponse{Code: domain.CodeOK, Msg: "ok"}
	out.Data.Current = fallback(p.Current, 1)
	out.Data.Size = fallback(p.Size, 24)
	out.Data.Total = int(result.Total)
	out.Data.QueryId = result.QueryID
	out.Data.ExpandedTerms = &result.ExpandedTerms
	out.Data.Records = []publicapi.SearchHit{}
	for _, r := range result.Records {
		if pkg, exists := packages[r.ID]; exists {
			out.Data.Records = append(out.Data.Records, publicapi.SearchHit{Package: pkg, Score: float32(r.Score), MatchedName: r.MatchedName})
		}
	}
	return publicapi.SearchPackages200JSONResponse{Body: out}, nil
}

func (h *Handler) ReportSearchClick(ctx context.Context, request publicapi.ReportSearchClickRequestObject) (publicapi.ReportSearchClickResponseObject, error) {
	if h.Search == nil {
		return nil, domain.Internal(nil)
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	r := request.Body
	err := h.Search.Click(requestContext(ctx), r.QueryId, string(r.Kind), r.Token, r.Position)
	return response[publicapi.ReportSearchClick200JSONResponse](nil, err)
}

func (h *Handler) Suggest(ctx context.Context, request publicapi.SuggestRequestObject) (publicapi.SuggestResponseObject, error) {
	if h.Search == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Search.Suggest(requestContext(ctx), request.Params.Q, locale(ctx, request.Params.Locale), fallback(request.Params.Limit, 8))
	return response[publicapi.Suggest200JSONResponse](data, err)
}

func (h *Handler) ListSitemapPackages(ctx context.Context, request publicapi.ListSitemapPackagesRequestObject) (publicapi.ListSitemapPackagesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	group := ""
	if request.Params.Group != nil {
		group = string(*request.Params.Group)
	}
	data, err := h.Catalog.Sitemap(requestContext(ctx), group, fallback(request.Params.Current, 1), fallback(request.Params.Size, 5000))
	return response[publicapi.ListSitemapPackages200JSONResponse](data, err)
}

func (h *Handler) GetPackageLifecycle(ctx context.Context, request publicapi.GetPackageLifecycleRequestObject) (publicapi.GetPackageLifecycleResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.Lifecycle(requestContext(ctx), string(request.Kind), request.Token, locale(ctx, request.Params.Locale))
	return response[publicapi.GetPackageLifecycle200JSONResponse](data, err)
}

func (h *Handler) ListArticles(ctx context.Context, request publicapi.ListArticlesRequestObject) (publicapi.ListArticlesResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	current, size, kind := 1, 12, ""
	if request.Params.Current != nil {
		current = *request.Params.Current
	}
	if request.Params.Size != nil {
		size = *request.Params.Size
	}
	if request.Params.Type != nil {
		kind = string(*request.Params.Type)
	}
	data, err := h.Catalog.Articles(requestContext(ctx), locale(ctx, request.Params.Locale), kind, current, size)
	return response[publicapi.ListArticles200JSONResponse](data, err)
}

func (h *Handler) GetArticle(ctx context.Context, request publicapi.GetArticleRequestObject) (publicapi.GetArticleResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.Article(requestContext(ctx), request.Slug, locale(ctx, request.Params.Locale))
	return response[publicapi.GetArticle200JSONResponse](data, err)
}

func (h *Handler) GetAuthor(ctx context.Context, request publicapi.GetAuthorRequestObject) (publicapi.GetAuthorResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	current, size := 1, 12
	if request.Params.Current != nil {
		current = *request.Params.Current
	}
	if request.Params.Size != nil {
		size = *request.Params.Size
	}
	data, err := h.Catalog.Author(requestContext(ctx), request.Slug, locale(ctx, request.Params.Locale), current, size)
	return response[publicapi.GetAuthor200JSONResponse](data, err)
}

func (h *Handler) GetArticleSitemap(ctx context.Context, request publicapi.GetArticleSitemapRequestObject) (publicapi.GetArticleSitemapResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.ArticleSitemap(requestContext(ctx))
	return response[publicapi.GetArticleSitemap200JSONResponse](data, err)
}

func (h *Handler) GetSiteSettings(ctx context.Context, request publicapi.GetSiteSettingsRequestObject) (publicapi.GetSiteSettingsResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.SiteSettings(requestContext(ctx))
	return response[publicapi.GetSiteSettings200JSONResponse](data, err)
}

func (h *Handler) VerifyIndexNowKey(ctx context.Context, request publicapi.VerifyIndexNowKeyRequestObject) (publicapi.VerifyIndexNowKeyResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.VerifyIndexNowKey(requestContext(ctx), request.Key)
	return response[publicapi.VerifyIndexNowKey200JSONResponse](data, err)
}

func (h *Handler) contentCache(ctx context.Context) error {
	policy, err := h.Catalog.ContentCachePolicy(requestContext(ctx))
	if err != nil {
		return err
	}
	if c, ok := ctx.(*gin.Context); ok {
		c.Set("publicCachePolicy", policy)
	}
	return nil
}
