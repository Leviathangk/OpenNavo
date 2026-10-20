package admin

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agent"
)

func (h *Handler) UpdatePackageText(ctx context.Context, request adminapi.UpdatePackageTextRequestObject) (adminapi.UpdatePackageTextResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdatePackageText", request)
	return response[adminapi.UpdatePackageText200JSONResponse](data, err)
}

func (h *Handler) UpsertReleaseNotes(ctx context.Context, request adminapi.UpsertReleaseNotesRequestObject) (adminapi.UpsertReleaseNotesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpsertReleaseNotes", request)
	return response[adminapi.UpsertReleaseNotes200JSONResponse](data, err)
}

func (h *Handler) ListTranslations(ctx context.Context, request adminapi.ListTranslationsRequestObject) (adminapi.ListTranslationsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListTranslations", request)
	return response[adminapi.ListTranslations200JSONResponse](data, err)
}

func (h *Handler) GetTranslationStatus(ctx context.Context, request adminapi.GetTranslationStatusRequestObject) (adminapi.GetTranslationStatusResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetTranslationStatus", request)
	return response[adminapi.GetTranslationStatus200JSONResponse](data, err)
}

func (h *Handler) FixTranslation(ctx context.Context, request adminapi.FixTranslationRequestObject) (adminapi.FixTranslationResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "FixTranslation", request)
	return response[adminapi.FixTranslation200JSONResponse](data, err)
}

func (h *Handler) ListGlossary(ctx context.Context, request adminapi.ListGlossaryRequestObject) (adminapi.ListGlossaryResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListGlossary", request)
	return response[adminapi.ListGlossary200JSONResponse](data, err)
}

func (h *Handler) UpsertGlossaryTerm(ctx context.Context, request adminapi.UpsertGlossaryTermRequestObject) (adminapi.UpsertGlossaryTermResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpsertGlossaryTerm", request)
	return response[adminapi.UpsertGlossaryTerm200JSONResponse](data, err)
}

func (h *Handler) DeleteGlossaryTerm(ctx context.Context, request adminapi.DeleteGlossaryTermRequestObject) (adminapi.DeleteGlossaryTermResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "DeleteGlossaryTerm", request)
	return response[adminapi.DeleteGlossaryTerm200JSONResponse](data, err)
}

func (h *Handler) GetAnnouncement(ctx context.Context, request adminapi.GetAnnouncementRequestObject) (adminapi.GetAnnouncementResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAnnouncement", request)
	return response[adminapi.GetAnnouncement200JSONResponse](data, err)
}

func (h *Handler) UpdateAnnouncement(ctx context.Context, request adminapi.UpdateAnnouncementRequestObject) (adminapi.UpdateAnnouncementResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateAnnouncement", request)
	return response[adminapi.UpdateAnnouncement200JSONResponse](data, err)
}

func (h *Handler) UpdateDesktopReleaseNotes(ctx context.Context, request adminapi.UpdateDesktopReleaseNotesRequestObject) (adminapi.UpdateDesktopReleaseNotesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateDesktopReleaseNotes", request)
	return response[adminapi.UpdateDesktopReleaseNotes200JSONResponse](data, err)
}

func (h *Handler) ListAdminCatalogChanges(ctx context.Context, request adminapi.ListAdminCatalogChangesRequestObject) (adminapi.ListAdminCatalogChangesResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAdminCatalogChanges", request)
	return response[adminapi.ListAdminCatalogChanges200JSONResponse](data, err)
}

func (h *Handler) GetAgentSettings(ctx context.Context, request adminapi.GetAgentSettingsRequestObject) (adminapi.GetAgentSettingsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAgentSettings", request)
	return response[adminapi.GetAgentSettings200JSONResponse](data, err)
}

func (h *Handler) UpdateAgentSettings(ctx context.Context, request adminapi.UpdateAgentSettingsRequestObject) (adminapi.UpdateAgentSettingsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateAgentSettings", request)
	return response[adminapi.UpdateAgentSettings200JSONResponse](data, err)
}

func (h *Handler) ListAgentClients(ctx context.Context, request adminapi.ListAgentClientsRequestObject) (adminapi.ListAgentClientsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAgentClients", request)
	return response[adminapi.ListAgentClients200JSONResponse](data, err)
}

func (h *Handler) CreateAgentClient(ctx context.Context, request adminapi.CreateAgentClientRequestObject) (adminapi.CreateAgentClientResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateAgentClient", request)
	return response[adminapi.CreateAgentClient200JSONResponse](data, err)
}

func (h *Handler) GetAgentClient(ctx context.Context, request adminapi.GetAgentClientRequestObject) (adminapi.GetAgentClientResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAgentClient", request)
	return response[adminapi.GetAgentClient200JSONResponse](data, err)
}

func (h *Handler) UpdateAgentClient(ctx context.Context, request adminapi.UpdateAgentClientRequestObject) (adminapi.UpdateAgentClientResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateAgentClient", request)
	return response[adminapi.UpdateAgentClient200JSONResponse](data, err)
}

func (h *Handler) ListAgentTokens(ctx context.Context, request adminapi.ListAgentTokensRequestObject) (adminapi.ListAgentTokensResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAgentTokens", request)
	return response[adminapi.ListAgentTokens200JSONResponse](data, err)
}

func (h *Handler) CreateAgentToken(ctx context.Context, request adminapi.CreateAgentTokenRequestObject) (adminapi.CreateAgentTokenResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "CreateAgentToken", request)
	return response[adminapi.CreateAgentToken200JSONResponse](data, err)
}

func (h *Handler) UpdateAgentToken(ctx context.Context, request adminapi.UpdateAgentTokenRequestObject) (adminapi.UpdateAgentTokenResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "UpdateAgentToken", request)
	return response[adminapi.UpdateAgentToken200JSONResponse](data, err)
}

func (h *Handler) RevokeAgentToken(ctx context.Context, request adminapi.RevokeAgentTokenRequestObject) (adminapi.RevokeAgentTokenResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RevokeAgentToken", request)
	return response[adminapi.RevokeAgentToken200JSONResponse](data, err)
}

func (h *Handler) IntrospectAgentToken(ctx context.Context, request adminapi.IntrospectAgentTokenRequestObject) (adminapi.IntrospectAgentTokenResponseObject, error) {
	c, ok := ctx.(*gin.Context)
	if !ok {
		return nil, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: 403}
	}
	value, exists := c.Get("agentIdentity")
	svc := h.agentService()
	if !exists || svc == nil {
		return nil, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: 403}
	}
	id, ok := value.(agent.Identity)
	if !ok {
		return nil, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: 403}
	}
	data, err := svc.Introspect(requestContext(ctx), id)
	return response[adminapi.IntrospectAgentToken200JSONResponse](data, err)
}

func (h *Handler) ListContentRevisions(ctx context.Context, request adminapi.ListContentRevisionsRequestObject) (adminapi.ListContentRevisionsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListContentRevisions", request)
	return response[adminapi.ListContentRevisions200JSONResponse](data, err)
}

func (h *Handler) GetContentRevision(ctx context.Context, request adminapi.GetContentRevisionRequestObject) (adminapi.GetContentRevisionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetContentRevision", request)
	return response[adminapi.GetContentRevision200JSONResponse](data, err)
}

func (h *Handler) RestoreRevision(ctx context.Context, request adminapi.RestoreRevisionRequestObject) (adminapi.RestoreRevisionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RestoreRevision", request)
	return response[adminapi.RestoreRevision200JSONResponse](data, err)
}

func (h *Handler) ListTrash(ctx context.Context, request adminapi.ListTrashRequestObject) (adminapi.ListTrashResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListTrash", request)
	return response[adminapi.ListTrash200JSONResponse](data, err)
}

func (h *Handler) GetTrashItem(ctx context.Context, request adminapi.GetTrashItemRequestObject) (adminapi.GetTrashItemResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetTrashItem", request)
	return response[adminapi.GetTrashItem200JSONResponse](data, err)
}

func (h *Handler) RestoreTrash(ctx context.Context, request adminapi.RestoreTrashRequestObject) (adminapi.RestoreTrashResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RestoreTrash", request)
	return response[adminapi.RestoreTrash200JSONResponse](data, err)
}

func (h *Handler) RevertRequest(ctx context.Context, request adminapi.RevertRequestRequestObject) (adminapi.RevertRequestResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "RevertRequest", request)
	return response[adminapi.RevertRequest200JSONResponse](data, err)
}

func (h *Handler) ListAgentCalls(ctx context.Context, request adminapi.ListAgentCallsRequestObject) (adminapi.ListAgentCallsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAgentCalls", request)
	return response[adminapi.ListAgentCalls200JSONResponse](data, err)
}

func (h *Handler) GetAgentCall(ctx context.Context, request adminapi.GetAgentCallRequestObject) (adminapi.GetAgentCallResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetAgentCall", request)
	return response[adminapi.GetAgentCall200JSONResponse](data, err)
}

func (h *Handler) ListTranslationLogs(ctx context.Context, request adminapi.ListTranslationLogsRequestObject) (adminapi.ListTranslationLogsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListTranslationLogs", request)
	return response[adminapi.ListTranslationLogs200JSONResponse](data, err)
}

func (h *Handler) GetTranslationLog(ctx context.Context, request adminapi.GetTranslationLogRequestObject) (adminapi.GetTranslationLogResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "GetTranslationLog", request)
	return response[adminapi.GetTranslationLog200JSONResponse](data, err)
}

func (h *Handler) ListAdminVersions(ctx context.Context, request adminapi.ListAdminVersionsRequestObject) (adminapi.ListAdminVersionsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.manage(ctx, "ListAdminVersions", request)
	return response[adminapi.ListAdminVersions200JSONResponse](data, err)
}
