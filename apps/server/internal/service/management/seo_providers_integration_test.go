//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/gen/adminapi"
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

func TestSEOProviderServiceAccountViewAndCredentialReplacement(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st, Config: config.Config{JWTSecret: strings.Repeat("fixture-", 8), WebBaseURL: "https://fixture.example"}}
	ctx := domain.WithOperation(context.Background(), &domain.Operation{Permissions: []string{"system:seo:edit"}})
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	call := func(source, operation string, body map[string]any) map[string]any {
		t.Helper()
		result, err := svc.Execute(ctx, operation, Input{Key: source, Body: body})
		require.NoError(t, err)
		view := result.(map[string]any)
		require.NoError(t, spec.Components.Schemas["SeoProviderView"].Value.VisitJSON(view))
		raw, err := json.Marshal(view)
		require.NoError(t, err)
		for _, secret := range []string{"PRIVATE KEY", "client_email", "reader@", "fixture-secret", "fixture-api-key", "origin.fixture"} {
			require.NotContains(t, string(raw), secret)
		}
		return view
	}
	initial := call("gsc", "GetSeoProvider", nil)
	require.Nil(t, initial["credentialType"])
	require.Len(t, initial["watermarks"], 3)
	require.Nil(t, initial["watermarks"].([]any)[0].(map[string]any)["completeThrough"])
	c := serviceAccountCredentials(t)
	c.ServiceAccountJSON += strings.Repeat(" ", 16384-len(c.ServiceAccountJSON))
	configured := call("gsc", "UpdateSeoProvider", map[string]any{"enabled": true, "credentials": map[string]any{"property": c.Property, "serviceAccountJson": c.ServiceAccountJSON}})
	require.Equal(t, "service_account", configured["credentialType"])
	require.Equal(t, true, configured["configured"])
	var cipher string
	require.NoError(t, st.DB.Raw("SELECT credentials_ciphertext FROM seo_provider_settings WHERE source='gsc'").Scan(&cipher).Error)
	require.NotContains(t, cipher, "PRIVATE KEY")
	decoded, err := svc.Config.DecryptSEOSecret("provider:gsc", cipher)
	require.NoError(t, err)
	require.Contains(t, decoded, "private_key")
	require.NoError(t, st.DB.Exec("UPDATE seo_data_watermarks SET complete_through='2026-10-20',fresh_through='2026-10-22' WHERE source='gsc_page'").Error)
	view := call("gsc", "GetSeoProvider", nil)
	progress := view["watermarks"].([]any)
	for i, source := range []string{"gsc_page", "gsc_query", "inspection"} {
		require.Equal(t, source, progress[i].(map[string]any)["source"])
	}
	require.Equal(t, "2026-10-20", progress[0].(map[string]any)["completeThrough"])
	require.Equal(t, "service_account", call("gsc", "UpdateSeoProvider", map[string]any{"enabled": false})["credentialType"])
	_, err = svc.Execute(ctx, "UpdateSeoProvider", Input{Key: "gsc", Body: map[string]any{"credentials": map[string]any{"property": c.Property, "serviceAccountJson": c.ServiceAccountJSON, "clientId": "mixed"}}})
	require.Error(t, err)
	require.Equal(t, "service_account", call("gsc", "GetSeoProvider", nil)["credentialType"])
	require.Equal(t, "oauth", call("gsc", "UpdateSeoProvider", map[string]any{"credentials": map[string]any{"property": c.Property, "clientId": "fixture-client", "clientSecret": "fixture-secret", "refreshToken": "fixture-refresh"}})["credentialType"])
	cleared := call("gsc", "UpdateSeoProvider", map[string]any{"enabled": false, "clearCredentials": true})
	require.Nil(t, cleared["credentialType"])
	require.Equal(t, false, cleared["configured"])
	require.Equal(t, "2026-10-20", cleared["watermarks"].([]any)[0].(map[string]any)["completeThrough"])
	for _, tc := range []struct {
		source, kind string
		credentials  map[string]any
		count        int
	}{
		{"bing", "api_key", map[string]any{"property": "https://fixture.example/", "apiKey": "fixture-api-key"}, 1},
		{"audit", "origin", map[string]any{"originUrl": "https://origin.fixture.example/"}, 2},
	} {
		result := call(tc.source, "UpdateSeoProvider", map[string]any{"credentials": tc.credentials})
		require.Equal(t, tc.kind, result["credentialType"])
		require.Len(t, result["watermarks"], tc.count)
	}
	require.NoError(t, st.DB.Exec("DELETE FROM seo_data_watermarks WHERE source='edge_probe'").Error)
	require.Len(t, call("audit", "GetSeoProvider", nil)["watermarks"], 1)
	var audits string
	require.NoError(t, st.DB.Raw("SELECT coalesce(string_agg(before::text||after::text,''),'') FROM audit_logs WHERE entity_type='seo_provider_settings'").Scan(&audits).Error)
	require.NotContains(t, audits, "private_key")
	require.NotContains(t, audits, "fixture-secret")
}
