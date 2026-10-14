package admin

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
)

// Handler aggregates admin business services; generated strict interfaces enforce the HTTP contract.
type Handler struct {
	Management *management.Service
	Auth       *auth.Service
	Assets     *assets.Service
	CIToken    string
}

var _ adminapi.StrictServerInterface = (*Handler)(nil)

func (h *Handler) ListAppConfig(ctx context.Context, request adminapi.ListAppConfigRequestObject) (adminapi.ListAppConfigResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAppConfig", request)
	return response[adminapi.ListAppConfig200JSONResponse](data, err)
}

func (h *Handler) UpdateAppConfig(ctx context.Context, request adminapi.UpdateAppConfigRequestObject) (adminapi.UpdateAppConfigResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateAppConfig", request)
	return response[adminapi.UpdateAppConfig200JSONResponse](data, err)
}

func (h *Handler) ListAssets(ctx context.Context, request adminapi.ListAssetsRequestObject) (adminapi.ListAssetsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAssets", request)
	return response[adminapi.ListAssets200JSONResponse](data, err)
}

func (h *Handler) CreateCategory(ctx context.Context, request adminapi.CreateCategoryRequestObject) (adminapi.CreateCategoryResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateCategory", request)
	return response[adminapi.CreateCategory200JSONResponse](data, err)
}

func (h *Handler) ReorderCategories(ctx context.Context, request adminapi.ReorderCategoriesRequestObject) (adminapi.ReorderCategoriesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ReorderCategories", request)
	return response[adminapi.ReorderCategories200JSONResponse](data, err)
}

func (h *Handler) GetCategoryTree(ctx context.Context, request adminapi.GetCategoryTreeRequestObject) (adminapi.GetCategoryTreeResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetCategoryTree", request)
	return response[adminapi.GetCategoryTree200JSONResponse](data, err)
}

func (h *Handler) DeleteCategory(ctx context.Context, request adminapi.DeleteCategoryRequestObject) (adminapi.DeleteCategoryResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteCategory", request)
	return response[adminapi.DeleteCategory200JSONResponse](data, err)
}

func (h *Handler) UpdateCategory(ctx context.Context, request adminapi.UpdateCategoryRequestObject) (adminapi.UpdateCategoryResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateCategory", request)
	return response[adminapi.UpdateCategory200JSONResponse](data, err)
}

func (h *Handler) ListAdminCollections(ctx context.Context, request adminapi.ListAdminCollectionsRequestObject) (adminapi.ListAdminCollectionsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAdminCollections", request)
	return response[adminapi.ListAdminCollections200JSONResponse](data, err)
}

func (h *Handler) CreateCollection(ctx context.Context, request adminapi.CreateCollectionRequestObject) (adminapi.CreateCollectionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateCollection", request)
	return response[adminapi.CreateCollection200JSONResponse](data, err)
}

func (h *Handler) DeleteCollection(ctx context.Context, request adminapi.DeleteCollectionRequestObject) (adminapi.DeleteCollectionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteCollection", request)
	return response[adminapi.DeleteCollection200JSONResponse](data, err)
}

func (h *Handler) GetAdminCollection(ctx context.Context, request adminapi.GetAdminCollectionRequestObject) (adminapi.GetAdminCollectionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAdminCollection", request)
	return response[adminapi.GetAdminCollection200JSONResponse](data, err)
}

func (h *Handler) UpdateCollection(ctx context.Context, request adminapi.UpdateCollectionRequestObject) (adminapi.UpdateCollectionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateCollection", request)
	return response[adminapi.UpdateCollection200JSONResponse](data, err)
}

func (h *Handler) SetCollectionItems(ctx context.Context, request adminapi.SetCollectionItemsRequestObject) (adminapi.SetCollectionItemsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetCollectionItems", request)
	return response[adminapi.SetCollectionItems200JSONResponse](data, err)
}

func (h *Handler) PublishCollection(ctx context.Context, request adminapi.PublishCollectionRequestObject) (adminapi.PublishCollectionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "PublishCollection", request)
	return response[adminapi.PublishCollection200JSONResponse](data, err)
}

func (h *Handler) UnpublishCollection(ctx context.Context, request adminapi.UnpublishCollectionRequestObject) (adminapi.UnpublishCollectionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UnpublishCollection", request)
	return response[adminapi.UnpublishCollection200JSONResponse](data, err)
}

func (h *Handler) GetLlmUsage(ctx context.Context, request adminapi.GetLlmUsageRequestObject) (adminapi.GetLlmUsageResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetLlmUsage", request)
	return response[adminapi.GetLlmUsage200JSONResponse](data, err)
}

func (h *Handler) GetDashboardOverview(ctx context.Context, request adminapi.GetDashboardOverviewRequestObject) (adminapi.GetDashboardOverviewResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetDashboardOverview", request)
	return response[adminapi.GetDashboardOverview200JSONResponse](data, err)
}

func (h *Handler) ListDesktopReleases(ctx context.Context, request adminapi.ListDesktopReleasesRequestObject) (adminapi.ListDesktopReleasesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListDesktopReleases", request)
	return response[adminapi.ListDesktopReleases200JSONResponse](data, err)
}

func (h *Handler) CreateDesktopRelease(ctx context.Context, request adminapi.CreateDesktopReleaseRequestObject) (adminapi.CreateDesktopReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateDesktopRelease", request)
	return response[adminapi.CreateDesktopRelease200JSONResponse](data, err)
}

func (h *Handler) GetDesktopRelease(ctx context.Context, request adminapi.GetDesktopReleaseRequestObject) (adminapi.GetDesktopReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetDesktopRelease", request)
	return response[adminapi.GetDesktopRelease200JSONResponse](data, err)
}

func (h *Handler) UpdateDesktopRelease(ctx context.Context, request adminapi.UpdateDesktopReleaseRequestObject) (adminapi.UpdateDesktopReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateDesktopRelease", request)
	return response[adminapi.UpdateDesktopRelease200JSONResponse](data, err)
}

func (h *Handler) PublishDesktopRelease(ctx context.Context, request adminapi.PublishDesktopReleaseRequestObject) (adminapi.PublishDesktopReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "PublishDesktopRelease", request)
	return response[adminapi.PublishDesktopRelease200JSONResponse](data, err)
}

func (h *Handler) RollbackDesktopRelease(ctx context.Context, request adminapi.RollbackDesktopReleaseRequestObject) (adminapi.RollbackDesktopReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RollbackDesktopRelease", request)
	return response[adminapi.RollbackDesktopRelease200JSONResponse](data, err)
}

func (h *Handler) ListFeatures(ctx context.Context, request adminapi.ListFeaturesRequestObject) (adminapi.ListFeaturesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListFeatures", request)
	return response[adminapi.ListFeatures200JSONResponse](data, err)
}

func (h *Handler) CreateFeature(ctx context.Context, request adminapi.CreateFeatureRequestObject) (adminapi.CreateFeatureResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateFeature", request)
	return response[adminapi.CreateFeature200JSONResponse](data, err)
}

func (h *Handler) DeleteFeature(ctx context.Context, request adminapi.DeleteFeatureRequestObject) (adminapi.DeleteFeatureResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteFeature", request)
	return response[adminapi.DeleteFeature200JSONResponse](data, err)
}

func (h *Handler) GetFeature(ctx context.Context, request adminapi.GetFeatureRequestObject) (adminapi.GetFeatureResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetFeature", request)
	return response[adminapi.GetFeature200JSONResponse](data, err)
}

func (h *Handler) UpdateFeature(ctx context.Context, request adminapi.UpdateFeatureRequestObject) (adminapi.UpdateFeatureResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateFeature", request)
	return response[adminapi.UpdateFeature200JSONResponse](data, err)
}

func (h *Handler) ListFeedback(ctx context.Context, request adminapi.ListFeedbackRequestObject) (adminapi.ListFeedbackResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListFeedback", request)
	return response[adminapi.ListFeedback200JSONResponse](data, err)
}

func (h *Handler) GetFeedback(ctx context.Context, request adminapi.GetFeedbackRequestObject) (adminapi.GetFeedbackResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetFeedback", request)
	return response[adminapi.GetFeedback200JSONResponse](data, err)
}

func (h *Handler) UpdateFeedback(ctx context.Context, request adminapi.UpdateFeedbackRequestObject) (adminapi.UpdateFeedbackResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateFeedback", request)
	return response[adminapi.UpdateFeedback200JSONResponse](data, err)
}

func (h *Handler) ListQueues(ctx context.Context, request adminapi.ListQueuesRequestObject) (adminapi.ListQueuesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListQueues", request)
	return response[adminapi.ListQueues200JSONResponse](data, err)
}

func (h *Handler) ListJobRuns(ctx context.Context, request adminapi.ListJobRunsRequestObject) (adminapi.ListJobRunsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListJobRuns", request)
	return response[adminapi.ListJobRuns200JSONResponse](data, err)
}

func (h *Handler) GetJobRun(ctx context.Context, request adminapi.GetJobRunRequestObject) (adminapi.GetJobRunResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetJobRun", request)
	return response[adminapi.GetJobRun200JSONResponse](data, err)
}

func (h *Handler) TriggerJob(ctx context.Context, request adminapi.TriggerJobRequestObject) (adminapi.TriggerJobResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "TriggerJob", request)
	return response[adminapi.TriggerJob200JSONResponse](data, err)
}

func (h *Handler) ListMirrors(ctx context.Context, request adminapi.ListMirrorsRequestObject) (adminapi.ListMirrorsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListMirrors", request)
	return response[adminapi.ListMirrors200JSONResponse](data, err)
}

func (h *Handler) CreateMirror(ctx context.Context, request adminapi.CreateMirrorRequestObject) (adminapi.CreateMirrorResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateMirror", request)
	return response[adminapi.CreateMirror200JSONResponse](data, err)
}

func (h *Handler) DeleteMirror(ctx context.Context, request adminapi.DeleteMirrorRequestObject) (adminapi.DeleteMirrorResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteMirror", request)
	return response[adminapi.DeleteMirror200JSONResponse](data, err)
}

func (h *Handler) UpdateMirror(ctx context.Context, request adminapi.UpdateMirrorRequestObject) (adminapi.UpdateMirrorResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateMirror", request)
	return response[adminapi.UpdateMirror200JSONResponse](data, err)
}

func (h *Handler) ListAdminPackages(ctx context.Context, request adminapi.ListAdminPackagesRequestObject) (adminapi.ListAdminPackagesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAdminPackages", request)
	return response[adminapi.ListAdminPackages200JSONResponse](data, err)
}

func (h *Handler) GetAdminPackage(ctx context.Context, request adminapi.GetAdminPackageRequestObject) (adminapi.GetAdminPackageResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAdminPackage", request)
	return response[adminapi.GetAdminPackage200JSONResponse](data, err)
}

func (h *Handler) SetPackageCategories(ctx context.Context, request adminapi.SetPackageCategoriesRequestObject) (adminapi.SetPackageCategoriesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageCategories", request)
	return response[adminapi.SetPackageCategories200JSONResponse](data, err)
}

func (h *Handler) ListChangelogSources(ctx context.Context, request adminapi.ListChangelogSourcesRequestObject) (adminapi.ListChangelogSourcesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListChangelogSources", request)
	return response[adminapi.ListChangelogSources200JSONResponse](data, err)
}

func (h *Handler) UpdatePackageI18n(ctx context.Context, request adminapi.UpdatePackageI18nRequestObject) (adminapi.UpdatePackageI18nResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdatePackageI18n", request)
	return response[adminapi.UpdatePackageI18n200JSONResponse](data, err)
}

func (h *Handler) DeletePackageIcon(ctx context.Context, request adminapi.DeletePackageIconRequestObject) (adminapi.DeletePackageIconResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeletePackageIcon", request)
	return response[adminapi.DeletePackageIcon200JSONResponse](data, err)
}

func (h *Handler) SetPackageIcon(ctx context.Context, request adminapi.SetPackageIconRequestObject) (adminapi.SetPackageIconResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetPackageIcon", request)
	return response[adminapi.SetPackageIcon200JSONResponse](data, err)
}

func (h *Handler) UpdatePackageMeta(ctx context.Context, request adminapi.UpdatePackageMetaRequestObject) (adminapi.UpdatePackageMetaResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdatePackageMeta", request)
	return response[adminapi.UpdatePackageMeta200JSONResponse](data, err)
}

func (h *Handler) ResyncPackage(ctx context.Context, request adminapi.ResyncPackageRequestObject) (adminapi.ResyncPackageResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ResyncPackage", request)
	return response[adminapi.ResyncPackage200JSONResponse](data, err)
}

func (h *Handler) ListTapPackages(ctx context.Context, request adminapi.ListTapPackagesRequestObject) (adminapi.ListTapPackagesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListTapPackages", request)
	return response[adminapi.ListTapPackages200JSONResponse](data, err)
}

func (h *Handler) AddTapPackage(ctx context.Context, request adminapi.AddTapPackageRequestObject) (adminapi.AddTapPackageResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "AddTapPackage", request)
	return response[adminapi.AddTapPackage200JSONResponse](data, err)
}

func (h *Handler) RemoveTapPackage(ctx context.Context, request adminapi.RemoveTapPackageRequestObject) (adminapi.RemoveTapPackageResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RemoveTapPackage", request)
	return response[adminapi.RemoveTapPackage200JSONResponse](data, err)
}

func (h *Handler) ListPackageScreenshots(ctx context.Context, request adminapi.ListPackageScreenshotsRequestObject) (adminapi.ListPackageScreenshotsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListPackageScreenshots", request)
	return response[adminapi.ListPackageScreenshots200JSONResponse](data, err)
}

func (h *Handler) AddPackageScreenshot(ctx context.Context, request adminapi.AddPackageScreenshotRequestObject) (adminapi.AddPackageScreenshotResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "AddPackageScreenshot", request)
	return response[adminapi.AddPackageScreenshot200JSONResponse](data, err)
}

func (h *Handler) ReorderPackageScreenshots(ctx context.Context, request adminapi.ReorderPackageScreenshotsRequestObject) (adminapi.ReorderPackageScreenshotsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ReorderPackageScreenshots", request)
	return response[adminapi.ReorderPackageScreenshots200JSONResponse](data, err)
}

func (h *Handler) DeletePackageScreenshot(ctx context.Context, request adminapi.DeletePackageScreenshotRequestObject) (adminapi.DeletePackageScreenshotResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeletePackageScreenshot", request)
	return response[adminapi.DeletePackageScreenshot200JSONResponse](data, err)
}

func (h *Handler) UpdatePackageScreenshot(ctx context.Context, request adminapi.UpdatePackageScreenshotRequestObject) (adminapi.UpdatePackageScreenshotResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdatePackageScreenshot", request)
	return response[adminapi.UpdatePackageScreenshot200JSONResponse](data, err)
}

func (h *Handler) ListPackageVersions(ctx context.Context, request adminapi.ListPackageVersionsRequestObject) (adminapi.ListPackageVersionsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListPackageVersions", request)
	return response[adminapi.ListPackageVersions200JSONResponse](data, err)
}

func (h *Handler) ListAdminReleases(ctx context.Context, request adminapi.ListAdminReleasesRequestObject) (adminapi.ListAdminReleasesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAdminReleases", request)
	return response[adminapi.ListAdminReleases200JSONResponse](data, err)
}

func (h *Handler) GetAdminRelease(ctx context.Context, request adminapi.GetAdminReleaseRequestObject) (adminapi.GetAdminReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAdminRelease", request)
	return response[adminapi.GetAdminRelease200JSONResponse](data, err)
}

func (h *Handler) UpdateRelease(ctx context.Context, request adminapi.UpdateReleaseRequestObject) (adminapi.UpdateReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateRelease", request)
	return response[adminapi.UpdateRelease200JSONResponse](data, err)
}

func (h *Handler) UpdateReleaseI18n(ctx context.Context, request adminapi.UpdateReleaseI18nRequestObject) (adminapi.UpdateReleaseI18nResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateReleaseI18n", request)
	return response[adminapi.UpdateReleaseI18n200JSONResponse](data, err)
}

func (h *Handler) RetranslateRelease(ctx context.Context, request adminapi.RetranslateReleaseRequestObject) (adminapi.RetranslateReleaseResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RetranslateRelease", request)
	return response[adminapi.RetranslateRelease200JSONResponse](data, err)
}

func (h *Handler) ListTopQueries(ctx context.Context, request adminapi.ListTopQueriesRequestObject) (adminapi.ListTopQueriesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListTopQueries", request)
	return response[adminapi.ListTopQueries200JSONResponse](data, err)
}

func (h *Handler) ListZeroResultQueries(ctx context.Context, request adminapi.ListZeroResultQueriesRequestObject) (adminapi.ListZeroResultQueriesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListZeroResultQueries", request)
	return response[adminapi.ListZeroResultQueries200JSONResponse](data, err)
}

func (h *Handler) ListSynonyms(ctx context.Context, request adminapi.ListSynonymsRequestObject) (adminapi.ListSynonymsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListSynonyms", request)
	return response[adminapi.ListSynonyms200JSONResponse](data, err)
}

func (h *Handler) CreateSynonym(ctx context.Context, request adminapi.CreateSynonymRequestObject) (adminapi.CreateSynonymResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateSynonym", request)
	return response[adminapi.CreateSynonym200JSONResponse](data, err)
}

func (h *Handler) DeleteSynonym(ctx context.Context, request adminapi.DeleteSynonymRequestObject) (adminapi.DeleteSynonymResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteSynonym", request)
	return response[adminapi.DeleteSynonym200JSONResponse](data, err)
}

func (h *Handler) UpdateSynonym(ctx context.Context, request adminapi.UpdateSynonymRequestObject) (adminapi.UpdateSynonymResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateSynonym", request)
	return response[adminapi.UpdateSynonym200JSONResponse](data, err)
}

func (h *Handler) ListAuditLogs(ctx context.Context, request adminapi.ListAuditLogsRequestObject) (adminapi.ListAuditLogsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAuditLogs", request)
	return response[adminapi.ListAuditLogs200JSONResponse](data, err)
}

func (h *Handler) ListPermissions(ctx context.Context, request adminapi.ListPermissionsRequestObject) (adminapi.ListPermissionsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListPermissions", request)
	return response[adminapi.ListPermissions200JSONResponse](data, err)
}

func (h *Handler) ListRoles(ctx context.Context, request adminapi.ListRolesRequestObject) (adminapi.ListRolesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListRoles", request)
	return response[adminapi.ListRoles200JSONResponse](data, err)
}

func (h *Handler) CreateRole(ctx context.Context, request adminapi.CreateRoleRequestObject) (adminapi.CreateRoleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateRole", request)
	return response[adminapi.CreateRole200JSONResponse](data, err)
}

func (h *Handler) DeleteRole(ctx context.Context, request adminapi.DeleteRoleRequestObject) (adminapi.DeleteRoleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteRole", request)
	return response[adminapi.DeleteRole200JSONResponse](data, err)
}

func (h *Handler) UpdateRole(ctx context.Context, request adminapi.UpdateRoleRequestObject) (adminapi.UpdateRoleResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateRole", request)
	return response[adminapi.UpdateRole200JSONResponse](data, err)
}

func (h *Handler) SetRolePermissions(ctx context.Context, request adminapi.SetRolePermissionsRequestObject) (adminapi.SetRolePermissionsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "SetRolePermissions", request)
	return response[adminapi.SetRolePermissions200JSONResponse](data, err)
}

func (h *Handler) ListAdminUsers(ctx context.Context, request adminapi.ListAdminUsersRequestObject) (adminapi.ListAdminUsersResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAdminUsers", request)
	return response[adminapi.ListAdminUsers200JSONResponse](data, err)
}

func (h *Handler) CreateAdminUser(ctx context.Context, request adminapi.CreateAdminUserRequestObject) (adminapi.CreateAdminUserResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateAdminUser", request)
	return response[adminapi.CreateAdminUser200JSONResponse](data, err)
}

func (h *Handler) DeleteAdminUser(ctx context.Context, request adminapi.DeleteAdminUserRequestObject) (adminapi.DeleteAdminUserResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteAdminUser", request)
	return response[adminapi.DeleteAdminUser200JSONResponse](data, err)
}

func (h *Handler) UpdateAdminUser(ctx context.Context, request adminapi.UpdateAdminUserRequestObject) (adminapi.UpdateAdminUserResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateAdminUser", request)
	return response[adminapi.UpdateAdminUser200JSONResponse](data, err)
}

func (h *Handler) ForceLogoutAdminUser(ctx context.Context, request adminapi.ForceLogoutAdminUserRequestObject) (adminapi.ForceLogoutAdminUserResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ForceLogoutAdminUser", request)
	return response[adminapi.ForceLogoutAdminUser200JSONResponse](data, err)
}

func (h *Handler) ResetAdminUserPassword(ctx context.Context, request adminapi.ResetAdminUserPasswordRequestObject) (adminapi.ResetAdminUserPasswordResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ResetAdminUserPassword", request)
	return response[adminapi.ResetAdminUserPassword200JSONResponse](data, err)
}

func (h *Handler) ApproveTranslations(ctx context.Context, request adminapi.ApproveTranslationsRequestObject) (adminapi.ApproveTranslationsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ApproveTranslations", request)
	return response[adminapi.ApproveTranslations200JSONResponse](data, err)
}

func (h *Handler) ListTranslationQueue(ctx context.Context, request adminapi.ListTranslationQueueRequestObject) (adminapi.ListTranslationQueueResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListTranslationQueue", request)
	return response[adminapi.ListTranslationQueue200JSONResponse](data, err)
}

func (h *Handler) GetPackageChangelogSettings(ctx context.Context, request adminapi.GetPackageChangelogSettingsRequestObject) (adminapi.GetPackageChangelogSettingsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetPackageChangelogSettings", request)
	return response[adminapi.GetPackageChangelogSettings200JSONResponse](data, err)
}

func (h *Handler) UpdatePackageChangelogSettings(ctx context.Context, request adminapi.UpdatePackageChangelogSettingsRequestObject) (adminapi.UpdatePackageChangelogSettingsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdatePackageChangelogSettings", request)
	return response[adminapi.UpdatePackageChangelogSettings200JSONResponse](data, err)
}
