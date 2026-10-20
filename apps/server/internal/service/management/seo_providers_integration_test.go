//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOProviderCredentialsEncryptedAndNeverReturned(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st, Config: config.Config{JWTSecret: strings.Repeat("fixture-", 8)}}
	ctx := domain.WithOperation(context.Background(), &domain.Operation{Permissions: []string{"system:seo:edit"}})
	value, err := svc.Execute(ctx, "UpdateSeoProvider", Input{Key: "gsc", Body: map[string]any{"enabled": true, "credentials": map[string]any{"property": "sc-domain:fixture.example", "clientId": "fixture-client", "clientSecret": "fixture-secret-not-a-real-key", "refreshToken": "fixture-refresh-not-a-real-token"}}})
	require.NoError(t, err)
	raw, _ := json.Marshal(value)
	require.NotContains(t, string(raw), "fixture-secret")
	require.Contains(t, string(raw), `"configured":true`)
	var cipher string
	require.NoError(t, st.DB.Raw("SELECT credentials_ciphertext FROM seo_provider_settings WHERE source='gsc'").Scan(&cipher).Error)
	require.NotContains(t, cipher, "fixture-secret")
	plain, err := svc.Config.DecryptSEOSecret("provider:gsc", cipher)
	require.NoError(t, err)
	require.Contains(t, plain, "fixture-secret")
	_, err = svc.Config.DecryptSEOSecret("provider:bing", cipher)
	require.Error(t, err)
	_, err = svc.Execute(ctx, "UpdateSeoProvider", Input{Key: "gsc", Body: map[string]any{"clearCredentials": true}})
	require.Error(t, err)
	_, err = svc.Execute(domain.WithOperation(context.Background(), &domain.Operation{AgentClientID: 1, Permissions: []string{"system:seo:edit"}}), "GetSeoProvider", Input{Key: "gsc"})
	require.Error(t, err)
}
