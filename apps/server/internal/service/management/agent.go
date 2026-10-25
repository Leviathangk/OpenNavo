package management

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

const clientJSON = `jsonb_build_object('id',c.id,'userId',c.user_id,'name',c.name,'notes',c.notes,'enabled',c.enabled,'activeTokenCount',(SELECT count(*) FROM agent_tokens t WHERE t.client_id=c.id AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>now())),'lastUsedAt',(SELECT max(t.last_used_at) FROM agent_tokens t WHERE t.client_id=c.id),'lastUsedIp',(SELECT host(t.last_used_ip) FROM agent_tokens t WHERE t.client_id=c.id ORDER BY t.last_used_at DESC NULLS LAST,t.id DESC LIMIT 1),'createdAt',c.created_at,'updatedAt',c.updated_at)`
const tokenJSON = `jsonb_build_object('id',t.id,'clientId',t.client_id,'name',t.name,'permissions',t.permissions,'allowDelete',t.allow_delete,'expiresAt',t.expires_at,'ipAllowlist',t.ip_allowlist,'prefix',t.token_prefix,'revokedAt',t.revoked_at,'lastUsedAt',t.last_used_at,'lastUsedIp',host(t.last_used_ip),'createdAt',t.created_at,'updatedAt',t.updated_at,'status',CASE WHEN t.revoked_at IS NOT NULL THEN 'revoked' WHEN t.expires_at<=now() THEN 'expired' ELSE 'active' END,'expiringSoon',t.revoked_at IS NULL AND COALESCE(t.expires_at>now() AND t.expires_at<=now()+interval '7 days',false))`

func (s *Service) agentSettings(ctx context.Context) (any, error) {
	var row struct {
		Enabled                                                bool
		CallsPerMinute, WritesPerDay, LLMOpsPerDay, BatchLimit int
		UpdatedAt                                              time.Time
	}
	if err := s.Store.DB.WithContext(ctx).Table("agent_settings").Where("id=true").Take(&row).Error; err != nil {
		return nil, err
	}
	mcpURL := s.Config.AdminOrigin + "/mcp"
	if s.Config.AppEnv == "dev" {
		mcpURL = "http://127.0.0.1:8790/mcp"
	}
	return map[string]any{"enabled": row.Enabled, "limits": agent.Limits{CallsPerMinute: row.CallsPerMinute, WritesPerDay: row.WritesPerDay, LLMOpsPerDay: row.LLMOpsPerDay, BatchLimit: row.BatchLimit}, "mcpUrl": mcpURL, "grantablePermissions": agent.Grantable, "updatedAt": row.UpdatedAt}, nil
}
func (s *Service) agentOperation(ctx context.Context, op string, in Input) (any, error) {
	switch op {
	case "GetAgentSettings":
		return s.agentSettings(ctx)
	case "ListAgentClients", "ListAgentTokens":
		current, size, err := page(in)
		if err != nil {
			return nil, err
		}
		expr, from, where, args := clientJSON, "FROM agent_clients c", "TRUE", []any{}
		if op == "ListAgentTokens" {
			if _, err := s.Store.JSONRow(ctx, "SELECT "+clientJSON+" data FROM agent_clients c WHERE c.id=?", in.ID); err != nil {
				return nil, err
			}
			expr, from, where, args = tokenJSON, "FROM agent_tokens t", "t.client_id=?", []any{in.ID}
		} else {
			if v, ok := in.Params["enabled"].(bool); ok {
				where += " AND c.enabled=?"
				args = append(args, v)
			}
			if q := text(in.Params, "q"); q != "" {
				where += " AND strpos(lower(c.name),lower(?))>0"
				args = append(args, q)
			}
		}
		return s.Store.AdminPage(ctx, expr, from, where, "id DESC", args, current, size)
	case "GetAgentClient":
		return s.Store.JSONRow(ctx, "SELECT "+clientJSON+" data FROM agent_clients c WHERE c.id=?", in.ID)
	}
	result, err := s.write(ctx, in, op, "agent", func(st *store.Store) (any, error) {
		switch op {
		case "UpdateAgentSettings":
			limits, ok := in.Body["limits"].(map[string]any)
			if !ok {
				return nil, domain.Validation()
			}
			fields := map[string]any{"enabled": in.Body["enabled"], "calls_per_minute": number(limits, "callsPerMinute", 0), "writes_per_day": number(limits, "writesPerDay", 0), "llm_ops_per_day": number(limits, "llmOpsPerDay", 0), "batch_limit": number(limits, "batchLimit", 0), "updated_by": in.Actor.ID, "updated_at": s.now(), "policy_version": gorm.Expr("policy_version+1")}
			if err := st.DB.WithContext(ctx).Table("agent_settings").Where("id=true").Updates(fields).Error; err != nil {
				return nil, err
			}
			copy := *s
			copy.Store = st
			return copy.agentSettings(ctx)
		case "CreateAgentClient":
			var userID, id int64
			if err := st.DB.WithContext(ctx).Raw(`INSERT INTO admin_users(user_name,nick_name,password_hash,is_machine,created_by) VALUES(?,?,'!machine-account',true,?) RETURNING id`, "agent-"+uuid.NewString(), text(in.Body, "name"), in.Actor.ID).Scan(&userID).Error; err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec(`INSERT INTO admin_user_roles(user_id,role_id) SELECT ?,id FROM admin_roles WHERE role_code='R_AGENT'`, userID).Error; err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Raw(`INSERT INTO agent_clients(user_id,name,notes,created_by) VALUES(?,?,?,?) RETURNING id`, userID, text(in.Body, "name"), text(in.Body, "notes"), in.Actor.ID).Scan(&id).Error; err != nil {
				return nil, err
			}
			return st.JSONRow(ctx, "SELECT "+clientJSON+" data FROM agent_clients c WHERE c.id=?", id)
		case "UpdateAgentClient":
			fields := mapped(in.Body, map[string]string{"name": "name", "notes": "notes", "enabled": "enabled"})
			fields["updated_at"] = s.now()
			r := st.DB.WithContext(ctx).Table("agent_clients").Where("id=?", in.ID).Updates(fields)
			if r.Error != nil {
				return nil, r.Error
			}
			if r.RowsAffected == 0 {
				return nil, fail(domain.CodeNotFound)
			}
			return st.JSONRow(ctx, "SELECT "+clientJSON+" data FROM agent_clients c WHERE c.id=?", in.ID)
		case "CreateAgentToken", "UpdateAgentToken", "RevokeAgentToken":
			fields := mapped(in.Body, map[string]string{"name": "name", "allowDelete": "allow_delete", "expiresAt": "expires_at"})
			fields["updated_at"] = s.now()
			for key, col := range map[string]string{"permissions": "permissions", "ipAllowlist": "ip_allowlist"} {
				if value, ok := in.Body[key]; ok {
					raw, err := json.Marshal(value)
					if err != nil {
						return nil, err
					}
					var list []string
					if json.Unmarshal(raw, &list) != nil {
						return nil, domain.Validation()
					}
					if key == "permissions" {
						if !agent.ValidPermissionSet(list) {
							return nil, domain.Validation()
						}
						for _, p := range list {
							if !slices.Contains(agent.Grantable, p) {
								return nil, domain.Validation()
							}
						}
					} else {
						for _, p := range list {
							if _, err := netip.ParsePrefix(p); err != nil {
								return nil, domain.Validation()
							}
						}
					}
					cast := "text"
					if key == "ipAllowlist" {
						cast = "cidr"
					}
					fields[col] = gorm.Expr("ARRAY(SELECT jsonb_array_elements_text(?::jsonb)::"+cast+")", string(raw))
				}
			}
			if raw, present := in.Body["expiresAt"]; present && raw != nil {
				expires, err := time.Parse(time.RFC3339, text(in.Body, "expiresAt"))
				if err != nil || !expires.After(s.now()) {
					return nil, domain.Validation()
				}
			}
			if op == "CreateAgentToken" {
				if _, err := st.JSONRow(ctx, "SELECT "+clientJSON+" data FROM agent_clients c WHERE c.id=?", in.ID); err != nil {
					return nil, err
				}
				plain, hash, err := agent.Token()
				if err != nil {
					return nil, err
				}
				fields["client_id"], fields["token_hash"], fields["token_prefix"], fields["created_by"] = in.ID, hash, plain[:8], in.Actor.ID
				if err := st.DB.WithContext(ctx).Table("agent_tokens").Create(fields).Error; err != nil {
					return nil, err
				}
				token, err := st.JSONRow(ctx, "SELECT "+tokenJSON+" data FROM agent_tokens t WHERE token_hash=?", hash)
				return map[string]any{"token": token, "plaintext": plain}, err
			}
			if op == "RevokeAgentToken" {
				// Revoking any OAuth access token also revokes its refresh family.
				// Lock the grant before tokens, matching the OAuth rotation order.
				var grantID int64
				if err := st.DB.WithContext(ctx).Raw(`SELECT g.id FROM mcp_oauth_grants g JOIN agent_tokens t ON t.oauth_grant_id=g.id WHERE t.id=? FOR UPDATE OF g`, in.ID).Scan(&grantID).Error; err != nil {
					return nil, err
				}
				if grantID != 0 {
					if err := st.DB.WithContext(ctx).Exec(`UPDATE mcp_oauth_grants SET revoked_at=COALESCE(revoked_at,now()) WHERE id=?`, grantID).Error; err != nil {
						return nil, err
					}
					if err := st.DB.WithContext(ctx).Exec(`UPDATE agent_tokens SET revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE oauth_grant_id=?`, grantID).Error; err != nil {
						return nil, err
					}
				}
				fields = map[string]any{"revoked_at": gorm.Expr("COALESCE(revoked_at,now())"), "updated_at": s.now()}
			}
			r := st.DB.WithContext(ctx).Table("agent_tokens").Where("id=?", in.ID).Updates(fields)
			if r.Error != nil {
				return nil, r.Error
			}
			if r.RowsAffected == 0 {
				return nil, fail(domain.CodeNotFound)
			}
			return st.JSONRow(ctx, "SELECT "+tokenJSON+" data FROM agent_tokens t WHERE t.id=?", in.ID)
		}
		return nil, domain.Validation()
	})
	return result, err
}
