//go:build integration

package httpserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/mcpoauth"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestMCPOAuthLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("oauth-isolated-test")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "oauth-admin", hash))
	cfg := config.Config{AdminOrigin: "https://admin.example.test", JWTSecret: strings.Repeat("o", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, AgentGatewaySecret: strings.Repeat("g", 32)}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "oauth-admin", "oauth-isolated-test", store.AuditActor{})
	require.NoError(t, err)
	manager := &management.Service{Store: st, Config: cfg}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: manager}})
	require.NoError(t, err)
	call := func(method, path, body, contentType, bearer, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		return w
	}
	post := func(path string, body any, bearer string) *httptest.ResponseRecorder {
		b, e := json.Marshal(body)
		require.NoError(t, e)
		return call("POST", path, string(b), "application/json", bearer, cfg.AdminOrigin)
	}
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource/mcp"} {
		w := call("GET", path, "", "", "", "https://client.example")
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, "https://client.example", w.Header().Get("Access-Control-Allow-Origin"))
	}
	w := post("/oauth/register", map[string]any{"client_name": "Test application", "redirect_uris": []string{"http://127.0.0.1:4321/callback"}}, "")
	require.Equal(t, 201, w.Code, w.Body.String())
	var client mcpoauth.Client
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &client))
	require.NotEmpty(t, client.ID)
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	authorization := mcpoauth.Authorization{ClientID: client.ID, RedirectURI: "http://127.0.0.1:4321/callback", ResponseType: "code", Scope: "catalog:package:view", State: "opaque-state", Challenge: base64.RawURLEncoding.EncodeToString(digest[:]), ChallengeMethod: "S256", Resource: cfg.AdminOrigin + "/mcp"}
	authorize := func() string {
		t.Helper()
		w := post("/oauth/consent", map[string]any{"authorization": authorization, "scopes": []string{"catalog:package:view"}, "approve": true}, pair.Token)
		require.Equal(t, 200, w.Code, w.Body.String())
		var result struct{ Redirect string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		u, e := url.Parse(result.Redirect)
		require.NoError(t, e)
		require.Equal(t, "opaque-state", u.Query().Get("state"))
		return u.Query().Get("code")
	}
	token := func(values url.Values, expected int) mcpoauth.TokenResponse {
		t.Helper()
		w := call("POST", "/oauth/token", values.Encode(), "application/x-www-form-urlencoded", "", "https://client.example")
		require.Equal(t, expected, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var out mcpoauth.TokenResponse
		if expected == 200 {
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		}
		return out
	}
	exchange := func(code string) url.Values {
		return url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ID}, "code": {code}, "redirect_uri": {authorization.RedirectURI}, "code_verifier": {verifier}, "resource": {authorization.Resource}}
	}
	refresh := func(value string) url.Values {
		return url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {value}, "resource": {authorization.Resource}}
	}
	agents := &agent.Service{Store: st, GatewaySecret: cfg.AgentGatewaySecret}
	valid := func(value string) bool {
		_, err := agents.Authenticate(ctx, cfg.AgentGatewaySecret, value, "127.0.0.1")
		return err == nil
	}
	code := authorize()
	bad := exchange(code)
	bad.Set("code_verifier", strings.Repeat("x", 43))
	token(bad, 400)
	bad = exchange(code)
	bad.Set("redirect_uri", "https://attacker.example/callback")
	token(bad, 400)
	bad = exchange(code)
	bad.Set("resource", "https://other.example/mcp")
	token(bad, 400)
	first := token(exchange(code), 200)
	require.True(t, valid(first.AccessToken))
	require.NotEmpty(t, first.RefreshToken)
	next := token(refresh(first.RefreshToken), 200)
	require.True(t, valid(next.AccessToken))
	require.NotEqual(t, first.RefreshToken, next.RefreshToken)
	token(refresh(first.RefreshToken), 400)
	require.False(t, valid(next.AccessToken))
	token(refresh(next.RefreshToken), 400)
	code = authorize()
	first = token(exchange(code), 200)
	token(exchange(code), 400)
	require.False(t, valid(first.AccessToken))
	code = authorize()
	first = token(exchange(code), 200)
	// Foreign client cannot revoke a grant; protocol revocation is non-enumerating.
	w = call("POST", "/oauth/revoke", url.Values{"client_id": {"foreign"}, "token": {first.RefreshToken}}.Encode(), "application/x-www-form-urlencoded", "", "")
	require.Equal(t, 200, w.Code)
	require.True(t, valid(first.AccessToken))
	w = call("POST", "/oauth/revoke", url.Values{"client_id": {client.ID}, "token": {first.RefreshToken}}.Encode(), "application/x-www-form-urlencoded", "", "")
	require.Equal(t, 200, w.Code)
	require.False(t, valid(first.AccessToken))
	token(refresh(first.RefreshToken), 400)
	code = authorize()
	first = token(exchange(code), 200)
	// User-owned revocation is visible in the UI and invalidates refresh as well.
	w = call("GET", "/oauth/grants", "", "", pair.Token, cfg.AdminOrigin)
	require.Equal(t, 200, w.Code)
	var grantPage struct{ Items []struct{ ID int64 } }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &grantPage))
	grants := grantPage.Items
	require.NotEmpty(t, grants)
	svc := mcpoauth.Service{Store: st, Issuer: cfg.AdminOrigin}
	identity, err := authentication.Authenticate(ctx, pair.Token)
	require.NoError(t, err)
	require.Error(t, svc.RevokeOwned(ctx, identity.ID+10000, grants[0].ID, store.AuditActor{}))
	require.True(t, valid(first.AccessToken))
	require.NoError(t, svc.RevokeOwned(ctx, identity.ID, grants[0].ID, store.AuditActor{ID: &identity.ID}))
	require.False(t, valid(first.AccessToken))
	token(refresh(first.RefreshToken), 400)
	// Bearer-protected consent rejects missing sessions and cross-origin writes.
	w = post("/oauth/consent", map[string]any{}, "")
	require.Equal(t, 401, w.Code)
	w = call("POST", "/oauth/consent", "{}", "application/json", pair.Token, "https://attacker.example")
	require.Equal(t, 403, w.Code)
	// Grant scope escalation is rejected; denial returns state without creating credentials.
	w = post("/oauth/consent", map[string]any{"authorization": authorization, "scopes": []string{"content:article:publish"}, "approve": true}, pair.Token)
	require.Equal(t, 400, w.Code)
	w = post("/oauth/consent", map[string]any{"authorization": authorization, "approve": false}, pair.Token)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "access_denied")
	// Expired authorization codes and access tokens cannot be used.
	code = authorize()
	require.NoError(t, st.DB.Exec(`UPDATE mcp_oauth_codes SET expires_at=now()-interval '1 second' WHERE token_hash=?`, agent.Hash(code)).Error)
	token(exchange(code), 400)
	code = authorize()
	first = token(exchange(code), 200)
	require.NoError(t, st.DB.Exec(`UPDATE agent_tokens SET created_at=now()-interval '1 hour',expires_at=now()-interval '1 second' WHERE token_hash=?`, agent.Hash(first.AccessToken)).Error)
	require.False(t, valid(first.AccessToken))
	next = token(refresh(first.RefreshToken), 200)
	require.True(t, valid(next.AccessToken))
	// Administrative token revocation must not be undone by a later refresh.
	var tokenID int64
	require.NoError(t, st.DB.Raw(`SELECT id FROM agent_tokens WHERE token_hash=?`, agent.Hash(next.AccessToken)).Scan(&tokenID).Error)
	_, err = manager.Execute(ctx, "RevokeAgentToken", management.Input{ID: tokenID, Actor: store.AuditActor{ID: &identity.ID}})
	require.NoError(t, err)
	require.False(t, valid(next.AccessToken))
	token(refresh(next.RefreshToken), 400)
	// Current owner permissions constrain already-issued access tokens.
	code = authorize()
	first = token(exchange(code), 200)
	var roleIDs []int64
	require.NoError(t, st.DB.Raw(`SELECT role_id FROM admin_user_roles WHERE user_id=?`, identity.ID).Scan(&roleIDs).Error)
	require.NoError(t, st.DB.Exec(`DELETE FROM admin_user_roles WHERE user_id=?`, identity.ID).Error)
	restricted, err := agents.Authenticate(ctx, cfg.AgentGatewaySecret, first.AccessToken, "127.0.0.1")
	require.NoError(t, err)
	require.Empty(t, restricted.Permissions)
	token(refresh(first.RefreshToken), 400)
	for _, roleID := range roleIDs {
		require.NoError(t, st.DB.Exec(`INSERT INTO admin_user_roles(user_id,role_id) VALUES(?,?)`, identity.ID, roleID).Error)
	}
	// Concurrent refreshes serialize on the grant: one succeeds and the replay revokes it.
	code = authorize()
	first = token(exchange(code), 200)
	var wg sync.WaitGroup
	outcomes := make(chan struct {
		token mcpoauth.TokenResponse
		err   error
	}, 2)
	for range 2 {
		wg.Go(func() {
			out, err := svc.Exchange(ctx, mcpoauth.TokenRequest{GrantType: "refresh_token", ClientID: client.ID, RefreshToken: first.RefreshToken, Resource: authorization.Resource})
			outcomes <- struct {
				token mcpoauth.TokenResponse
				err   error
			}{out, err}
		})
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for outcome := range outcomes {
		if outcome.err == nil {
			successes++
			require.False(t, valid(outcome.token.AccessToken))
		} else {
			require.Equal(t, "invalid_grant", mcpoauth.ProtocolError(outcome.err))
		}
	}
	require.Equal(t, 1, successes)

	t.Run("refresh preserves quotas", func(t *testing.T) {
		fixed := time.Now().UTC()
		quotaAgent := &agent.Service{Store: st, GatewaySecret: cfg.AgentGatewaySecret, Now: func() time.Time { return fixed }}
		for _, kind := range []string{"calls", "writes", "llm"} {
			t.Run(kind, func(t *testing.T) {
				original := token(exchange(authorize()), 200)
				before, e := quotaAgent.Authenticate(ctx, cfg.AgentGatewaySecret, original.AccessToken, "127.0.0.1")
				require.NoError(t, e)
				before.Limits = agent.Limits{CallsPerMinute: 100, WritesPerDay: 100, LLMOpsPerDay: 100}
				write, llm := 0, 0
				switch kind {
				case "calls":
					before.Limits.CallsPerMinute = 1
				case "writes":
					before.Limits.WritesPerDay = 1
					write = 1
				case "llm":
					before.Limits.LLMOpsPerDay = 1
					llm = 1
				}
				require.NoError(t, quotaAgent.Reserve(ctx, before, write, llm))
				rotated := token(refresh(original.RefreshToken), 200)
				after, e := quotaAgent.Authenticate(ctx, cfg.AgentGatewaySecret, rotated.AccessToken, "127.0.0.1")
				require.NoError(t, e)
				require.NotEqual(t, before.ID, after.ID)
				after.Limits = before.Limits
				require.Error(t, quotaAgent.Reserve(ctx, after, write, llm), "rotation must not reset usage")
				prior, e := quotaAgent.Introspect(ctx, before)
				require.NoError(t, e)
				current, e := quotaAgent.Introspect(ctx, after)
				require.NoError(t, e)
				require.Equal(t, prior.(map[string]any)["quota"], current.(map[string]any)["quota"])
			})
		}
	})
	t.Run("deleting an OAuth owner", func(t *testing.T) {
		var ownerID int64
		require.NoError(t, st.DB.Raw(`INSERT INTO admin_users(user_name,nick_name,password_hash) VALUES('oauth-delete-owner','Delete fixture',?) RETURNING id`, hash).Scan(&ownerID).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO admin_user_roles(user_id,role_id) SELECT ?,id FROM admin_roles WHERE role_code='R_SUPER'`, ownerID).Error)
		owner := auth.Identity{ID: ownerID, SV: 1}
		credentials := []mcpoauth.TokenResponse{}
		for _, revoked := range []bool{false, true} {
			destination, e := svc.Consent(ctx, owner, authorization, []string{"catalog:package:view"}, false, true, store.AuditActor{ID: &ownerID})
			require.NoError(t, e)
			u, e := url.Parse(destination)
			require.NoError(t, e)
			issued := token(exchange(u.Query().Get("code")), 200)
			credentials = append(credentials, issued)
			if revoked {
				require.NoError(t, svc.Revoke(ctx, client.ID, issued.RefreshToken))
			}
		}
		_, e := manager.Execute(ctx, "DeleteAdminUser", management.Input{ID: ownerID, Actor: store.AuditActor{ID: &identity.ID}})
		require.NoError(t, e)
		for _, issued := range credentials {
			require.False(t, valid(issued.AccessToken))
			token(refresh(issued.RefreshToken), 400)
		}
		var retained int64
		require.NoError(t, st.DB.Raw(`SELECT count(*) FROM mcp_oauth_grants WHERE user_id IS NULL`).Scan(&retained).Error)
		require.EqualValues(t, 2, retained)
	})
	t.Run("older active grant remains reachable", func(t *testing.T) {
		active := token(exchange(authorize()), 200)
		var activeID int64
		require.NoError(t, st.DB.Raw(`SELECT oauth_grant_id FROM agent_tokens WHERE token_hash=?`, agent.Hash(active.AccessToken)).Scan(&activeID).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO mcp_oauth_grants(client_id,user_id,session_version,agent_client_id,scopes,expires_at,revoked_at) SELECT g.client_id,g.user_id,g.session_version,g.agent_client_id,g.scopes,g.expires_at,now() FROM mcp_oauth_grants g CROSS JOIN generate_series(1,100) WHERE g.id=?`, activeID).Error)
		cursor := ""
		found := false
		seen := map[int64]bool{}
		for page := 0; page < 4; page++ {
			path := "/oauth/grants"
			if cursor != "" {
				path += "?before=" + cursor
			}
			response := call("GET", path, "", "", pair.Token, cfg.AdminOrigin)
			require.Equal(t, 200, response.Code)
			var list struct {
				Items      []struct{ ID int64 }
				NextCursor *string
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
			for _, item := range list.Items {
				require.False(t, seen[item.ID])
				seen[item.ID] = true
				if item.ID == activeID {
					found = true
				}
			}
			if list.NextCursor == nil {
				break
			}
			require.NotEqual(t, cursor, *list.NextCursor)
			cursor = *list.NextCursor
		}
		require.True(t, found, "100 revoked grants must not hide an active grant")
		response := call("DELETE", "/oauth/grants/"+strconv.FormatInt(activeID, 10), "", "", pair.Token, cfg.AdminOrigin)
		require.Equal(t, 200, response.Code)
		require.False(t, valid(active.AccessToken))
		for _, bad := range []string{"0", "-1", "bad", "1&before=2"} {
			response = call("GET", "/oauth/grants?before="+bad, "", "", pair.Token, cfg.AdminOrigin)
			require.Equal(t, 400, response.Code)
		}
	})
	// Password/session-version changes invalidate both access and refresh credentials.
	code = authorize()
	first = token(exchange(code), 200)
	require.NoError(t, st.DB.Exec(`UPDATE admin_users SET session_version=session_version+1 WHERE id=?`, identity.ID).Error)
	require.False(t, valid(first.AccessToken))
	token(refresh(first.RefreshToken), 400)
}
