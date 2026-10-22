package management

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
)

// nominateTopic lets an agent choose an article topic itself. It creates the
// topic task (entity article:new:<slug>) that the article workflow claims like a
// keyword topic; keyword demand stays the other source. Nominating publishes
// nothing, and the article budget applies when the article is written.
func (s *Service) nominateTopic(ctx context.Context, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "seo:task:handle"); err != nil {
		return nil, err
	}
	if err := requireSEOAccess(ctx, "content:article:edit"); err != nil {
		return nil, err
	}
	var req struct {
		IdempotencyKey string `json:"idempotencyKey"`
		Keyword        string `json:"keyword"`
		Locale         string `json:"locale"`
		Reason         string `json:"reason"`
	}
	if err := decodeStrictBody(in.Body, &req); err != nil {
		return nil, err
	}
	req.Keyword, req.Reason = strings.TrimSpace(req.Keyword), strings.TrimSpace(req.Reason)
	if req.Locale == "" {
		req.Locale = "en-US"
	}
	if !articles.ValidSlug(in.Key, 100) || req.Keyword == "" || len(req.Keyword) > 200 || req.Reason == "" || len(req.Reason) > 1000 || !i18n.Valid(req.Locale) {
		return nil, domain.Validation()
	}
	owner := seoOwner(ctx)
	entity, dedupe := "article:new:"+in.Key, "nominate-topic:"+in.Key
	raw, err := json.Marshal(map[string]any{"source": "nomination", "keyword": req.Keyword, "targetSlug": in.Key, "reason": req.Reason, "nominatedBy": owner})
	if err != nil {
		return nil, err
	}
	tasks := &seotasks.Service{DB: s.Store.DB}
	result, err := tasks.Control(ctx, owner, "NominateSeoTopic", req.IdempotencyKey, map[string]any{"slug": in.Key, "body": in.Body}, domain.CurrentOperation(ctx).DryRun, func(t *seotasks.Service) (any, error) {
		var taken bool
		if err := t.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM articles WHERE slug=?) OR EXISTS(SELECT 1 FROM article_slug_aliases WHERE slug=?)", in.Key, in.Key).Scan(&taken).Error; err != nil {
			return nil, err
		}
		if taken {
			return nil, &seotasks.StateError{Reason: "article_exists"}
		}
		// A topic already queued or in progress, from keywords or an earlier
		// nomination, is returned as is instead of starting a second article.
		var task seotasks.Task
		r := t.DB.WithContext(ctx).Raw("SELECT * FROM seo_tasks WHERE kind='topic' AND entity_key=? AND state IN ('open','leased','deferred') ORDER BY id LIMIT 1", entity).Scan(&task)
		if r.Error != nil {
			return nil, r.Error
		}
		if r.RowsAffected > 0 {
			return task, nil
		}
		r = t.DB.WithContext(ctx).Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version) VALUES('topic',?,?,?,100,?::jsonb,?,'nomination-v1')
  ON CONFLICT(dedupe_key) DO UPDATE SET locale=EXCLUDED.locale,evidence=EXCLUDED.evidence,evidence_hash=EXCLUDED.evidence_hash,priority=greatest(seo_tasks.priority,EXCLUDED.priority),state='open',next_retry_at=NULL,lease_owner=NULL,lease_until=NULL,pending_evidence=NULL,pending_evidence_hash=NULL,updated_at=clock_timestamp()
  WHERE seo_tasks.state IN ('done','skipped','expired') RETURNING *`, entity, req.Locale, dedupe, string(raw), seotasks.Hash(raw)).Scan(&task)
		if r.Error != nil {
			return nil, r.Error
		}
		if r.RowsAffected == 0 {
			if err := t.DB.WithContext(ctx).Raw("SELECT * FROM seo_tasks WHERE dedupe_key=?", dedupe).Scan(&task).Error; err != nil {
				return nil, err
			}
		}
		return task, nil
	})
	return result, mapTaskError(err)
}
