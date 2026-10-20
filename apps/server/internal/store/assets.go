package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

type AssetFile struct {
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	URL        string `json:"url"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	StorageKey string `json:"storageKey"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
}

type Asset struct {
	Files                               json.RawMessage
	ID                                  int64
	Kind, StorageKey, URL, MIME, SHA256 string
	Width, Height                       int
	Bytes                               int64
	SourceURL                           *string
	CreatedBy                           *int64
	CreatedAt                           time.Time
}
type AuditActor struct {
	ID            *int64
	IP, UserAgent string
}

func Audit(ctx context.Context, tx *gorm.DB, actor AuditActor, action, entity, id string, before, after any) error {
	previous, err := json.Marshal(before)
	if err != nil {
		return err
	}
	next, err := json.Marshal(after)
	if err != nil {
		return err
	}
	requestID := ""
	if op := domain.CurrentOperation(ctx); op != nil {
		requestID = op.RequestID
	}
	return tx.WithContext(ctx).Exec(`INSERT INTO audit_logs(actor_id,action,entity_type,entity_id,before,after,ip,user_agent,request_id) VALUES(?,?,?,?,?::jsonb,?::jsonb,?,?,?)`, actor.ID, action, entity, id, string(previous), string(next), nullable(actor.IP), nullable(actor.UserAgent), nullable(requestID)).Error
}
func (s *Store) SaveAsset(ctx context.Context, asset Asset, actor *AuditActor) (Asset, error) {
	if len(asset.Files) == 0 {
		asset.Files = json.RawMessage(`[]`)
	}
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		var identity struct {
			ID        int64
			CreatedAt time.Time
		}
		if err := tx.WithContext(ctx).Raw(`INSERT INTO assets(kind,storage_key,url,mime,width,height,bytes,sha256,source_url,created_by,files) VALUES(?,?,?,?,?,?,?,?,?,?,?::jsonb) ON CONFLICT(storage_key) DO UPDATE SET files=CASE WHEN EXCLUDED.files<>'[]'::jsonb THEN EXCLUDED.files ELSE assets.files END RETURNING id,created_at`, asset.Kind, asset.StorageKey, asset.URL, asset.MIME, asset.Width, asset.Height, asset.Bytes, asset.SHA256, asset.SourceURL, asset.CreatedBy, string(asset.Files)).Scan(&identity).Error; err != nil {
			return err
		}
		asset.ID, asset.CreatedAt = identity.ID, identity.CreatedAt
		if actor != nil {
			return Audit(ctx, tx, *actor, "asset.upload", "asset", asset.StorageKey, nil, map[string]any{"id": asset.ID, "kind": asset.Kind, "sha256": asset.SHA256})
		}
		return nil
	})
	return asset, err
}
