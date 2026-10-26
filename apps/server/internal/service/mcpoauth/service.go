// Package mcpoauth implements the MCP OAuth authorization server. Credentials are
// opaque and only their hashes are persisted. Agent permissions remain authoritative.
package mcpoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

const AccessTTL = 15 * time.Minute
const GrantTTL = 30 * 24 * time.Hour

type Error string

func (e Error) Error() string { return string(e) }

type Service struct {
	Store  *store.Store
	Issuer string
}

func (s *Service) Resource() string { return s.Issuer + "/mcp" }

type Client struct {
	ID           string          `json:"client_id"`
	Name         string          `json:"client_name"`
	RedirectURIs json.RawMessage `json:"redirect_uris"`
	Scopes       string          `json:"scope"`
}
type Registration struct {
	Name          string   `json:"client_name"`
	RedirectURIs  []string `json:"redirect_uris"`
	Scope         string   `json:"scope"`
	AuthMethod    string   `json:"token_endpoint_auth_method"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
}

func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname()))
}

// retiredScopes maps the scopes of the retired SEO engine, which clients registered
// before its removal still request, to their replacement; "" drops the scope. Remove
// in the release after (docs/design/seo-removal/be-a.md, 「下一版清理」).
var retiredScopes = map[string]string{"seo:insight:view": "site:index:view", "seo:task:handle": "site:index:view", "seo:snippet:propose": "", "seo:automation:tune": "", "seo:keyword:edit": ""}

func current(scope string) string {
	if next, retired := retiredScopes[scope]; retired {
		return next
	}
	return scope
}

// scopes validates a requested scope list against allowed, both possibly naming
// retired scopes. The limit applies to the list after mapping and merging.
func scopes(raw string, allowed []string) ([]string, error) {
	permitted := map[string]bool{}
	for _, p := range allowed {
		permitted[current(p)] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, p := range strings.Fields(raw) {
		if seen[p] {
			return nil, Error("invalid_scope")
		}
		seen[p] = true
		p = current(p)
		if p == "" || slices.Contains(out, p) {
			continue
		}
		if !permitted[p] {
			return nil, Error("invalid_scope")
		}
		out = append(out, p)
	}
	if len(out) == 0 || len(out) > len(agent.Grantable) {
		return nil, Error("invalid_scope")
	}
	return out, nil
}
func (s *Service) Register(ctx context.Context, in Registration) (Client, error) {
	var out Client
	if len(in.Name) == 0 || len(in.Name) > 120 || len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 || (in.AuthMethod != "" && in.AuthMethod != "none") {
		return out, Error("invalid_client_metadata")
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return out, Error("invalid_client_metadata")
		}
	}
	for _, r := range in.ResponseTypes {
		if r != "code" {
			return out, Error("invalid_client_metadata")
		}
	}
	for _, uri := range in.RedirectURIs {
		if !validRedirect(uri) {
			return out, Error("invalid_redirect_uri")
		}
	}
	if in.Scope == "" {
		in.Scope = strings.Join(agent.Grantable, " ")
	}
	list, err := scopes(in.Scope, agent.Grantable)
	if err != nil {
		return out, err
	}
	uris, err := json.Marshal(in.RedirectURIs)
	if err != nil {
		return out, err
	}
	out = Client{ID: rand.Text(), Name: in.Name, RedirectURIs: uris, Scopes: strings.Join(list, " ")}
	err = s.Store.DB.WithContext(ctx).Exec(`INSERT INTO mcp_oauth_clients(id,name,redirect_uris,scopes) VALUES(?,?,?::jsonb,?)`, out.ID, out.Name, string(uris), out.Scopes).Error
	return out, err
}

type Authorization struct {
	ClientID        string `json:"client_id"`
	RedirectURI     string `json:"redirect_uri"`
	ResponseType    string `json:"response_type"`
	Scope           string `json:"scope"`
	State           string `json:"state"`
	Challenge       string `json:"code_challenge"`
	ChallengeMethod string `json:"code_challenge_method"`
	Resource        string `json:"resource"`
}

var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func (s *Service) Validate(ctx context.Context, in Authorization) (Client, []string, error) {
	var c Client
	if err := s.Store.DB.WithContext(ctx).Raw(`SELECT * FROM mcp_oauth_clients WHERE id=?`, in.ClientID).Scan(&c).Error; err != nil {
		return c, nil, err
	}
	if c.ID == "" {
		return c, nil, Error("invalid_client")
	}
	var uris []string
	if err := json.Unmarshal(c.RedirectURIs, &uris); err != nil {
		return c, nil, err
	}
	// Exact match, including loopback ports: clients register their current callback.
	if !slices.Contains(uris, in.RedirectURI) {
		return c, nil, Error("invalid_redirect_uri")
	}
	if in.ResponseType != "code" {
		return c, nil, Error("unsupported_response_type")
	}
	if in.Resource != s.Resource() {
		return c, nil, Error("invalid_target")
	}
	if in.ChallengeMethod != "S256" || !challengePattern.MatchString(in.Challenge) || len(in.State) > 2048 {
		return c, nil, Error("invalid_request")
	}
	raw := in.Scope
	if raw == "" {
		raw = c.Scopes
	}
	list, err := scopes(raw, strings.Fields(c.Scopes))
	return c, list, err
}
func (s *Service) Allowed(ctx context.Context, userID int64) ([]string, error) {
	p, err := s.Store.Permissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, v := range agent.Grantable {
		if slices.Contains(p.Buttons, v) {
			out = append(out, v)
		}
	}
	return out, nil
}
func redirect(in Authorization, key, value string) string {
	u, _ := url.Parse(in.RedirectURI)
	q := u.Query()
	q.Set(key, value)
	if in.State != "" {
		q.Set("state", in.State)
	}
	q.Set("iss", strings.TrimSuffix(in.Resource, "/mcp"))
	u.RawQuery = q.Encode()
	return u.String()
}
func (s *Service) Consent(ctx context.Context, id auth.Identity, in Authorization, selected []string, allowDelete, approve bool, actor store.AuditActor) (string, error) {
	client, requested, err := s.Validate(ctx, in)
	if err != nil {
		return "", err
	}
	if !approve {
		return redirect(in, "error", "access_denied"), nil
	}
	allowed, err := s.Allowed(ctx, id.ID)
	if err != nil {
		return "", err
	}
	selected, err = scopes(strings.Join(selected, " "), requested)
	if err != nil {
		return "", err
	}
	for _, p := range selected {
		if !slices.Contains(allowed, p) {
			return "", Error("invalid_scope")
		}
	}
	code := rand.Text()
	now := time.Now().UTC()
	err = s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		var u store.AuthUser
		if err := tx.Raw(`SELECT * FROM admin_users WHERE id=? FOR UPDATE`, id.ID).Scan(&u).Error; err != nil {
			return err
		}
		if u.ID == 0 || u.Status != "1" || u.IsMachine || u.SessionVersion != id.SV {
			return Error("access_denied")
		}
		var enabled bool
		if err := tx.Raw(`SELECT enabled FROM agent_settings WHERE id=true`).Scan(&enabled).Error; err != nil {
			return err
		}
		if !enabled {
			return Error("access_denied")
		}
		var machineID, agentID, grantID int64
		if err := tx.Raw(`INSERT INTO admin_users(user_name,nick_name,password_hash,is_machine,created_by) VALUES(?,?,'!machine-account',true,?) RETURNING id`, "oauth-"+rand.Text(), client.Name, id.ID).Scan(&machineID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO admin_user_roles(user_id,role_id) SELECT ?,id FROM admin_roles WHERE role_code='R_AGENT'`, machineID).Error; err != nil {
			return err
		}
		if err := tx.Raw(`INSERT INTO agent_clients(user_id,name,notes,created_by) VALUES(?,?,'MCP OAuth authorization',?) RETURNING id`, machineID, client.Name, id.ID).Scan(&agentID).Error; err != nil {
			return err
		}
		if err := tx.Raw(`INSERT INTO mcp_oauth_grants(client_id,user_id,session_version,agent_client_id,scopes,allow_delete,expires_at) VALUES(?,?,?,?,?,?,?) RETURNING id`, client.ID, id.ID, id.SV, agentID, strings.Join(selected, " "), allowDelete, now.Add(GrantTTL)).Scan(&grantID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO mcp_oauth_codes(token_hash,grant_id,redirect_uri,challenge,resource,expires_at) VALUES(?,?,?,?,?,?)`, agent.Hash(code), grantID, in.RedirectURI, in.Challenge, in.Resource, now.Add(5*time.Minute)).Error; err != nil {
			return err
		}
		return store.Audit(ctx, tx, actor, "oauth.authorize", "oauth_grant", strconv.FormatInt(grantID, 10), nil, map[string]any{"clientId": client.ID, "scopes": selected, "allowDelete": allowDelete})
	})
	if err != nil {
		return "", err
	}
	return redirect(in, "code", code), nil
}

type grant struct {
	ID, UserID, AgentClientID int64
	SessionVersion            int
	ClientID, Scopes          string
	AllowDelete               bool
	ExpiresAt                 time.Time
	RevokedAt                 *time.Time
}
type credential struct {
	GrantID                          int64
	RedirectURI, Challenge, Resource string
	ExpiresAt                        time.Time
	ConsumedAt                       *time.Time
}
type TokenRequest struct{ GrantType, ClientID, Code, RedirectURI, Verifier, RefreshToken, Resource, Scope string }
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func revoke(tx *gorm.DB, id int64) error {
	if err := tx.Exec(`UPDATE mcp_oauth_grants SET revoked_at=COALESCE(revoked_at,now()) WHERE id=?`, id).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE agent_tokens SET revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE oauth_grant_id=?`, id).Error
}
func (s *Service) Exchange(ctx context.Context, in TokenRequest) (TokenResponse, error) {
	var out TokenResponse
	table, plain := "mcp_oauth_codes", in.Code
	if in.GrantType == "refresh_token" {
		table, plain = "mcp_oauth_refresh_tokens", in.RefreshToken
	} else if in.GrantType != "authorization_code" {
		return out, Error("unsupported_grant_type")
	}
	if plain == "" || len(plain) > 256 || in.ClientID == "" {
		return out, Error("invalid_grant")
	}
	if in.Resource != s.Resource() {
		return out, Error("invalid_target")
	}
	var result error
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		var cr credential
		if err := tx.Raw("SELECT * FROM "+table+" WHERE token_hash=?", agent.Hash(plain)).Scan(&cr).Error; err != nil {
			return err
		}
		if cr.GrantID == 0 {
			return Error("invalid_grant")
		}
		var g grant
		// All operations on a grant lock it first, serializing refresh, replay and revocation.
		if err := tx.Raw(`SELECT * FROM mcp_oauth_grants WHERE id=? FOR UPDATE`, cr.GrantID).Scan(&g).Error; err != nil {
			return err
		}
		if g.ClientID != in.ClientID || cr.Resource != in.Resource {
			return Error("invalid_grant")
		}
		if err := tx.Raw("SELECT * FROM "+table+" WHERE token_hash=?", agent.Hash(plain)).Scan(&cr).Error; err != nil {
			return err
		}
		if in.GrantType == "authorization_code" {
			sum := sha256.Sum256([]byte(in.Verifier))
			challenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if in.RedirectURI != cr.RedirectURI || !verifierPattern.MatchString(in.Verifier) || subtle.ConstantTimeCompare([]byte(challenge), []byte(cr.Challenge)) != 1 {
				return Error("invalid_grant")
			}
		}
		if cr.ConsumedAt != nil {
			result = Error("invalid_grant")
			return revoke(tx, g.ID)
		}
		now := time.Now().UTC()
		if g.RevokedAt != nil || !g.ExpiresAt.After(now) || !cr.ExpiresAt.After(now) {
			return Error("invalid_grant")
		}
		var active bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM admin_users u JOIN agent_clients c ON c.id=? JOIN admin_users m ON m.id=c.user_id CROSS JOIN agent_settings s WHERE u.id=? AND u.status='1' AND NOT u.is_machine AND u.session_version=? AND c.enabled AND m.status='1' AND s.enabled)`, g.AgentClientID, g.UserID, g.SessionVersion).Scan(&active).Error; err != nil {
			return err
		}
		if !active {
			return Error("invalid_grant")
		}
		st := &store.Store{DB: tx}
		p, err := st.Permissions(ctx, g.UserID)
		if err != nil {
			return err
		}
		list, err := scopes(g.Scopes, p.Buttons)
		if err != nil {
			return Error("invalid_grant")
		}
		if in.Scope != "" {
			list, err = scopes(in.Scope, list)
			if err != nil {
				return err
			}
			g.Scopes = strings.Join(list, " ")
			if err := tx.Exec(`UPDATE mcp_oauth_grants SET scopes=? WHERE id=?`, g.Scopes, g.ID).Error; err != nil {
				return err
			}
		}
		access, hash, err := agent.Token()
		if err != nil {
			return err
		}
		permissions, err := json.Marshal(list)
		if err != nil {
			return err
		}
		expiry := now.Add(AccessTTL)
		if g.ExpiresAt.Before(expiry) {
			expiry = g.ExpiresAt
		}
		if err := tx.Exec(`INSERT INTO agent_tokens(client_id,name,token_hash,token_prefix,permissions,allow_delete,expires_at,created_by,oauth_grant_id) VALUES(?,'OAuth access token',?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?,?,?,?)`, g.AgentClientID, hash, access[:8], string(permissions), g.AllowDelete, expiry, g.UserID, g.ID).Error; err != nil {
			return err
		}
		refresh := rand.Text()
		if err := tx.Exec(`INSERT INTO mcp_oauth_refresh_tokens(token_hash,grant_id,resource,expires_at) VALUES(?,?,?,?)`, agent.Hash(refresh), g.ID, s.Resource(), g.ExpiresAt).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE "+table+" SET consumed_at=? WHERE token_hash=?", now, agent.Hash(plain)).Error; err != nil {
			return err
		}
		// The issued scope, which differs from the stored one when that names retired scopes.
		out = TokenResponse{access, "Bearer", int(expiry.Sub(now).Seconds()), refresh, strings.Join(list, " ")}
		return nil
	})
	if err != nil {
		return TokenResponse{}, err
	}
	if result != nil {
		return TokenResponse{}, result
	}
	return out, nil
}
func (s *Service) Revoke(ctx context.Context, clientID, token string) error {
	if len(token) > 256 {
		return nil
	}
	return s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		var id int64
		if err := tx.Raw(`SELECT g.id FROM mcp_oauth_grants g WHERE g.client_id=? AND (g.id IN (SELECT grant_id FROM mcp_oauth_refresh_tokens WHERE token_hash=?) OR g.id IN (SELECT oauth_grant_id FROM agent_tokens WHERE token_hash=?)) FOR UPDATE`, clientID, agent.Hash(token), agent.Hash(token)).Scan(&id).Error; err != nil {
			return err
		}
		if id == 0 {
			return nil
		}
		return revoke(tx, id)
	})
}
func (s *Service) RevokeOwned(ctx context.Context, userID, id int64, actor store.AuditActor) error {
	return s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		var found int64
		if err := tx.Raw(`SELECT id FROM mcp_oauth_grants WHERE id=? AND user_id=? FOR UPDATE`, id, userID).Scan(&found).Error; err != nil {
			return err
		}
		if found == 0 {
			return Error("invalid_request")
		}
		if err := revoke(tx, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, actor, "oauth.revoke", "oauth_grant", strconv.FormatInt(id, 10), nil, map[string]any{"revoked": true})
	})
}
func ProtocolError(err error) string {
	var e Error
	if errors.As(err, &e) {
		return string(e)
	}
	return "server_error"
}
