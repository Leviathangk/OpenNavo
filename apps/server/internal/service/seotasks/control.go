package seotasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
)

var errControlPreview = errors.New("task control preview rollback")

func (s *Service) Control(ctx context.Context, owner, operation, key string, payload any, dry bool, fn func(*Service) (any, error)) (any, error) {
	if owner == "" || len(key) < 8 || len(key) > 200 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n\x00") {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hash := Hash(raw)
	var result any
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo:control:"+owner+":"+key).Error; err != nil {
			return err
		}
		if !dry {
			var saved struct {
				Operation, RequestHash string
				Result                 json.RawMessage
			}
			r := tx.Raw("SELECT operation,request_hash,result FROM seo_task_controls WHERE owner=? AND idempotency_key=?", owner, key).Scan(&saved)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected > 0 {
				if saved.Operation != operation || saved.RequestHash != hash {
					return ErrConflict
				}
				return json.Unmarshal(saved.Result, &result)
			}
		}
		var err error
		result, err = fn(&Service{DB: tx})
		if err != nil {
			return err
		}
		if dry {
			return errControlPreview
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Exec("INSERT INTO seo_task_controls(owner,idempotency_key,operation,request_hash,result) VALUES(?,?,?,?,?::jsonb)", owner, key, operation, hash, string(encoded)).Error
	})
	if errors.Is(err, errControlPreview) {
		err = nil
	}
	return result, err
}

func (s *Service) Complete(ctx context.Context, id, fencing int64, owner, outcome, reason string) (Task, error) {
	var result Task
	if outcome != "done" && outcome != "skipped" && outcome != "quarantined" {
		return result, ErrInvalid
	}
	if len(reason) > 1000 || outcome != "done" && strings.TrimSpace(reason) == "" {
		return result, ErrInvalid
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task Task
		if err := tx.Raw("SELECT * FROM seo_tasks WHERE id=? FOR UPDATE", id).Scan(&task).Error; err != nil {
			return err
		}
		if task.ID == 0 || task.Fencing != fencing {
			return ErrLease
		}
		if task.State == "done" {
			var prior string
			if err := tx.Raw("SELECT owner FROM seo_actions WHERE task_id=? ORDER BY id DESC LIMIT 1", id).Scan(&prior).Error; err != nil {
				return err
			}
			if prior != owner || outcome != "done" {
				return ErrLease
			}
			result = task
			return nil
		}
		verified, err := Verify(ctx, tx, id, fencing, owner)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]any{"outcome": outcome, "reason": reason})
		// A finished task reports what its last write produced. A skipped or
		// quarantined one keeps the agent's reason, with the last write beside it.
		update := "UPDATE seo_tasks SET state=?,lease_owner=NULL,lease_until=NULL,result=coalesce((SELECT a.result FROM seo_actions a WHERE a.task_id=seo_tasks.id ORDER BY a.id DESC LIMIT 1),?::jsonb),updated_at=clock_timestamp() WHERE id=?"
		if outcome != "done" {
			update = "UPDATE seo_tasks SET state=?,lease_owner=NULL,lease_until=NULL,result=?::jsonb||jsonb_build_object('lastAction',(SELECT a.result FROM seo_actions a WHERE a.task_id=seo_tasks.id ORDER BY a.id DESC LIMIT 1)),updated_at=clock_timestamp() WHERE id=?"
		}
		if err := tx.Exec(update, outcome, string(raw), id).Error; err != nil {
			return err
		}
		if err := release(tx, verified, owner); err != nil {
			return err
		}
		if err := promotePending(ctx, tx, id); err != nil {
			return err
		}
		result, err = (&Service{DB: tx}).Get(ctx, id)
		return err
	})
	return result, err
}
