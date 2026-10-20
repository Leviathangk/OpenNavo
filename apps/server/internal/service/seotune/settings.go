// Package seotune validates the independent tuner control plane.
package seotune

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"gorm.io/gorm"
)

var Actions = []string{"maintenance", "translation", "fact_fix", "withdraw", "compensate", "fact_fill", "content", "listing", "article", "snippet", "operations", "research"}

type Settings struct {
	Version               int                         `json:"version"`
	Actions               map[string]agentgate.Policy `json:"actions"`
	DailyEntities         map[string]int              `json:"dailyEntities"`
	ResearchDailyMicros   int64                       `json:"researchDailyMicros"`
	ResearchMonthlyMicros int64                       `json:"researchMonthlyMicros"`
	BusinessDropRatio     float64                     `json:"businessDropRatio"`
}

func Decode(raw []byte) (Settings, error) {
	var v Settings
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || v.Version != 1 || len(v.Actions) != len(Actions) || len(v.DailyEntities) != len(Actions) || v.ResearchDailyMicros < 0 || v.ResearchMonthlyMicros < v.ResearchDailyMicros || v.ResearchMonthlyMicros > 1000000000000 || v.BusinessDropRatio <= 0 || v.BusinessDropRatio >= 1 {
		return v, seotasks.ErrInvalid
	}
	for _, name := range Actions {
		p, ok := v.Actions[name]
		n, exists := v.DailyEntities[name]
		if !ok || !exists || n < 0 || n > 1000000 || !slices.Contains([]string{"live", "proposal"}, p.Mode) {
			return v, seotasks.ErrInvalid
		}
	}
	return v, nil
}

// Receipt serializes retries and captures the exact validated result in the same transaction.
// A dry run rolls the entire callback back, including the receipt.
func Receipt(ctx context.Context, db *gorm.DB, owner, operation, key, reason string, payload any, dry bool, fn func(*gorm.DB) (any, any, any, error)) (any, error) {
	if owner == "" || len(key) < 8 || len(key) > 200 || strings.TrimSpace(key) != key || len(reason) > 2000 {
		return nil, seotasks.ErrInvalid
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hash := seotasks.Hash(raw)
	var result any
	preview := errors.New("preview rollback")
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo-control:"+owner+":"+operation+":"+key).Error; err != nil {
			return err
		}
		var old struct {
			RequestHash string
			Result      json.RawMessage
		}
		r := tx.Raw("SELECT request_hash,result FROM seo_control_receipts WHERE owner=? AND operation=? AND idempotency_key=?", owner, operation, key).Scan(&old)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected > 0 {
			if old.RequestHash != hash {
				return seotasks.ErrConflict
			}
			return json.Unmarshal(old.Result, &result)
		}
		before, after, v, err := fn(tx)
		if err != nil {
			return err
		}
		result = v
		if dry {
			return preview
		}
		b, _ := json.Marshal(before)
		a, _ := json.Marshal(after)
		out, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return tx.Exec("INSERT INTO seo_control_receipts(owner,operation,idempotency_key,request_hash,before_data,after_data,result,reason) VALUES(?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?)", owner, operation, key, hash, string(b), string(a), string(out), reason).Error
	})
	if errors.Is(err, preview) {
		err = nil
	}
	return result, err
}
