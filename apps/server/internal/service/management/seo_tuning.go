package management

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/service/seotune"
	"gorm.io/gorm"
)

func seoOwner(ctx context.Context) string {
	op := domain.CurrentOperation(ctx)
	if op.AgentClientID > 0 {
		return seotasks.Owner(op.AgentClientID, op.AgentTokenID)
	}
	if op.ActorID != nil {
		return fmt.Sprintf("admin:%d", *op.ActorID)
	}
	return "system"
}
func requireSEOAccess(ctx context.Context, permission string) error {
	op := domain.CurrentOperation(ctx)
	if op == nil || !op.Allowed(permission) || (op.AgentClientID > 0 && !agent.ValidPermissionSet(op.Permissions)) {
		return fail(domain.CodeForbidden)
	}
	return nil
}
func (s *Service) seoAutomation(ctx context.Context, name string, in Input) (any, error) {
	if err := requireSEOAccess(ctx, agent.TunerPermission); err != nil {
		return nil, err
	}
	if name == "GetSeoAutomation" {
		return s.Store.JSONRow(ctx, "SELECT jsonb_build_object('automation',automation,'revision',automation_revision) data FROM seo_settings WHERE id=true")
	}
	var request struct {
		ExpectedRevision int64           `json:"expectedRevision"`
		IdempotencyKey   string          `json:"idempotencyKey"`
		Reason           string          `json:"reason"`
		Automation       json.RawMessage `json:"automation"`
	}
	raw, _ := json.Marshal(in.Body)
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&request) != nil || request.ExpectedRevision < 1 || strings.TrimSpace(request.Reason) == "" {
		return nil, domain.Validation()
	}
	if _, err := seotune.Decode(request.Automation); err != nil {
		return nil, domain.Validation()
	}
	result, err := seotune.Receipt(ctx, s.Store.DB, seoOwner(ctx), name, request.IdempotencyKey, request.Reason, in.Body, domain.CurrentOperation(ctx).DryRun, func(tx *gorm.DB) (any, any, any, error) {
		if err := seobudget.Lock(ctx, tx, seobudget.Site(domain.CurrentOperation(ctx).WebBaseURL)); err != nil {
			return nil, nil, nil, err
		}
		var row struct {
			Automation json.RawMessage
			Revision   int64
		}
		if err := tx.Raw("SELECT automation,automation_revision revision FROM seo_settings WHERE id=true FOR UPDATE").Scan(&row).Error; err != nil {
			return nil, nil, nil, err
		}
		if row.Revision != request.ExpectedRevision {
			return nil, nil, nil, seotasks.ErrConflict
		}
		if err := tx.Exec("UPDATE seo_settings SET automation=?::jsonb,automation_revision=automation_revision+1 WHERE id=true", string(request.Automation)).Error; err != nil {
			return nil, nil, nil, err
		}
		var coverage struct{ Data json.RawMessage }
		if err := tx.Raw(`SELECT coalesce(jsonb_object_agg(state,n),'{}'::jsonb) data FROM (SELECT coalesce(coverage_state,'unknown') state,count(*) n FROM seo_url_status GROUP BY coverage_state) c`).Scan(&coverage).Error; err != nil {
			return nil, nil, nil, err
		}
		before := map[string]any{"automation": row.Automation, "revision": row.Revision, "coverage": coverage.Data}
		after := map[string]any{"automation": request.Automation, "revision": row.Revision + 1}
		return before, after, after, nil
	})
	return result, mapTaskError(err)
}

// SEOTaskTrace is shared with H5's task reader. Authorization belongs to the
// caller; callers must first enforce their task-view permission and scope.
func (s *Service) SEOTaskTrace(ctx context.Context, id int64) (any, error) {
	return s.Store.JSONRow(ctx, `SELECT jsonb_build_object(
 'task',to_jsonb(t)-'lease_owner',
 'actions',coalesce((SELECT jsonb_agg(to_jsonb(a)-'owner' ORDER BY a.id) FROM seo_actions a WHERE a.task_id=t.id),'[]'::jsonb),
 'evidence',coalesce((SELECT jsonb_agg(to_jsonb(e)-'created_by'-'body_text' ORDER BY e.id) FROM seo_evidence_snapshots e WHERE e.id IN (SELECT unnest(a.evidence_ids) FROM seo_actions a WHERE a.task_id=t.id)),'[]'::jsonb),
 'experiments',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM seo_experiments x WHERE x.id IN (SELECT a.experiment_id FROM seo_actions a WHERE a.task_id=t.id)),'[]'::jsonb),
 'gateEvents',coalesce((SELECT jsonb_agg(to_jsonb(g)-'owner' ORDER BY g.id) FROM seo_gate_events g WHERE g.task_id=t.id),'[]'::jsonb)) data FROM seo_tasks t WHERE t.id=?`, id)
}

// SEOTuningMetrics reports observed counts and ratios, never causal attribution.
func (s *Service) SEOTuningMetrics(ctx context.Context) (any, error) {
	rows, err := s.Store.JSONRows(ctx, `SELECT jsonb_build_object('kind','action','operation',operation,'live',count(*) FILTER(WHERE mode='live'),
 'proposals',count(*) FILTER(WHERE mode='proposal'),'compensated',count(*) FILTER(WHERE compensated_at IS NOT NULL),
 'retentionRate',count(*) FILTER(WHERE mode='live' AND compensated_at IS NULL)::numeric/nullif(count(*) FILTER(WHERE mode='live'),0),
 'compensationRate',count(*) FILTER(WHERE compensated_at IS NOT NULL)::numeric/nullif(count(*) FILTER(WHERE mode='live'),0)) data
 FROM seo_actions GROUP BY operation ORDER BY operation`)
	if err != nil {
		return nil, err
	}
	for _, query := range []string{
		`SELECT jsonb_build_object('kind','experiments','completed',count(*) FILTER(WHERE state<>'running'),'won',count(*) FILTER(WHERE state='won'),'inconclusive',count(*) FILTER(WHERE state='inconclusive'),'winRate',count(*) FILTER(WHERE state='won')::numeric/nullif(count(*) FILTER(WHERE state<>'running'),0),'inconclusiveRate',count(*) FILTER(WHERE state='inconclusive')::numeric/nullif(count(*) FILTER(WHERE state<>'running'),0)) data FROM seo_experiments`,
		`SELECT jsonb_build_object('kind','costs','spentMicros',coalesce(sum(cost_micros),0),'retainedActions',(SELECT count(*) FROM seo_actions WHERE mode='live' AND compensated_at IS NULL),'costPerRetainedActionMicros',sum(cost_micros)::numeric/nullif((SELECT count(*) FROM seo_actions WHERE mode='live' AND compensated_at IS NULL),0),'attribution','aggregate_observation_only') data FROM seo_cost_receipts`,
		`WITH current AS (SELECT coalesce(jsonb_object_agg(state,n),'{}'::jsonb) states FROM (SELECT coalesce(coverage_state,'unknown') state,count(*) n FROM seo_url_status GROUP BY coverage_state) c), baseline AS (SELECT before_data->'coverage' states,created_at FROM seo_control_receipts WHERE operation='UpdateSeoAutomation' AND before_data ? 'coverage' ORDER BY id DESC LIMIT 1)
 SELECT jsonb_build_object('kind','inspectionCoverage','currentStates',c.states,'baselineStates',b.states,'baselineAt',b.created_at,'changes',CASE WHEN b.states IS NULL THEN NULL ELSE (SELECT jsonb_object_agg(k,coalesce((c.states->>k)::bigint,0)-coalesce((b.states->>k)::bigint,0)) FROM (SELECT jsonb_object_keys(c.states) k UNION SELECT jsonb_object_keys(b.states) k) keys) END,'scope','inspected_urls_only') data FROM current c LEFT JOIN baseline b ON true`,
	} {
		extra, err := s.Store.JSONRows(ctx, query)
		if err != nil {
			return nil, err
		}
		rows = append(rows, extra...)
	}
	return rows, nil
}
