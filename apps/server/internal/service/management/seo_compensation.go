package management

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
)

type compensationRow struct {
	ID                            int64
	Object, ObjectID, Mode        string
	BeforeData, AfterData, Fields json.RawMessage
	CompensatedAt                 *time.Time
	AfterRevision                 *int64
}

func loadCompensation(ctx context.Context, st *store.Store, id int64) (compensationRow, error) {
	var row compensationRow
	err := st.DB.WithContext(ctx).Raw("SELECT id,object,object_id,mode,before_data,after_data,to_jsonb(fields) fields,compensated_at,after_revision FROM seo_actions WHERE id=? FOR UPDATE", id).Scan(&row).Error
	if err == nil && (row.ID == 0 || row.Mode != "live" || row.CompensatedAt != nil) {
		err = fail(domain.CodeInvalidState)
	}
	return row, err
}
func compensationStates(raw json.RawMessage) (map[string]store.HistoryState, []content.Ref, error) {
	var states map[string]store.HistoryState
	if json.Unmarshal(raw, &states) != nil || len(states) == 0 {
		return nil, nil, domain.Validation()
	}
	refs := []content.Ref{}
	for key := range states {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			return nil, nil, domain.Validation()
		}
		ref, err := store.HistoryRef(parts[0], parts[1])
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, ref)
	}
	return states, refs, nil
}

func (s *Service) compensateAction(ctx context.Context, in Input) (any, error) {
	row, err := loadCompensation(ctx, s.Store, in.ID)
	if err != nil {
		return nil, err
	}
	before, refs, err := compensationStates(row.BeforeData)
	if err != nil {
		return nil, err
	}
	after, _, err := compensationStates(row.AfterData)
	if err != nil {
		return nil, err
	}
	revision, err := actionRevision(ctx, s.Store, refs)
	if err != nil {
		return nil, err
	}
	if row.AfterRevision == nil || revision != *row.AfterRevision {
		return nil, fail(domain.CodeConflict)
	}
	current, err := captureHistory(ctx, s.Store, refs)
	if err != nil {
		return nil, err
	}
	var saved, requested []string
	if json.Unmarshal(row.Fields, &saved) != nil {
		return nil, domain.Validation()
	}
	if value, present := in.Body["fieldPaths"]; present {
		raw, _ := json.Marshal(value)
		if json.Unmarshal(raw, &requested) != nil {
			return nil, domain.Validation()
		}
	}
	if err := agentgate.CheckCompensation(before, after, current, saved, requested, text(in.Body, "reason")); err != nil {
		return nil, err
	}
	op := domain.CurrentOperation(ctx)
	for _, ref := range refs {
		if err := assertContentAccess(ctx, ref); err != nil {
			return nil, err
		}
		key := ref.Entity + ":" + ref.Key()
		target := before[key]
		if restoresDeletedContent(ref.Entity, current[key], target) && !op.AllowDelete {
			return nil, fail(domain.CodeForbidden)
		}
		if err := s.validateRestoration(ctx, s.Store, ref, current[key], target); err != nil {
			return nil, err
		}
		if err := s.Store.RestoreHistoryState(ctx, ref, target); err != nil {
			return nil, err
		}
		if ref.Valid() && target != nil {
			if err := s.Store.ScheduleContentTranslation(ctx, ref); err != nil {
				return nil, err
			}
			if err := s.Store.ContentChanged(ctx, ref); err != nil {
				return nil, err
			}
		}
	}
	preview, err := finishHistory(ctx, s.Store, refs, current)
	if err != nil {
		return nil, err
	}
	if err := s.Store.DB.WithContext(ctx).Exec("UPDATE seo_actions SET compensated_at=clock_timestamp() WHERE id=? AND compensated_at IS NULL", row.ID).Error; err != nil {
		return nil, err
	}
	return map[string]any{"actionId": row.ID, "compensated": true, "affectedUrls": preview["affectedUrls"]}, nil
}

// The same validation also protects legacy restore/revert endpoints.
func (s *Service) validateRestoration(ctx context.Context, st *store.Store, ref content.Ref, current, target store.HistoryState) error {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.AgentClientID == 0 {
		return nil
	}
	if op.Execution == nil {
		return domain.Validation()
	}
	entity := ref.Entity + ":" + ref.Key()
	evidence, err := agentgate.LoadEvidence(ctx, st.DB, op.Execution.EvidenceIDs, entity, false)
	if err != nil {
		return err
	}
	var version string
	if ref.Entity == "package" {
		if err := st.DB.WithContext(ctx).Raw("SELECT coalesce(version,'') FROM packages WHERE id=?", ref.ID).Scan(&version).Error; err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(target)
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return domain.Validation()
	}
	if err := agentgate.ValidateText(value, version); err != nil {
		return err
	}
	for table, rows := range target {
		if strings.Contains(table, "fact") || table == "collection_setup_criteria" {
			if len(agentgate.ChangedFields(current[table], rows)) > 0 {
				if err := agentgate.ValidatePriorFacts(rows, evidence, s.now()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
