package management

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
)

const agentCallJSON = `jsonb_build_object('id',r.id,'requestId',r.request_id,'clientId',r.client_id,'clientName',r.client_name,'tokenId',r.token_id,'tokenPrefix',r.token_prefix,'tool',r.tool,'operationId',r.operation_id,'objects',r.objects,'code',r.code,'durationMs',r.duration_ms,'dryRun',r.dry_run,'paramsSummary',r.params_summary,'resultSummary',r.result_summary,'canRevert',NOT r.dry_run AND r.code='0000' AND EXISTS(SELECT 1 FROM content_revision_requests q WHERE q.request_id=r.request_id AND q.reversible AND q.revision_count>0 AND q.revision_count=(SELECT count(*) FROM content_revisions v WHERE v.request_id=q.request_id)) AND NOT EXISTS(SELECT 1 FROM content_request_reverts z WHERE z.request_id=r.request_id),'revertedByRequestId',(SELECT reverted_by_request_id FROM content_request_reverts z WHERE z.request_id=r.request_id),'createdAt',r.created_at)`
const translationLogJSON = `jsonb_build_object('id',r.id,'ref',jsonb_build_object('entity',r.entity,'id',CASE WHEN r.entity='announcement' THEN 0 ELSE split_part(r.object_key,':',1)::bigint END)||CASE WHEN r.entity='collection_item' THEN jsonb_build_object('secondaryId',split_part(r.object_key,':',2)::bigint) ELSE '{}'::jsonb END,'fields',r.fields,'sourceLocale',r.source_locale,'targetLocales',r.target_locales,'model',r.model,'promptTokens',r.prompt_tokens,'completionTokens',r.completion_tokens,'reasoningTokens',r.reasoning_tokens,'cachedTokens',r.cached_tokens,'latencyMs',r.latency_ms,'status',r.status,'reason',r.reason,'requestId',r.request_id,'actor',jsonb_build_object('type',CASE WHEN r.agent_client_id IS NOT NULL THEN 'agent' WHEN r.actor_id IS NOT NULL THEN 'admin' ELSE 'system' END,'id',r.actor_id,'name',COALESCE((SELECT name FROM agent_clients WHERE id=r.agent_client_id),(SELECT user_name FROM admin_users WHERE id=r.actor_id),'')),'createdAt',r.created_at)`

func (s *Service) agentLogs(ctx context.Context, op string, in Input) (any, error) {
	expr, table := agentCallJSON, "agent_call_logs"
	translation := strings.Contains(op, "TranslationLog")
	if translation {
		expr, table = translationLogJSON, "translation_logs"
	}
	where, args := "TRUE", []any{}
	clientCol := "r.client_id"
	if translation {
		clientCol = "r.agent_client_id"
	}
	if scope := domain.CurrentOperation(ctx); scope != nil && scope.AgentClientID > 0 {
		where += " AND " + clientCol + "=?"
		args = append(args, scope.AgentClientID)
		allowed := allowedHistoryEntities(scope)
		if translation {
			where += " AND r.entity IN ?"
			args = append(args, allowed)
		} else {
			raw, _ := json.Marshal(allowed)
			where += " AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(r.objects) o WHERE NOT (o->>'entity'=ANY(ARRAY(SELECT jsonb_array_elements_text(?::jsonb)))))"
			args = append(args, string(raw))
		}

	}
	if !translation {
		objectFilters := []string{}
		for param, field := range map[string]string{"entity": "entity", "objectkey": "objectKey"} {
			if value := text(in.Params, param); value != "" {
				objectFilters = append(objectFilters, "o->>'"+field+"'=?")
				args = append(args, value)
			}
		}
		if len(objectFilters) > 0 {
			where += " AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.objects) o WHERE " + strings.Join(objectFilters, " AND ") + ")"
		}
	}
	for param, col := range map[string]string{"requestid": "r.request_id", "tool": "r.tool", "code": "r.code", "status": "r.status", "entity": "r.entity", "objectkey": "r.object_key"} {
		if v := text(in.Params, param); v != "" {
			if translation && (param == "tool" || param == "code") {
				continue
			}
			if !translation && (param == "status" || param == "entity" || param == "objectkey") {
				continue
			}
			where += " AND " + col + "=?"
			args = append(args, v)
		}
	}
	for param, col := range map[string]string{"clientid": clientCol, "tokenid": "r.token_id"} {
		if n := number(in.Params, param, 0); n > 0 && (!translation || param != "tokenid") {
			where += " AND " + col + "=?"
			args = append(args, n)
		}
	}
	for param, cmp := range map[string]string{"from": ">=", "to": "<"} {
		if v := text(in.Params, param); v != "" {
			where += " AND r.created_at" + cmp + "?::timestamptz"
			args = append(args, v)
		}
	}
	if translation {
		if v := text(in.Params, "locale"); v != "" {
			where += " AND r.target_locales @> ?::jsonb"
			args = append(args, `["`+v+`"]`)
		}
	} else {
		if v, ok := in.Params["dryrun"].(bool); ok {
			where += " AND r.dry_run=?"
			args = append(args, v)
		}
	}
	if strings.HasPrefix(op, "Get") {
		where += " AND r.id=?"
		args = append(args, in.ID)
		return s.Store.JSONRow(ctx, "SELECT "+expr+" data FROM "+table+" r WHERE "+where, args...)
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	return s.Store.AdminPage(ctx, expr, "FROM "+table+" r", where, "r.created_at DESC,r.id DESC", args, current, size)
}
func (s *Service) invalidate(ctx context.Context) error {
	if s.Store.Redis != nil {
		return cache.PublishInvalidation(ctx, s.Store.Redis, "c:*")
	}
	return nil
}
