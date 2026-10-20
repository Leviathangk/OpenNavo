// Package seotasks owns durable entity leases and the atomic Agent action ledger.
package seotasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/i18n"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrLease    = errors.New("SEO lease lost")
	ErrConflict = errors.New("SEO request conflicts with recorded action")
	ErrInvalid  = errors.New("invalid SEO task request")
)

const LeaseDuration = 20 * time.Minute

type Service struct{ DB *gorm.DB }
type Task struct {
	ID            int64           `json:"id"`
	Kind          string          `json:"kind"`
	EntityKey     string          `json:"entityKey"`
	Locale        *string         `json:"locale"`
	DedupeKey     string          `json:"dedupeKey"`
	Priority      float64         `json:"priority"`
	Evidence      json.RawMessage `json:"evidence"`
	EvidenceHash  string          `json:"evidenceHash"`
	RuleVersion   string          `json:"ruleVersion"`
	State         string          `json:"state"`
	LeaseOwner    *string         `json:"-"`
	LeaseUntil    *time.Time      `json:"leaseUntil"`
	Fencing       int64           `json:"fencing"`
	Result        json.RawMessage `json:"result"`
	Attempts      int             `json:"attempts"`
	NextRetryAt   *time.Time      `json:"nextRetryAt"`
	LastAttemptAt *time.Time      `json:"lastAttemptAt"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	ExpiresAt     *time.Time      `json:"expiresAt"`
}

func (Task) TableName() string { return "seo_tasks" }

type NewTask struct {
	Kind, EntityKey, Locale, DedupeKey, RuleVersion string
	Priority                                        float64
	Evidence                                        any
	ExpiresAt                                       *time.Time
}

func (s *Service) Enqueue(ctx context.Context, in NewTask) (Task, error) {
	var task Task
	if in.Kind == "" || len(in.Kind) > 80 || in.EntityKey == "" || len(in.EntityKey) > 300 || len(in.DedupeKey) > 500 || in.DedupeKey == "" || in.RuleVersion == "" || math.IsNaN(in.Priority) || math.IsInf(in.Priority, 0) || (in.Locale != "" && !i18n.Valid(in.Locale)) {
		return task, ErrInvalid
	}
	raw, err := json.Marshal(in.Evidence)
	if err != nil {
		return task, ErrInvalid
	}
	hash := Hash(raw)
	err = s.DB.WithContext(ctx).Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version,expires_at) VALUES(?,?,NULLIF(?,''),?,?,?::jsonb,?,?,?) ON CONFLICT(dedupe_key) DO UPDATE SET dedupe_key=EXCLUDED.dedupe_key RETURNING *`, in.Kind, in.EntityKey, in.Locale, in.DedupeKey, in.Priority, string(raw), hash, in.RuleVersion, in.ExpiresAt).Scan(&task).Error
	if err == nil && (task.Kind != in.Kind || task.EntityKey != in.EntityKey || task.EvidenceHash != hash) {
		return Task{}, ErrConflict
	}
	return task, err
}

func (s *Service) Get(ctx context.Context, id int64) (Task, error) {
	var t Task
	err := s.DB.WithContext(ctx).First(&t, id).Error
	return t, err
}

func (s *Service) Claim(ctx context.Context, owner string, limit int, kinds []string) ([]Task, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 200 || limit < 1 || limit > 50 || len(kinds) > 20 {
		return nil, ErrInvalid
	}
	out := []Task{}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`WITH expired AS (SELECT id FROM seo_tasks WHERE expires_at<=clock_timestamp() AND state IN ('open','leased','deferred') ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED) UPDATE seo_tasks t SET state='expired',lease_owner=NULL,lease_until=NULL,updated_at=clock_timestamp() FROM expired e WHERE t.id=e.id`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE seo_entity_leases l SET lease_until=NULL,owner=NULL,task_id=NULL FROM seo_tasks t WHERE l.task_id=t.id AND l.fencing=t.fencing AND t.state='expired'`).Error; err != nil {
			return err
		}
		var candidates []Task
		query := tx.Where("(state IN ('open','deferred','leased') OR (kind='hermes_import' AND state='done' AND next_retry_at IS NOT NULL)) AND (expires_at IS NULL OR expires_at>clock_timestamp()) AND ((state='open') OR (kind='hermes_import' AND state='done' AND next_retry_at<=clock_timestamp()) OR (state='deferred' AND next_retry_at<=clock_timestamp()) OR (state='leased' AND lease_until<=clock_timestamp()))").
			Where("NOT EXISTS(SELECT 1 FROM seo_entity_leases e WHERE e.entity_key=seo_tasks.entity_key AND e.lease_until>clock_timestamp())")
		if len(kinds) > 0 {
			query = query.Where("kind IN ?", kinds)
		}
		// Adding age in days is this static score plus the same current-time
		// constant for every row. Keep the expression and open/retry predicate in
		// sync with seo_tasks_claim_age so it can supply candidate order without
		// scanning terminal history.
		// Waiting still adds an unbounded floor for lower-demand tasks.
		if err := query.Order("priority - extract(epoch FROM (created_at AT TIME ZONE 'UTC'))/86400 DESC,id").Limit(limit * 10).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Find(&candidates).Error; err != nil {
			return err
		}
		rank := map[int64]int{}
		for i, t := range candidates {
			rank[t.ID] = i
		}
		selected := []Task{}
		leaseKeys := map[int64]string{}
		seen := map[string]bool{}
		for _, t := range candidates {
			key, err := CanonicalEntity(ctx, tx, t.EntityKey)
			if err != nil {
				return err
			}
			leaseKeys[t.ID] = key
			if !seen[key] && len(selected) < limit {
				selected = append(selected, t)
				seen[key] = true
			}
		}
		// A stable entity order prevents deadlocks between concurrent batch claims.
		sort.Slice(selected, func(i, j int) bool { return leaseKeys[selected[i].ID] < leaseKeys[selected[j].ID] })
		for _, t := range selected {
			var lease struct {
				Fencing    int64
				LeaseUntil time.Time
			}
			r := tx.Raw(`INSERT INTO seo_entity_leases(entity_key,task_id,owner,fencing,lease_until) VALUES(?,?,?,?,clock_timestamp()+interval '20 minutes') ON CONFLICT(entity_key) DO UPDATE SET task_id=EXCLUDED.task_id,owner=EXCLUDED.owner,fencing=GREATEST(seo_entity_leases.fencing,EXCLUDED.fencing-1)+1,lease_until=EXCLUDED.lease_until WHERE seo_entity_leases.lease_until IS NULL OR seo_entity_leases.lease_until<=clock_timestamp() RETURNING fencing,lease_until`, leaseKeys[t.ID], t.ID, owner, t.Fencing+1).Scan(&lease)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				continue
			}
			if err := tx.Model(&Task{}).Where("id=?", t.ID).Updates(map[string]any{"state": "leased", "lease_owner": owner, "lease_until": lease.LeaseUntil, "fencing": lease.Fencing, "attempts": gorm.Expr("attempts+1"), "last_attempt_at": gorm.Expr("clock_timestamp()"), "updated_at": gorm.Expr("clock_timestamp()"), "next_retry_at": gorm.Expr("CASE WHEN kind='hermes_import' THEN CASE WHEN evidence->>'field' IN ('price','repoUrl','screenshots','icon','macos') THEN clock_timestamp()+interval '30 days' ELSE NULL END ELSE next_retry_at END")}).Error; err != nil {
				return err
			}
			if err := tx.First(&t, t.ID).Error; err != nil {
				return err
			}
			out = append(out, t)
		}
		sort.Slice(out, func(i, j int) bool { return rank[out[i].ID] < rank[out[j].ID] })
		return nil
	})
	return out, err
}

// Verify locks both records until commit and uses database time, not worker time.
func Verify(ctx context.Context, tx *gorm.DB, id, fencing int64, owner string) (Task, error) {
	var task Task
	if id <= 0 || fencing <= 0 || owner == "" {
		return task, ErrLease
	}
	err := tx.WithContext(ctx).Where("id=? AND state='leased' AND lease_owner=? AND fencing=? AND lease_until>clock_timestamp() AND (expires_at IS NULL OR expires_at>clock_timestamp())", id, owner, fencing).Clauses(clause.Locking{Strength: "UPDATE"}).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return task, ErrLease
	}
	if err != nil {
		return task, err
	}
	key, err := CanonicalEntity(ctx, tx, task.EntityKey)
	if err != nil {
		return task, err
	}
	var entity string
	r := tx.WithContext(ctx).Raw(`SELECT entity_key FROM seo_entity_leases WHERE entity_key=? AND task_id=? AND owner=? AND fencing=? AND lease_until>clock_timestamp() FOR UPDATE`, key, id, owner, fencing).Scan(&entity)
	if r.Error != nil {
		return task, r.Error
	}
	if r.RowsAffected != 1 {
		return task, ErrLease
	}
	return task, nil
}

func (s *Service) Defer(ctx context.Context, id, fencing int64, owner, reason string, retry time.Time) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 1000 {
		return ErrInvalid
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := Verify(ctx, tx, id, fencing, owner)
		if err != nil {
			return err
		}
		var valid bool
		if err := tx.Raw("SELECT ?::timestamptz>clock_timestamp() AND ?::timestamptz<=clock_timestamp()+interval '30 days'", retry, retry).Scan(&valid).Error; err != nil {
			return err
		}
		if !valid {
			return ErrInvalid
		}
		result, _ := json.Marshal(map[string]any{"reason": reason})
		if err := tx.Model(&Task{}).Where("id=?", id).Updates(map[string]any{"state": "deferred", "lease_owner": nil, "lease_until": nil, "next_retry_at": retry, "result": gorm.Expr("?::jsonb", string(result)), "updated_at": gorm.Expr("clock_timestamp()")}).Error; err != nil {
			return err
		}
		return release(tx, task, owner)
	})
}

func release(tx *gorm.DB, t Task, owner string) error {
	key, err := CanonicalEntity(tx.Statement.Context, tx, t.EntityKey)
	if err != nil {
		return err
	}
	r := tx.Exec("UPDATE seo_entity_leases SET owner=NULL,task_id=NULL,lease_until=NULL WHERE entity_key=? AND task_id=? AND owner=? AND fencing=?", key, t.ID, owner, t.Fencing)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrLease
	}
	return nil
}

func Owner(clientID, tokenID int64) string { return fmt.Sprintf("agent:%d:%d", clientID, tokenID) }
