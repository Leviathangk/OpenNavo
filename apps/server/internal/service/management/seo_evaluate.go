package management

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// EvaluateSEO never treats a traffic decrease as permission to undo a fact.
// Automatic compensation of legacy actions additionally verifies its task and evidence.
func (s *Service) EvaluateSEO(ctx context.Context) (map[string]any, error) {
	if err := s.assessPageGuards(ctx); err != nil {
		return nil, err
	}
	var rows []struct {
		ID          int64
		ObjectID    string
		EvidenceIDs json.RawMessage
	}
	err := s.Store.DB.WithContext(ctx).Raw(`SELECT a.id,a.object_id,to_jsonb(a.evidence_ids) evidence_ids FROM seo_actions a
 WHERE a.mode='live' AND a.compensated_at IS NULL AND a.after_revision IS NOT NULL
 AND a.operation NOT IN ('CompensateSeoAction','compensateSeoAction')
 AND a.applied_at>clock_timestamp()-interval '24 hours'
 AND EXISTS(SELECT 1 FROM seo_audit_issues i JOIN seo_urls u ON u.id=i.url_id WHERE 'pkg:'||u.package_id=a.object_id AND i.severity='p0' AND i.resolved_at IS NULL AND i.first_seen BETWEEN a.applied_at AND a.applied_at+interval '30 minutes')
 AND NOT EXISTS(SELECT 1 FROM seo_actions newer WHERE newer.object_id=a.object_id AND newer.mode='live' AND newer.id>a.id)
 ORDER BY a.id LIMIT 20`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	tasks := &seotasks.Service{DB: s.Store.DB}
	for _, row := range rows {
		_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "auto_compensate", EntityKey: row.ObjectID, DedupeKey: fmt.Sprintf("technical-compensation:%d", row.ID), RuleVersion: agentgate.RuleVersion, Priority: 100000, Evidence: map[string]any{"actionId": row.ID, "reason": "implementation_error", "evidenceIds": row.EvidenceIDs}})
		if err != nil {
			return nil, err
		}
	}
	claimed, err := tasks.Claim(ctx, "system:seo-compensation", 20, []string{"auto_compensate"})
	if err != nil {
		return nil, err
	}
	applied, deferred := 0, 0
	for _, task := range claimed {
		var input struct {
			ActionID    int64   `json:"actionId"`
			EvidenceIDs []int64 `json:"evidenceIds"`
		}
		if json.Unmarshal(task.Evidence, &input) != nil {
			return nil, domain.Validation()
		}
		err := s.autoCompensate(ctx, task, input.ActionID, input.EvidenceIDs)
		if err != nil {
			// Fixed reason excludes fetched text, URLs and provider response bodies.
			if e := tasks.Defer(ctx, task.ID, task.Fencing, "system:seo-compensation", "compensation_requires_current_matching_state_and_valid_evidence", time.Now().Add(6*time.Hour)); e != nil {
				return nil, e
			}
			deferred++
		} else {
			applied++
		}
	}
	return map[string]any{"compensated": applied, "deferred": deferred}, nil
}

func (s *Service) autoCompensate(ctx context.Context, task seotasks.Task, id int64, evidence []int64) error {
	return s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &store.Store{DB: tx}
		local := *s
		local.Store = st
		row, err := loadCompensation(ctx, st, id)
		if err != nil {
			return err
		}
		if row.AfterRevision == nil {
			return domain.Validation()
		}
		// Negative identity is an internal system operation, never a token identity.
		op := &domain.Operation{RequestID: uuid.NewString(), Name: "compensateSeoAction", AgentClientID: -1, ActorName: "SEO compensation worker", Permissions: []string{"*"}, RequiredPermissions: []string{"seo:task:handle"}, Execution: &domain.AgentExecution{TaskID: task.ID, Fencing: task.Fencing, IdempotencyKey: fmt.Sprintf("auto-compensate:%d", id), ExpectedRevision: *row.AfterRevision, EvidenceIDs: evidence}}
		work := context.WithValue(domain.WithOperation(ctx, op), agentTransactionKey{}, true)
		b, err := seotasks.Begin(work, tx, seotasks.Write{TaskID: task.ID, Fencing: task.Fencing, Owner: "system:seo-compensation", IdempotencyKey: op.Execution.IdempotencyKey, Operation: "CompensateSeoAction", ExpectedRevision: *row.AfterRevision, EvidenceIDs: evidence, Payload: map[string]any{"actionId": id, "reason": "implementation_error"}})
		if err != nil {
			return err
		}
		if b.Replayed {
			return nil
		}
		decision, err := agentgate.Check(work, tx, agentgate.Write{Site: seobudget.Site(s.Config.WebBaseURL), Operation: "CompensateSeoAction", Entity: task.EntityKey, Task: task, EvidenceIDs: evidence, Body: map[string]any{"reason": "implementation_error"}})
		if err != nil {
			return err
		}
		// Automatic compensation is a live write; proposal policies leave it deferred.
		if decision.Policy.Mode != "live" {
			return domain.Validation()
		}
		if err := seobudget.Reserve(work, tx, seobudget.Site(s.Config.WebBaseURL), "compensate", task.EntityKey, op.RequestID); err != nil {
			return err
		}
		result, err := local.compensateAction(work, Input{ID: id, Body: map[string]any{"reason": "implementation_error"}})
		if err != nil {
			return err
		}
		_, refs, err := compensationStates(row.AfterData)
		if err != nil {
			return err
		}
		after, err := captureHistory(work, st, refs)
		if err != nil {
			return err
		}
		revision, err := actionRevision(work, st, refs)
		if err != nil {
			return err
		}
		_, err = seotasks.Commit(work, tx, b, seotasks.Mutation{Mode: "live", Object: "history", ObjectID: task.EntityKey, Before: row.AfterData, After: after, AfterRevision: &revision, Fields: agentgate.ChangedFields(row.AfterData, after), Result: result, RuleVersion: agentgate.RuleVersion, PlaybookVersion: agentgate.PlaybookVersion})
		if err != nil {
			return err
		}
		return queueAgentInvalidation(work, st, task.EntityKey, refs)
	})
}

func (s *Service) assessPageGuards(ctx context.Context) error {
	return s.Store.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The action date is excluded. Windows need 56 fully fetched, final days.
		var rows []struct {
			ID                                                             int64
			ObjectID                                                       string
			Through                                                        time.Time
			BeforeClicks, AfterClicks, BeforeImpressions, AfterImpressions int64
		}
		if err := tx.Raw(`SELECT a.id,a.object_id,w.complete_through AS through,
   coalesce(sum(p.clicks) FILTER(WHERE p.day<a.applied_at::date),0) before_clicks,coalesce(sum(p.clicks) FILTER(WHERE p.day>a.applied_at::date),0) after_clicks,
   coalesce(sum(p.impressions) FILTER(WHERE p.day<a.applied_at::date),0) before_impressions,coalesce(sum(p.impressions) FILTER(WHERE p.day>a.applied_at::date),0) after_impressions
   FROM seo_actions a JOIN seo_data_watermarks w ON w.source='gsc_page' AND w.complete_through>=a.applied_at::date+28
   JOIN seo_urls u ON 'pkg:'||u.package_id=a.object_id LEFT JOIN seo_gsc_page_daily p ON p.url_id=u.id AND p.day BETWEEN a.applied_at::date-28 AND a.applied_at::date+28 AND p.day<>a.applied_at::date
   WHERE a.mode='live' AND a.compensated_at IS NULL AND NOT EXISTS(SELECT 1 FROM seo_action_guards g WHERE g.action_id=a.id)
   AND (SELECT count(*) FROM seo_ingest_days d WHERE d.source='gsc_page' AND d.temporal_complete AND d.retrieval_complete AND d.day BETWEEN a.applied_at::date-28 AND a.applied_at::date+28 AND d.day<>a.applied_at::date)=56
   GROUP BY a.id,w.complete_through ORDER BY a.id LIMIT 100`).Scan(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			outcome := "insufficient"
			if r.BeforeClicks >= 20 && r.BeforeImpressions >= 1000 {
				outcome = "stable"
				if r.AfterClicks*2 < r.BeforeClicks && r.AfterImpressions*2 < r.BeforeImpressions {
					outcome = "degraded"
				}
			}
			raw, _ := json.Marshal(map[string]any{"beforeClicks": r.BeforeClicks, "afterClicks": r.AfterClicks, "beforeImpressions": r.BeforeImpressions, "afterImpressions": r.AfterImpressions, "actionId": r.ID, "notCausal": true})
			if err := tx.Exec("INSERT INTO seo_action_guards(action_id,complete_through,outcome,metrics) VALUES(?,?,?,?::jsonb) ON CONFLICT DO NOTHING", r.ID, r.Through, outcome, string(raw)).Error; err != nil {
				return err
			}
			if outcome == "degraded" {
				if _, err := (&seotasks.Service{DB: tx}).Enqueue(ctx, seotasks.NewTask{Kind: "tech_alert", EntityKey: r.ObjectID, DedupeKey: fmt.Sprintf("page-degradation:%d", r.ID), RuleVersion: agentgate.RuleVersion, Priority: 1000, Evidence: json.RawMessage(raw)}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
