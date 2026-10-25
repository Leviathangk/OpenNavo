package mcpoauth

import (
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
	// Client registration advertises the union, while actual token grants enforce separation.
	_, err = scopes("seo:automation:tune content:article:edit", agent.Grantable)
	require.NoError(t, err)
}
