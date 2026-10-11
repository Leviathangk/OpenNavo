package admin

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/store"
)

func requestContext(ctx context.Context) context.Context {
	if c, ok := ctx.(*gin.Context); ok {
		return c.Request.Context()
	}
	return ctx
}
func (h *Handler) assetActor(ctx context.Context) (store.AuditActor, error) {
	c, ok := ctx.(*gin.Context)
	if !ok {
		return store.AuditActor{}, &domain.AppError{Code: domain.CodeUnauthenticated, HTTPStatus: 401}
	}
	if value, ok := c.Get("adminActor"); ok {
		if actor, ok := value.(store.AuditActor); ok && actor.ID != nil {
			return actor, nil
		}
	}
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if h.CIToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(h.CIToken)) != 1 {
		return store.AuditActor{}, &domain.AppError{Code: domain.CodeUnauthenticated, HTTPStatus: 401}
	}
	return store.AuditActor{IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent")}, nil
}
func (h *Handler) UploadAsset(ctx context.Context, request adminapi.UploadAssetRequestObject) (adminapi.UploadAssetResponseObject, error) {
	if h.Assets == nil {
		return nil, domain.Internal(nil)
	}
	actor, err := h.assetActor(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	form, err := readAssetForm(request.Body)
	if err != nil {
		return nil, err
	}
	kind, data, source := form.kind, form.data, form.source
	if (data == nil) == (form.downloadURL == "") {
		return nil, domain.Validation()
	}
	if form.downloadURL != "" {
		data, err = h.Assets.DownloadURL(requestContext(ctx), form.downloadURL)
		if err != nil {
			return nil, err
		}
		if source == nil {
			source = &form.downloadURL
		}
	}
	if op := domain.CurrentOperation(requestContext(ctx)); op != nil && op.DryRun {
		after, err := h.Assets.Preview(requestContext(ctx), kind, data)
		if err != nil {
			return nil, err
		}
		return response[adminapi.UploadAsset200JSONResponse](map[string]any{"dryRun": true, "requestId": op.RequestID, "changes": []any{map[string]any{"object": map[string]any{"entity": "asset", "objectKey": "new"}, "action": "create", "before": nil, "after": after}}, "affectedUrls": []string{}}, nil)
	}
	if op := domain.CurrentOperation(requestContext(ctx)); op != nil {
		op.Name = "uploadAsset"
	}
	asset, err := h.Assets.Upload(requestContext(ctx), kind, data, source, actor)
	if err != nil {
		return nil, err
	}
	var out adminapi.AssetResponseResult
	if err := out.FromAssetResponse(adminapi.AssetResponse{Code: domain.CodeOK, Msg: "ok", Data: &asset}); err != nil {
		return nil, err
	}
	return adminapi.UploadAsset200JSONResponse{Body: out}, nil
}

// maxAssetBytes matches the image decoder and URL downloader limits; requests get 1 MB of multipart headroom.
const maxAssetBytes = 5 * 1024 * 1024

type assetForm struct {
	kind, downloadURL string
	source            *string
	data              []byte
}

// readAssetForm bounds the body before decoding: at most ten distinct parts, 2 KB per text field and one 5 MB file.
func readAssetForm(body *multipart.Reader) (assetForm, error) {
	var form assetForm
	seen := map[string]bool{}
	parts := 0
	for {
		part, err := body.NextPart()
		if errors.Is(err, io.EOF) {
			return form, nil
		}
		if err != nil {
			return form, domain.Validation()
		}
		if seen[part.FormName()] {
			_ = part.Close()
			return form, domain.Validation()
		}
		seen[part.FormName()] = true
		parts++
		if parts > 10 {
			return form, domain.Validation()
		}
		limit := int64(2048)
		if part.FormName() == "file" {
			limit = maxAssetBytes
		}
		value, err := io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if err != nil {
			return form, domain.Validation()
		}
		if int64(len(value)) > limit {
			return form, &domain.AppError{Code: domain.CodeInvalidUpload, HTTPStatus: http.StatusBadRequest}
		}
		switch part.FormName() {
		case "file":
			form.data = value
		case "kind":
			form.kind = string(value)
		case "downloadUrl":
			form.downloadURL = string(value)
		case "sourceUrl":
			text := string(value)
			form.source = &text
		}
	}
}

// CreateAssetUploadTicket lets Agents upload local files directly, so image bytes never pass through MCP.
func (h *Handler) CreateAssetUploadTicket(ctx context.Context, request adminapi.CreateAssetUploadTicketRequestObject) (adminapi.CreateAssetUploadTicketResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	c, ok := ctx.(*gin.Context)
	svc := h.agentService()
	op := domain.CurrentOperation(requestContext(ctx))
	if !ok || svc == nil || op == nil {
		return nil, agent.Error(domain.CodeForbidden)
	}
	value, _ := c.Get("agentIdentity")
	id, ok := value.(agent.Identity)
	if !ok {
		return nil, agent.Error(domain.CodeForbidden)
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	grant := agent.UploadGrant{Kind: string(request.Body.Kind), Tool: op.Tool, RequestID: op.RequestID}
	if request.Body.SourceUrl != nil {
		grant.SourceURL = *request.Body.SourceUrl
	}
	if op.DryRun {
		return response[adminapi.CreateAssetUploadTicket200JSONResponse](map[string]any{"dryRun": true, "requestId": op.RequestID, "changes": []any{map[string]any{"object": map[string]any{"entity": "asset", "objectKey": "new"}, "action": "create", "before": nil, "after": map[string]any{"kind": grant.Kind}}}, "affectedUrls": []string{}}, nil)
	}
	ticket, expires, err := svc.IssueUploadTicket(requestContext(ctx), id, grant)
	if err != nil {
		return nil, err
	}
	return response[adminapi.CreateAssetUploadTicket200JSONResponse](map[string]any{"uploadUrl": h.Management.Config.AdminAPIBaseURL() + "/assets/upload-tickets/" + ticket, "method": "POST", "fileField": "file", "kind": grant.Kind, "maxBytes": maxAssetBytes, "expiresAt": expires}, nil)
}

// UploadAssetWithTicket stores one file with the kind and provenance fixed at issue time.
func (h *Handler) UploadAssetWithTicket(ctx context.Context, request adminapi.UploadAssetWithTicketRequestObject) (adminapi.UploadAssetWithTicketResponseObject, error) {
	if h.Assets == nil {
		return nil, domain.Internal(nil)
	}
	// Only the ticket middleware admits this route; never fall back to admin or CI identities.
	c, ok := ctx.(*gin.Context)
	if !ok {
		return nil, agent.Error(domain.CodeUnauthenticated)
	}
	value, _ := c.Get("agentUpload")
	grant, ok := value.(agent.UploadGrant)
	if !ok {
		return nil, agent.Error(domain.CodeUnauthenticated)
	}
	actor, err := h.assetActor(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	form, err := readAssetForm(request.Body)
	if err != nil {
		return nil, err
	}
	if form.data == nil || form.kind != "" || form.downloadURL != "" || form.source != nil {
		return nil, domain.Validation()
	}
	var source *string
	if grant.SourceURL != "" {
		source = &grant.SourceURL
	}
	asset, err := h.Assets.Upload(requestContext(ctx), grant.Kind, form.data, source, actor)
	if err != nil {
		return nil, err
	}
	var out adminapi.AssetResponseResult
	if err := out.FromAssetResponse(adminapi.AssetResponse{Code: domain.CodeOK, Msg: "ok", Data: &asset}); err != nil {
		return nil, err
	}
	return adminapi.UploadAssetWithTicket200JSONResponse{Body: out}, nil
}
