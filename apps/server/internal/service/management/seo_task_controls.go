package management

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
)

func (s *Service) seoTaskControl(ctx context.Context, name string, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:task:handle"); err != nil {
		return nil, err
	}
	if name == "GetSeoTask" {
		return s.seoTaskTrace(ctx, in.ID)
	}
	var request struct {
		IdempotencyKey string    `json:"idempotencyKey"`
		Limit          int       `json:"limit"`
		Kinds          []string  `json:"kinds"`
		TaskIDs        []int64   `json:"taskIds"`
		Fencing        int64     `json:"fencing"`
		Outcome        string    `json:"outcome"`
		Reason         string    `json:"reason"`
		RetryAt        time.Time `json:"retryAt"`
	}
	raw, _ := json.Marshal(in.Body)
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return nil, domain.Validation()
	}
	if request.Limit == 0 {
		request.Limit = 1
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = uuid.NewString()
	}
	owner := seoOwner(ctx)
	var closed []string
	if name == "ClaimSeoTasks" {
		var err error
		if closed, err = s.closedLocales(ctx); err != nil {
			return nil, err
		}
	}
	tasks := &seotasks.Service{DB: s.Store.DB}
	result, err := tasks.Control(ctx, owner, name, request.IdempotencyKey, map[string]any{"id": in.ID, "body": in.Body}, domain.CurrentOperation(ctx).DryRun, func(t *seotasks.Service) (any, error) {
		switch name {
		case "ClaimSeoTasks":
			records, err := t.ClaimWith(ctx, owner, seotasks.ClaimFilter{Limit: request.Limit, Kinds: request.Kinds, IDs: request.TaskIDs, ClosedLocales: closed})
			return map[string]any{"records": records}, err
		case "CompleteSeoTask":
			return t.Complete(ctx, in.ID, request.Fencing, owner, request.Outcome, request.Reason)
		case "DeferSeoTask":
			if err := t.Defer(ctx, in.ID, request.Fencing, owner, request.Reason, request.RetryAt); err != nil {
				return nil, err
			}
			return t.Get(ctx, in.ID)
		case "ReopenSeoTask":
			return t.Reopen(ctx, in.ID, owner, request.Reason, domain.CurrentOperation(ctx).AgentClientID == 0)
		}
		return nil, domain.Validation()
	})
	return result, mapTaskError(err)
}

// closedLocales are the site languages closed for indexing (pages.openLocales), in site order.
func (s *Service) closedLocales(ctx context.Context) ([]string, error) {
	settings, err := s.Store.SEOSettings(ctx)
	if err != nil {
		return nil, err
	}
	closed := []string{}
	for _, locale := range seo.Locales {
		if !seo.LocaleOpen(settings.Settings.Pages.OpenLocales, locale) {
			closed = append(closed, locale)
		}
	}
	return closed, nil
}

func (s *Service) captureSeoEvidence(ctx context.Context, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:task:handle"); err != nil {
		return nil, err
	}
	var request struct {
		TaskID         int64  `json:"taskId"`
		Fencing        int64  `json:"fencing"`
		IdempotencyKey string `json:"idempotencyKey"`
		EntityKey      string `json:"entityKey"`
		URL            string `json:"url"`
		Excerpt        string `json:"excerpt"`
	}
	raw, _ := json.Marshal(in.Body)
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return nil, domain.Validation()
	}
	svc := s.Evidence
	if svc == nil {
		svc = agentgate.NewEvidenceService(s.Store.DB, s.Config.FetchUserAgent)
	}
	result, err := svc.Capture(ctx, agentgate.Capture{TaskID: request.TaskID, Fencing: request.Fencing, Owner: seoOwner(ctx), IdempotencyKey: request.IdempotencyKey, EntityKey: request.EntityKey, URL: request.URL, Excerpt: request.Excerpt, DryRun: domain.CurrentOperation(ctx).DryRun})
	if rejected, reason, ok := agentgate.Rejection(err, http.StatusBadRequest); ok {
		logctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if logerr := agentgate.Log(logctx, s.Store.DB, domain.CurrentOperation(ctx).RequestID, seoOwner(ctx), "CreateSeoEvidence", "rejected", reason, request.TaskID, nil); logerr != nil {
			return nil, logerr
		}
		return nil, rejected
	}
	return result, mapTaskError(err)
}

func (s *Service) seoActions(ctx context.Context, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:insight:view"); err != nil {
		return nil, err
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	operation, mode, result := text(in.Params, "operation"), text(in.Params, "mode"), text(in.Params, "result")
	if mode != "" && mode != "live" && mode != "proposal" || result != "" && result != "retained" && result != "compensated" {
		return nil, domain.Validation()
	}
	where := `(?='' OR operation=?) AND (?='' OR mode=?) AND (?='' OR (?='compensated' AND compensated_at IS NOT NULL) OR (?='retained' AND mode='live' AND compensated_at IS NULL))`
	args := []any{operation, operation, mode, mode, result, result, result}
	var total int64
	if err := s.Store.DB.WithContext(ctx).Table("seo_actions").Where(where, args...).Count(&total).Error; err != nil {
		return nil, err
	}
	records, err := s.seoRows(ctx, `SELECT (to_jsonb(a)-'owner')::text FROM seo_actions a WHERE `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, size, (current-1)*size)...)
	if err != nil {
		return nil, err
	}
	metrics, err := s.SEOTuningMetrics(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"records": records, "current": current, "size": size, "total": total, "metrics": metrics}, nil
}
