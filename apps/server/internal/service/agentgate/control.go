package agentgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

var (
	// ErrInvalid means the idempotency key is malformed.
	ErrInvalid = errors.New("invalid agent write request")
	// ErrUnknownEntity means a release or screenshot key names no stored object.
	ErrUnknownEntity = errors.New("unknown write entity")
	// ErrIdempotencyKeyReused means the key already recorded a different request.
	ErrIdempotencyKeyReused = errors.New("idempotency key reused")
)

func Hash(raw []byte) string { return content.JSONHash(raw) }

func Owner(clientID, tokenID int64) string { return fmt.Sprintf("agent:%d:%d", clientID, tokenID) }

var errControlPreview = errors.New("agent write preview rollback")

// Control runs fn once per owner and idempotency key and replays its recorded
// result for an identical retry. A dry run always rolls fn back and records
// nothing. The key lock serializes a retry until the original commits.
func Control(ctx context.Context, db *gorm.DB, owner, operation, key string, payload any, dry bool, fn func(*gorm.DB) (any, error)) (any, error) {
	if owner == "" || len(key) < 8 || len(key) > 200 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n\x00") {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hash := Hash(raw)
	var result any
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "agent-write:control:"+owner+":"+key).Error; err != nil {
			return err
		}
		if !dry {
			var saved struct {
				Operation, RequestHash string
				Result                 json.RawMessage
			}
			r := tx.Raw("SELECT operation,request_hash,result FROM agent_write_controls WHERE owner=? AND idempotency_key=?", owner, key).Scan(&saved)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected > 0 {
				if saved.Operation != operation || saved.RequestHash != hash {
					return ErrIdempotencyKeyReused
				}
				return json.Unmarshal(saved.Result, &result)
			}
		}
		var err error
		result, err = fn(tx)
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
		return tx.Exec("INSERT INTO agent_write_controls(owner,idempotency_key,operation,request_hash,result) VALUES(?,?,?,?,?::jsonb)", owner, key, operation, hash, string(encoded)).Error
	})
	if errors.Is(err, errControlPreview) {
		err = nil
	}
	return result, err
}

// RevisionConflict rejects a write whose expected revision is stale and names
// the current one, so the caller can reread and retry.
func RevisionConflict(current int64) *domain.AppError {
	return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict, Msg: "rejected: revision_conflict:" + strconv.FormatInt(current, 10)}
}

// WriteError maps write-layer errors to API errors; others pass through.
func WriteError(err error) error {
	switch {
	case errors.Is(err, ErrIdempotencyKeyReused):
		return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict, Msg: "rejected: idempotency_key_reused", Cause: err}
	case errors.Is(err, ErrInvalid):
		return domain.Rejected("idempotency_key_invalid")
	case errors.Is(err, ErrUnknownEntity):
		return domain.Rejected("entity_invalid")
	}
	if rejected, _, ok := Rejection(err, http.StatusUnprocessableEntity); ok {
		return rejected
	}
	return err
}
