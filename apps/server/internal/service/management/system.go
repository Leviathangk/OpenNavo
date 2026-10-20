package management

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opennavo/opennavo/server/internal/about"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/searchtext"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

func stringList(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, domain.Validation()
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range list {
		str, ok := v.(string)
		if !ok || str == "" || seen[str] {
			return nil, domain.Validation()
		}
		seen[str] = true
		out = append(out, str)
	}
	return out, nil
}
func (s *Service) systemWrite(ctx context.Context, op string, in Input) (any, error) {
	entity := ""
	switch {
	case strings.Contains(op, "Mirror"):
		entity = "mirror"
	case strings.Contains(op, "Synonym"):
		entity = "synonym"
	case strings.Contains(op, "Feedback"):
		entity = "feedback"
	case strings.Contains(op, "AdminUser"):
		entity = "admin_user"
	case strings.Contains(op, "Role"):
		entity = "role"
	case op == "UpdateAppConfig":
		entity = "app_config"
	}
	passwordHash := ""
	if op == "CreateAdminUser" || op == "ResetAdminUserPassword" {
		password := text(in.Body, "password")
		if op == "ResetAdminUserPassword" {
			password = text(in.Body, "newPassword")
		}
		var err error
		passwordHash, err = seed.HashPassword(password)
		if err != nil {
			return nil, err
		}
	}
	result, err := s.write(ctx, in, op, entity, func(st *store.Store) (any, error) {
		id := in.ID
		var result any
		switch op {
		case "CreateMirror", "UpdateMirror":
			fields := mapped(in.Body, map[string]string{"key": "key", "apiDomain": "api_domain", "bottleDomain": "bottle_domain", "brewGitRemote": "brew_git_remote", "coreGitRemote": "core_git_remote", "probeUrl": "probe_url", "recommended": "recommended", "enabled": "enabled", "sort": "sort"})
			fields["updated_by"] = in.Actor.ID
			fields["updated_at"] = s.now()
			if op == "CreateMirror" {
				if err := st.DB.WithContext(ctx).Table("mirrors").Create(fields).Error; err != nil {
					return nil, err
				}
				if err := st.DB.WithContext(ctx).Raw("SELECT id FROM mirrors WHERE key=?", text(in.Body, "key")).Scan(&id).Error; err != nil {
					return nil, err
				}
				result = map[string]any{"id": id}
			} else {
				if err := st.DB.WithContext(ctx).Table("mirrors").Where("id=?", id).Updates(fields).Error; err != nil {
					return nil, err
				}
			}
			if err := st.WriteLocalized(ctx, "mirror", id, 0, in.Body, true); err != nil {
				return nil, err
			}
		case "DeleteMirror":
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM mirrors WHERE id=?", id).Error; err != nil {
				return nil, err
			}
		case "UpdateAppConfig":
			if strings.HasPrefix(in.Key, "catalog.") || in.Key == "desktop.announcement" {
				return nil, fail(domain.CodeForbidden)
			}
			data, err := json.Marshal(in.Body["value"])
			if err != nil {
				return nil, err
			}
			if in.Key == about.Key {
				if _, err := about.Parse(data); err != nil {
					return nil, domain.Validation()
				}
			}
			if err := st.DB.WithContext(ctx).Exec("INSERT INTO app_config(key,value,description,updated_by,updated_at) VALUES(?,?::jsonb,?,?,?) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,description=EXCLUDED.description,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at", in.Key, string(data), in.Body["description"], in.Actor.ID, s.now()).Error; err != nil {
				return nil, err
			}

		case "CreateSynonym", "UpdateSynonym":
			terms, err := stringList(in.Body["terms"])
			if err != nil {
				return nil, err
			}
			normalized := []string{}
			seen := map[string]bool{}
			for _, v := range terms {
				v = searchtext.Normalize(v)
				if v == "" || seen[v] {
					return nil, domain.Validation()
				}
				seen[v] = true
				normalized = append(normalized, v)
			}
			if len(normalized) < 2 {
				return nil, domain.Validation()
			}
			data, err := json.Marshal(normalized)
			if err != nil {
				return nil, err
			}
			if op == "CreateSynonym" {
				if err := st.DB.WithContext(ctx).Raw("INSERT INTO search_synonyms(terms,enabled,updated_by) VALUES(ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?,?) RETURNING id", string(data), in.Body["enabled"], in.Actor.ID).Scan(&id).Error; err != nil {
					return nil, err
				}
				result = map[string]any{"id": id}
			} else {
				if err := st.DB.WithContext(ctx).Exec("UPDATE search_synonyms SET terms=ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),enabled=?,updated_by=?,updated_at=? WHERE id=?", string(data), in.Body["enabled"], in.Actor.ID, s.now(), id).Error; err != nil {
					return nil, err
				}
			}
		case "DeleteSynonym":
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM search_synonyms WHERE id=?", id).Error; err != nil {
				return nil, err
			}
		case "UpdateFeedback":
			if err := st.DB.WithContext(ctx).Exec("UPDATE feedback SET status=?,handler_note=?,handled_by=?,handled_at=? WHERE id=?", in.Body["status"], in.Body["handlerNote"], in.Actor.ID, s.now(), id).Error; err != nil {
				return nil, err
			}
		case "CreateAdminUser", "UpdateAdminUser":
			if op == "CreateAdminUser" {
				if err := st.DB.WithContext(ctx).Raw("INSERT INTO admin_users(user_name,password_hash,nick_name,email,status,created_by,updated_by) VALUES(?,?,?,?,?,?,?) RETURNING id", in.Body["userName"], passwordHash, in.Body["nickName"], in.Body["email"], in.Body["status"], in.Actor.ID, in.Actor.ID).Scan(&id).Error; err != nil {
					return nil, err
				}
				result = map[string]any{"id": id}
			} else {
				fields := mapped(in.Body, map[string]string{"nickName": "nick_name", "email": "email", "status": "status"})
				fields["updated_by"] = in.Actor.ID
				fields["updated_at"] = s.now()
				if err := st.DB.WithContext(ctx).Table("admin_users").Where("id=?", id).Updates(fields).Error; err != nil {
					return nil, err
				}
			}
			if raw, ok := in.Body["roles"]; ok {
				roles, err := stringList(raw)
				if err != nil {
					return nil, err
				}
				if err := st.DB.WithContext(ctx).Exec("DELETE FROM admin_user_roles WHERE user_id=?", id).Error; err != nil {
					return nil, err
				}
				for _, code := range roles {
					r := st.DB.WithContext(ctx).Exec("INSERT INTO admin_user_roles(user_id,role_id) SELECT ?,id FROM admin_roles WHERE role_code=?", id, code)
					if r.Error != nil {
						return nil, r.Error
					}
					if r.RowsAffected != 1 {
						return nil, domain.Validation()
					}
				}
			}
			var supers int64
			if err := st.DB.WithContext(ctx).Raw("SELECT count(*) FROM admin_users u JOIN admin_user_roles ur ON ur.user_id=u.id JOIN admin_roles r ON r.id=ur.role_id WHERE u.status='1' AND r.role_code='R_SUPER' AND r.status='1'").Scan(&supers).Error; err != nil {
				return nil, err
			}
			if supers == 0 {
				return nil, fail(domain.CodeInvalidState)
			}
		case "DeleteAdminUser":
			if in.Actor.ID != nil && id == *in.Actor.ID {
				return nil, fail(domain.CodeInvalidState)
			}
			var super bool
			if err := st.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM admin_user_roles ur JOIN admin_roles r ON r.id=ur.role_id WHERE ur.user_id=? AND r.role_code='R_SUPER')", id).Scan(&super).Error; err != nil {
				return nil, err
			}
			if super {
				var remaining int64
				if err := st.DB.WithContext(ctx).Raw("SELECT count(*) FROM admin_users u JOIN admin_user_roles ur ON ur.user_id=u.id JOIN admin_roles r ON r.id=ur.role_id WHERE u.id<>? AND u.status='1' AND r.role_code='R_SUPER' AND r.status='1'", id).Scan(&remaining).Error; err != nil {
					return nil, err
				}
				if remaining == 0 {
					return nil, fail(domain.CodeInvalidState)
				}
			}
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM admin_users WHERE id=?", id).Error; err != nil {
				return nil, err
			}
		case "ResetAdminUserPassword", "ForceLogoutAdminUser":
			fields := map[string]any{"session_version": gorm.Expr("session_version+1"), "updated_by": in.Actor.ID, "updated_at": s.now(), "failed_logins": 0, "locked_until": nil}
			if op == "ResetAdminUserPassword" {
				fields["password_hash"] = passwordHash
			}
			if err := st.DB.WithContext(ctx).Table("admin_users").Where("id=?", id).Updates(fields).Error; err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec("UPDATE admin_refresh_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE user_id=?", s.now(), id).Error; err != nil {
				return nil, err
			}
		case "CreateRole", "UpdateRole":
			fields := mapped(in.Body, map[string]string{"roleCode": "role_code", "roleName": "role_name", "roleDesc": "role_desc", "status": "status"})
			fields["updated_at"] = s.now()
			if op == "CreateRole" {
				if text(in.Body, "roleCode") == "R_SUPER" {
					return nil, domain.Validation()
				}
				if err := st.DB.WithContext(ctx).Table("admin_roles").Create(fields).Error; err != nil {
					return nil, err
				}
				if err := st.DB.WithContext(ctx).Raw("SELECT id FROM admin_roles WHERE role_code=?", text(in.Body, "roleCode")).Scan(&id).Error; err != nil {
					return nil, err
				}
				result = map[string]any{"id": id}
			} else {
				var r struct {
					RoleCode string
					Builtin  bool
				}
				if err := st.DB.WithContext(ctx).Raw("SELECT role_code,builtin FROM admin_roles WHERE id=?", id).Scan(&r).Error; err != nil {
					return nil, err
				}
				if r.Builtin && text(in.Body, "roleCode") != r.RoleCode {
					return nil, fail(domain.CodeInvalidState)
				}
				if r.RoleCode == "R_SUPER" && text(in.Body, "status") != "1" {
					return nil, fail(domain.CodeInvalidState)
				}
				if err := st.DB.WithContext(ctx).Table("admin_roles").Where("id=?", id).Updates(fields).Error; err != nil {
					return nil, err
				}
			}
		case "DeleteRole":
			var r struct {
				Builtin bool
				Used    int64
			}
			if err := st.DB.WithContext(ctx).Raw("SELECT builtin,(SELECT count(*) FROM admin_user_roles WHERE role_id=?) AS used FROM admin_roles WHERE id=?", id, id).Scan(&r).Error; err != nil {
				return nil, err
			}
			if r.Builtin || r.Used > 0 {
				return nil, fail(domain.CodeInvalidState)
			}
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM admin_roles WHERE id=?", id).Error; err != nil {
				return nil, err
			}
		case "SetRolePermissions":
			codes, err := stringList(in.Body["permissionCodes"])
			if err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM admin_role_permissions WHERE role_id=?", id).Error; err != nil {
				return nil, err
			}
			for _, code := range codes {
				if err := st.DB.WithContext(ctx).Exec("INSERT INTO admin_role_permissions(role_id,permission_code) VALUES(?,?)", id, code).Error; err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("unknown system write")
		}
		return result, nil
	})
	if err == nil && (entity == "admin_user" || entity == "role") && s.Store.Redis != nil {
		var cursor uint64
		for {
			keys, next, e := s.Store.Redis.Scan(ctx, cursor, "admin:perm:*", 100).Result()
			if e != nil {
				return nil, e
			}
			if len(keys) > 0 {
				if err := s.Store.Redis.Unlink(ctx, keys...).Err(); err != nil {
					return nil, err
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return result, err
}
