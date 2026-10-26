//go:build integration

package mcpoauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/mcpoauth"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

// A client registered by the previous release asks for its whole default scope list, the five
// SEO scopes included, when it authorizes and refreshes.
func TestClientsWithRetiredScopesStillAuthorizeAndRefresh(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("oauth-isolated-test")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "oauth-admin", hash))
	var admin struct {
		ID             int64
		SessionVersion int
	}
	require.NoError(t, st.DB.Raw("SELECT id,session_version FROM admin_users WHERE user_name='oauth-admin'").Scan(&admin).Error)
	svc := &mcpoauth.Service{Store: st, Issuer: "https://admin.example.test"}
	previous := append([]string{"seo:snippet:propose", "seo:automation:tune", "seo:task:handle", "seo:insight:view", "seo:keyword:edit"},
		slices.DeleteFunc(slices.Clone(agent.Grantable), func(p string) bool { return p == "site:index:view" })...)
	require.Len(t, previous, 28)
	scope := strings.Join(previous, " ")

	registered, err := svc.Register(ctx, mcpoauth.Registration{Name: "Registered now", RedirectURIs: []string{"http://127.0.0.1:4321/callback"}, Scope: scope})
	require.NoError(t, err)
	require.ElementsMatch(t, agent.Grantable, strings.Fields(registered.Scopes))

	// Registered before the SEO engine went; Up left client registrations as they were.
	require.NoError(t, st.DB.Exec(`INSERT INTO mcp_oauth_clients(id,name,redirect_uris,scopes) VALUES('previous-client','Previous release','["http://127.0.0.1:4321/callback"]',?)`, scope).Error)
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	in := mcpoauth.Authorization{ClientID: "previous-client", RedirectURI: "http://127.0.0.1:4321/callback", ResponseType: "code", Scope: scope, State: "opaque-state",
		Challenge: base64.RawURLEncoding.EncodeToString(digest[:]), ChallengeMethod: "S256", Resource: svc.Resource()}
	_, requested, err := svc.Validate(ctx, in)
	require.NoError(t, err)
	require.ElementsMatch(t, agent.Grantable, requested)
	redirect, err := svc.Consent(ctx, auth.Identity{ID: admin.ID, SV: admin.SessionVersion}, in, previous, false, true, store.AuditActor{})
	require.NoError(t, err)
	u, err := url.Parse(redirect)
	require.NoError(t, err)
	require.NotEmpty(t, u.Query().Get("code"), redirect)

	first, err := svc.Exchange(ctx, mcpoauth.TokenRequest{GrantType: "authorization_code", ClientID: "previous-client", Code: u.Query().Get("code"), RedirectURI: in.RedirectURI, Verifier: verifier, Resource: svc.Resource()})
	require.NoError(t, err)
	require.ElementsMatch(t, agent.Grantable, strings.Fields(first.Scope))
	next, err := svc.Exchange(ctx, mcpoauth.TokenRequest{GrantType: "refresh_token", ClientID: "previous-client", RefreshToken: first.RefreshToken, Resource: svc.Resource(), Scope: scope})
	require.NoError(t, err)
	require.ElementsMatch(t, agent.Grantable, strings.Fields(next.Scope))
	var stored string
	require.NoError(t, st.DB.Raw("SELECT g.scopes FROM mcp_oauth_grants g JOIN mcp_oauth_refresh_tokens r ON r.grant_id=g.id WHERE r.token_hash=?", agent.Hash(next.RefreshToken)).Scan(&stored).Error)
	require.ElementsMatch(t, agent.Grantable, strings.Fields(stored))

	secret := strings.Repeat("g", 32)
	identity, err := (&agent.Service{Store: st, GatewaySecret: secret}).Authenticate(ctx, secret, next.AccessToken, "127.0.0.1")
	require.NoError(t, err)
	require.Contains(t, identity.Permissions, "site:index:view")
}
