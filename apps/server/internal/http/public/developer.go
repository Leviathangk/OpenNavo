package public

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/developer"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

const maxSubmissionPayload = 64 << 10

func (h *Handler) GetDeveloperMap(ctx context.Context, request publicapi.GetDeveloperMapRequestObject) (publicapi.GetDeveloperMapResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.DeveloperMap(requestContext(ctx), locale(ctx, request.Params.Locale))
	return response[publicapi.GetDeveloperMap200JSONResponse](data, err)
}

func (h *Handler) ListDeveloperPicks(ctx context.Context, request publicapi.ListDeveloperPicksRequestObject) (publicapi.ListDeveloperPicksResponseObject, error) {
	if h.Catalog == nil {
		return nil, domain.Internal(nil)
	}
	data, err := h.Catalog.DeveloperPicks(requestContext(ctx), locale(ctx, request.Params.Locale), fallback(request.Params.Limit, 4))
	return response[publicapi.ListDeveloperPicks200JSONResponse](data, err)
}

func (h *Handler) FetchDeveloperMetadata(ctx context.Context, request publicapi.FetchDeveloperMetadataRequestObject) (publicapi.FetchDeveloperMetadataResponseObject, error) {
	if request.Body == nil {
		return nil, domain.Validation()
	}
	if h.Developer == nil {
		return nil, domain.Internal(nil)
	}
	rawURL := strings.TrimSpace(request.Body.Url)
	if rawURL == "" || len(rawURL) > 2048 {
		return nil, domain.Validation()
	}
	metadata, err := h.Developer.Fetch(requestContext(ctx), rawURL)
	if err != nil {
		if metadata.Name == "" {
			return nil, domain.Validation()
		}
		metadata.Warnings = append(metadata.Warnings, err.Error())
	}
	return response[publicapi.FetchDeveloperMetadata200JSONResponse](metadata, nil)
}

func (h *Handler) CreateDeveloperSubmission(ctx context.Context, request publicapi.CreateDeveloperSubmissionRequestObject) (publicapi.CreateDeveloperSubmissionResponseObject, error) {
	if h.Catalog == nil || h.Catalog.Store == nil {
		return nil, domain.Internal(nil)
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	body := request.Body
	if body.Website != nil && strings.TrimSpace(*body.Website) != "" {
		// Honeypot: answer like a success, store nothing.
		return response[publicapi.CreateDeveloperSubmission200JSONResponse](publicapi.DeveloperSubmissionReceipt{}, nil)
	}
	kind := string(body.Kind)
	if kind != "new" && kind != "claim" && kind != "location" {
		return nil, domain.Validation()
	}
	contactEmail := strings.TrimSpace(string(body.ContactEmail))
	if !developer.ValidEmail(contactEmail) {
		return nil, domain.Validation()
	}
	sourceURL := ""
	if body.SourceUrl != nil {
		sourceURL = strings.TrimSpace(*body.SourceUrl)
	}
	proof := ""
	if body.Proof != nil {
		proof = strings.TrimSpace(*body.Proof)
	}
	if len(proof) > 2000 || len(sourceURL) > 2048 {
		return nil, domain.Validation()
	}
	var payload json.RawMessage
	if body.Payload != nil {
		encoded, err := json.Marshal(*body.Payload)
		if err != nil || len(encoded) > maxSubmissionPayload {
			return nil, domain.Validation()
		}
		payload = encoded
	}
	if kind == "new" && sourceURL == "" {
		return nil, domain.Validation()
	}
	if sourceURL != "" {
		parsed, err := url.Parse(sourceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			return nil, domain.Validation()
		}
	}
	if kind == "location" {
		parsed := store.ParseDeveloperPayload(payload)
		if !parsed.HasLocation() {
			return nil, domain.Validation()
		}
	}

	var packageID *int64
	if kind == "claim" || kind == "location" {
		packageKind := "cask"
		if body.PackageKind != nil && strings.TrimSpace(string(*body.PackageKind)) != "" {
			packageKind = strings.TrimSpace(string(*body.PackageKind))
		}
		token := ""
		if body.PackageToken != nil {
			token = strings.TrimSpace(*body.PackageToken)
		}
		if token == "" {
			return nil, domain.Validation()
		}
		id, ok, err := h.Catalog.Store.LivePackageID(requestContext(ctx), packageKind, token)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
		}
		packageID = &id
	}

	sourceKind := ""
	if body.SourceKind != nil {
		sourceKind = string(*body.SourceKind)
	}
	row, err := h.Catalog.Store.CreateDeveloperSubmissionWithEvent(requestContext(ctx), store.DeveloperSubmissionInput{
		Kind:         kind,
		SourceKind:   developer.InferSourceKind(sourceKind, sourceURL),
		SourceURL:    sourceURL,
		PackageID:    packageID,
		Payload:      payload,
		ContactEmail: contactEmail,
		Proof:        proof,
	})
	if err != nil {
		return nil, err
	}
	statusURL := fmt.Sprintf("/developer-map?tab=submit&submission=%s", row.PublicToken)
	if base := strings.TrimRight(strings.TrimSpace(h.Catalog.WebBaseURL), "/"); base != "" {
		statusURL = base + statusURL
	}
	return response[publicapi.CreateDeveloperSubmission200JSONResponse](publicapi.DeveloperSubmissionReceipt{
		Id:        row.ID,
		Reference: fmt.Sprintf("DS-%06d", row.ID),
		Token:     row.PublicToken,
		StatusUrl: statusURL,
	}, nil)
}

func (h *Handler) GetDeveloperSubmission(ctx context.Context, request publicapi.GetDeveloperSubmissionRequestObject) (publicapi.GetDeveloperSubmissionResponseObject, error) {
	if h.Catalog == nil || h.Catalog.Store == nil {
		return nil, domain.Internal(nil)
	}
	if c, ok := ctx.(*gin.Context); ok {
		c.Header("Cache-Control", "no-store")
	}
	row, err := h.Catalog.Store.DeveloperSubmissionByToken(requestContext(ctx), request.Token)
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
	}
	events, err := h.Catalog.Store.DeveloperSubmissionEvents(requestContext(ctx), row.ID)
	if err != nil {
		return nil, err
	}
	type statusEvent = struct {
		Actor     publicapi.DeveloperSubmissionStatusEventsActor `json:"actor"`
		CreatedAt time.Time                                      `json:"createdAt"`
		Note      *string                                        `json:"note,omitempty"`
		Status    string                                         `json:"status"`
	}
	type statusPackage = struct {
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	status := publicapi.DeveloperSubmissionStatus{
		Reference:    fmt.Sprintf("DS-%06d", row.ID),
		Kind:         publicapi.DeveloperSubmissionStatusKind(row.Kind),
		Status:       publicapi.DeveloperSubmissionStatusStatus(row.Status),
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		DecidedAt:    row.DecidedAt,
		DecisionNote: row.DecisionNote,
		Events:       make([]statusEvent, 0, len(events)),
	}
	for _, event := range events {
		status.Events = append(status.Events, statusEvent{
			Actor:     publicapi.DeveloperSubmissionStatusEventsActor(event.Actor),
			CreatedAt: event.CreatedAt,
			Note:      event.Note,
			Status:    event.Status,
		})
	}
	if row.PackageID != nil {
		kind, token, name, ok, err := h.Catalog.Store.DeveloperPackageRef(requestContext(ctx), *row.PackageID)
		if err != nil {
			return nil, err
		}
		if ok {
			status.Package = &statusPackage{Kind: kind, Token: token, Name: name}
		}
	}
	return response[publicapi.GetDeveloperSubmission200JSONResponse](status, nil)
}
