package mcpoauth

import (
	"slices"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/stretchr/testify/require"
)

func TestRedirectAndScopeValidation(t *testing.T) {
	for _, raw := range []string{"https://client.example/callback", "http://127.0.0.1:4319/callback", "http://[::1]:4519/callback", "http://localhost:8080/cb"} {
		require.True(t, validRedirect(raw), raw)
	}
	for _, raw := range []string{"https://user:pass@example.com/cb", "javascript:alert(1)", "http://example.com/cb", "https://example.com/cb#fragment", "//example.com/cb"} {
		require.False(t, validRedirect(raw), raw)
	}
	_, err := scopes("dashboard:view dashboard:view", agent.Grantable)
	require.Error(t, err)
	_, err = scopes("agent:manage", agent.Grantable)
	require.Error(t, err)
	_, err = scopes("site:index:view content:article:edit", agent.Grantable)
	require.NoError(t, err)
}

// Clients registered before the SEO engine went still request its scopes for one release.
func TestRetiredScopesMapToTheIndexView(t *testing.T) {
	got, err := scopes("seo:task:handle", agent.Grantable)
	require.NoError(t, err)
	require.Equal(t, []string{"site:index:view"}, got)
	got, err = scopes("seo:insight:view catalog:package:view seo:task:handle seo:keyword:edit", []string{"seo:insight:view", "catalog:package:view"})
	require.NoError(t, err, "stored scopes may name retired scopes too")
	require.Equal(t, []string{"site:index:view", "catalog:package:view"}, got)
	_, err = scopes("seo:automation:tune seo:snippet:propose", agent.Grantable)
	require.Error(t, err, "nothing is left to grant")
	_, err = scopes("seo:task:handle seo:task:handle", agent.Grantable)
	require.Error(t, err, "a repeated scope is still refused")
	previous := append([]string{"seo:snippet:propose", "seo:automation:tune", "seo:task:handle", "seo:insight:view", "seo:keyword:edit"},
		slices.DeleteFunc(slices.Clone(agent.Grantable), func(p string) bool { return p == "site:index:view" })...)
	require.Len(t, previous, 28)
	got, err = scopes(strings.Join(previous, " "), agent.Grantable)
	require.NoError(t, err, "the limit counts scopes after mapping")
	require.ElementsMatch(t, agent.Grantable, got)
}
