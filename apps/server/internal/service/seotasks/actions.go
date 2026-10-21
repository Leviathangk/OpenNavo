package seotasks

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/content"
	"gorm.io/gorm"
)

func Hash(raw []byte) string { return content.JSONHash(raw) }

type Write struct {
	TaskID           int64   `json:"taskId"`
	Fencing          int64   `json:"fencing"`
	Owner            string  `json:"-"`
	IdempotencyKey   string  `json:"idempotencyKey"`
	Operation        string  `json:"operation"`
	ExpectedRevision int64   `json:"expectedRevision"`
	EvidenceIDs      []int64 `json:"evidenceIds"`
	Payload          any     `json:"payload"`
}

type Begun struct {
	Task        Task
	Write       Write
	RequestHash string
	Replay      json.RawMessage
	Replayed    bool
}

type Mutation struct {
	Mode                         string
	Object, ObjectID             string
	Fields                       []string
	Before, After, Result        any
	RuleVersion, PlaybookVersion string
	ExperimentID                 *int64
	AfterRevision                *int64
}

// Begin must precede any business writes inside the same database transaction.
// The idempotency lock serializes a retry until the original commits or rolls back.
func Begin(ctx context.Context, tx *gorm.DB, w Write) (Begun, error) {
	out := Begun{Write: w}
	if w.TaskID <= 0 || w.Fencing <= 0 || w.Owner == "" || w.Operation == "" || w.ExpectedRevision < 0 || len(w.IdempotencyKey) < 8 || len(w.IdempotencyKey) > 200 || strings.TrimSpace(w.IdempotencyKey) != w.IdempotencyKey || strings.IndexFunc(w.IdempotencyKey, unicode.IsControl) >= 0 {
		return out, ErrInvalid
	}
	raw, err := json.Marshal(struct {
		TaskID    int64
		Operation string
		Revision  int64
		Evidence  []int64
		Payload   any
	}{w.TaskID, w.Operation, w.ExpectedRevision, w.EvidenceIDs, w.Payload})
	if err != nil {
		return out, ErrInvalid
	}
	out.RequestHash = Hash(raw)
	if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo:idempotency:"+w.IdempotencyKey).Error; err != nil {
		return out, err
	}
	var existing struct {
		TaskID             int64
		Owner, RequestHash string
		Result             json.RawMessage
	}
	r := tx.WithContext(ctx).Table("seo_actions").Where("idempotency_key=?", w.IdempotencyKey).Limit(1).Scan(&existing)
	if r.Error != nil {
		return out, r.Error
	}
	if r.RowsAffected > 0 {
		if existing.TaskID != w.TaskID || existing.Owner != w.Owner || existing.RequestHash != out.RequestHash {
			return out, ErrConflict
		}
		out.Replay = existing.Result
		out.Replayed = true
		return out, nil
	}
	out.Task, err = Verify(ctx, tx, w.TaskID, w.Fencing, w.Owner)
	return out, err
}

// Commit rechecks time after the business callback, so even a transaction holding
// the row lock cannot commit after its lease has expired. Its caller must roll
// the transaction back on any returned error.
func Commit(ctx context.Context, tx *gorm.DB, b Begun, m Mutation) (int64, error) {
	return commit(ctx, tx, b, m, true)
}

// CommitPartial records one language atomically while retaining the entity lease
// for the remaining languages. The caller completes the task explicitly.
func CommitPartial(ctx context.Context, tx *gorm.DB, b Begun, m Mutation) (int64, error) {
	return commit(ctx, tx, b, m, false)
}
func commit(ctx context.Context, tx *gorm.DB, b Begun, m Mutation, complete bool) (int64, error) {
	if b.Replayed {
		return 0, ErrConflict
	}
	if m.Mode != "live" && m.Mode != "proposal" {
		return 0, ErrInvalid
	}
	if m.Object == "" || m.ObjectID == "" || m.RuleVersion == "" || m.PlaybookVersion == "" {
		return 0, ErrInvalid
	}
	task, err := Verify(ctx, tx, b.Write.TaskID, b.Write.Fencing, b.Write.Owner)
	if err != nil {
		return 0, err
	}
	values := []any{m.Fields, m.Before, m.After, b.Write.EvidenceIDs, m.Result}
	encoded := make([]string, len(values))
	for i, v := range values {
		raw, e := json.Marshal(v)
		if e != nil {
			return 0, ErrInvalid
		}
		encoded[i] = string(raw)
	}
	if m.Fields == nil {
		encoded[0] = "[]"
	}
	if b.Write.EvidenceIDs == nil {
		encoded[3] = "[]"
	}
	var id int64
	err = tx.WithContext(ctx).Raw(`INSERT INTO seo_actions(task_id,idempotency_key,request_hash,owner,mode,operation,object,object_id,fields,before_data,after_data,base_revision,evidence_ids,rule_version,playbook_version,experiment_id,after_revision,result,applied_at) VALUES(?,?,?,?,?,?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?::jsonb,?::jsonb,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)::bigint),?,?,?,?,?::jsonb,CASE WHEN ?='live' THEN clock_timestamp() ELSE NULL END) RETURNING id`, task.ID, b.Write.IdempotencyKey, b.RequestHash, b.Write.Owner, m.Mode, b.Write.Operation, m.Object, m.ObjectID, encoded[0], encoded[1], encoded[2], b.Write.ExpectedRevision, encoded[3], m.RuleVersion, m.PlaybookVersion, m.ExperimentID, m.AfterRevision, encoded[4], m.Mode).Scan(&id).Error
	if err != nil {
		return 0, err
	}
	if !complete {
		if err := tx.Exec("UPDATE seo_tasks SET result=?::jsonb,updated_at=clock_timestamp() WHERE id=?", encoded[4], task.ID).Error; err != nil {
			return 0, err
		}
		return id, nil
	}
	r := tx.WithContext(ctx).Model(&Task{}).Where("id=? AND lease_until>clock_timestamp()", task.ID).Updates(map[string]any{"state": "done", "lease_owner": nil, "lease_until": nil, "result": gorm.Expr("?::jsonb", encoded[4]), "updated_at": gorm.Expr("clock_timestamp()")})
	if r.Error != nil {
		return 0, r.Error
	}
	if r.RowsAffected != 1 {
		return 0, ErrLease
	}
	if err := release(tx, task, b.Write.Owner); err != nil {
		return 0, err
	}
	return id, promotePending(ctx, tx, task.ID)
}

// Run keeps every callback effect (including revisions and outbox inserts) in
// the action transaction. A client disconnect after commit can replay the result.
func (s *Service) Run(ctx context.Context, w Write, fn func(*gorm.DB, Task) (Mutation, error)) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		b, err := Begin(ctx, tx, w)
		if err != nil {
			return err
		}
		if b.Replayed {
			result = b.Replay
			return nil
		}
		m, err := fn(tx, b.Task)
		if err != nil {
			return err
		}
		if _, err = Commit(ctx, tx, b, m); err != nil {
			return err
		}
		result, err = json.Marshal(m.Result)
		return err
	})
	return result, err
}
