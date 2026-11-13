package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func toDeveloperSubmission(row store.DeveloperSubmission) adminapi.DeveloperSubmission {
	var payload map[string]interface{}
	if len(row.Payload) > 0 {
		_ = json.Unmarshal(row.Payload, &payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	return adminapi.DeveloperSubmission{
		Id:           row.ID,
		PublicToken:  optionalString(row.PublicToken),
		Kind:         adminapi.DeveloperSubmissionKind(row.Kind),
		Status:       adminapi.DeveloperSubmissionStatus(row.Status),
		SourceKind:   optionalString(row.SourceKind),
		SourceUrl:    optionalString(row.SourceURL),
		PackageId:    row.PackageID,
		Payload:      payload,
		ContactEmail: row.ContactEmail,
		Proof:        optionalString(row.Proof),
		ReviewerId:   row.ReviewerID,
		DecisionNote: row.DecisionNote,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		DecidedAt:    row.DecidedAt,
	}
}

func (h *Handler) ListDeveloperSubmissions(ctx context.Context, request adminapi.ListDeveloperSubmissionsRequestObject) (adminapi.ListDeveloperSubmissionsResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	status := ""
	if request.Params.Status != nil {
		status = string(*request.Params.Status)
	}
	limit := 50
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	query := ""
	if request.Params.Q != nil {
		query = strings.TrimSpace(*request.Params.Q)
	}
	rows, err := h.Management.Store.DeveloperSubmissions(requestContext(ctx), status, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]adminapi.DeveloperSubmission, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDeveloperSubmission(row))
	}
	return response[adminapi.ListDeveloperSubmissions200JSONResponse](out, nil)
}

func (h *Handler) ListDeveloperPicks(ctx context.Context, _ adminapi.ListDeveloperPicksRequestObject) (adminapi.ListDeveloperPicksResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	rows, err := h.Management.Store.DeveloperPickRows(requestContext(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]adminapi.DeveloperPickAdmin, 0, len(rows))
	for _, row := range rows {
		quote := row.Quote
		out = append(out, adminapi.DeveloperPickAdmin{
			PackageId:    row.PackageID,
			Kind:         row.Kind,
			Token:        row.Token,
			Name:         row.Name,
			IconUrl:      row.IconURL,
			Quote:        &quote,
			Sort:         row.Sort,
			Active:       row.Active,
			PublishedAt:  row.PublishedAt,
			SubmissionId: row.SubmissionID,
		})
	}
	return response[adminapi.ListDeveloperPicks200JSONResponse](out, nil)
}

func (h *Handler) GetDeveloperSubmission(ctx context.Context, request adminapi.GetDeveloperSubmissionRequestObject) (adminapi.GetDeveloperSubmissionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	row, err := h.Management.Store.DeveloperSubmission(requestContext(ctx), request.Id)
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
	}
	return response[adminapi.GetDeveloperSubmission200JSONResponse](toDeveloperSubmission(row), nil)
}

func (h *Handler) UpdateDeveloperSubmission(ctx context.Context, request adminapi.UpdateDeveloperSubmissionRequestObject) (adminapi.UpdateDeveloperSubmissionResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	body := request.Body
	row, err := h.Management.Store.DeveloperSubmission(requestContext(ctx), request.Id)
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
	}
	status := row.Status
	if body.Status != nil {
		status = string(*body.Status)
	}
	switch status {
	case "submitted", "in_review", "changes_requested", "approved", "rejected", "published":
	default:
		return nil, domain.Validation()
	}
	packageID := row.PackageID
	if body.PackageToken != nil && strings.TrimSpace(*body.PackageToken) != "" {
		kind := "cask"
		if body.PackageKind != nil && strings.TrimSpace(*body.PackageKind) != "" {
			kind = strings.TrimSpace(*body.PackageKind)
		}
		found, ok, err := h.Management.Store.LivePackageID(domain.WithLinks(requestContext(ctx), true), kind, strings.TrimSpace(*body.PackageToken))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
		}
		packageID = &found
	}
	dryRun := request.Params.DryRun != nil && *request.Params.DryRun
	payload := row.Payload
	if body.Payload != nil {
		payload, err = json.Marshal(*body.Payload)
		if err != nil {
			return nil, domain.Validation()
		}
	}
	if dryRun {
		preview := row
		preview.Status = status
		preview.PackageID = packageID
		preview.Payload = payload
		preview.DecisionNote = body.DecisionNote
		return response[adminapi.UpdateDeveloperSubmission200JSONResponse](toDeveloperSubmission(preview), nil)
	}
	if status == "published" {
		quote := ""
		if body.Quote != nil {
			quote = strings.TrimSpace(*body.Quote)
		}
		sort := 0
		if body.Sort != nil {
			sort = *body.Sort
		}
		updated, err := h.Management.Store.PublishDeveloperSubmission(requestContext(ctx), store.DeveloperPublishInput{
			SubmissionID: row.ID,
			Status:       status,
			Note:         body.DecisionNote,
			Payload:      payload,
			PackageID:    packageID,
			ReviewerID:   actor(ctx).ID,
			Feature:      body.Feature,
			Quote:        quote,
			Sort:         sort,
		})
		if err != nil {
			return nil, err
		}
		return response[adminapi.UpdateDeveloperSubmission200JSONResponse](toDeveloperSubmission(updated), nil)
	}
	// Leaving `published` takes the pick, the map point and a `new` submission's link
	// package down in the same transaction as the status write and its timeline row.
	updated, err := h.Management.Store.ReviewDeveloperSubmission(requestContext(ctx), store.DeveloperReviewInput{
		SubmissionID: row.ID,
		Status:       status,
		Note:         body.DecisionNote,
		Payload:      payload,
		PackageID:    packageID,
		ReviewerID:   actor(ctx).ID,
	})
	if err != nil {
		return nil, err
	}
	return response[adminapi.UpdateDeveloperSubmission200JSONResponse](toDeveloperSubmission(updated), nil)
}

func (h *Handler) SetDeveloperPick(ctx context.Context, request adminapi.SetDeveloperPickRequestObject) (adminapi.SetDeveloperPickResponseObject, error) {
	if h.Management == nil {
		return nil, domain.Internal(nil)
	}
	if request.Body == nil {
		return nil, domain.Validation()
	}
	body := request.Body
	kind := "cask"
	if body.PackageKind != nil && strings.TrimSpace(*body.PackageKind) != "" {
		kind = strings.TrimSpace(*body.PackageKind)
	}
	token := strings.TrimSpace(body.PackageToken)
	if token == "" {
		return nil, domain.Validation()
	}
	id, ok, err := h.Management.Store.LivePackageID(domain.WithLinks(requestContext(ctx), true), kind, token)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	quote := ""
	if body.Quote != nil {
		quote = strings.TrimSpace(*body.Quote)
	}
	sort := 0
	if body.Sort != nil {
		sort = *body.Sort
	}
	dryRun := request.Params.DryRun != nil && *request.Params.DryRun
	if !dryRun {
		if err := h.Management.Store.SetDeveloperPick(requestContext(ctx), id, nil, quote, sort, active); err != nil {
			return nil, err
		}
	}
	return response[adminapi.SetDeveloperPick200JSONResponse](struct {
		PackageID int64 `json:"packageId"`
		Active    bool  `json:"active"`
	}{PackageID: id, Active: active}, nil)
}
