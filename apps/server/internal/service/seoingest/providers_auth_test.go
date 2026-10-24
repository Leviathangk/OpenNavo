package seoingest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func accountFixture(t *testing.T) (*rsa.PrivateKey, map[string]string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return key, map[string]string{"type": "service_account", "client_email": "reader@fixture-project.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "private_key_id": "fixture-key-id"}
}

func accountJSON(t *testing.T, account map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(account)
	require.NoError(t, err)
	return string(raw)
}

func testGoogleClient(server *httptest.Server) *http.Client {
	client := server.Client()
	transport := client.Transport
	target, _ := url.Parse(server.URL)
	client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		request := r.Clone(r.Context())
		copyURL := *r.URL
		request.URL = &copyURL
		request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
		return transport.RoundTrip(request)
	})
	client.CheckRedirect = apiClient().CheckRedirect
	return client
}

func TestGoogleServiceAccountJWTAndBothAPIs(t *testing.T) {
	key, account := accountFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, endpoint := range []string{"", googleTokenURL, "https://accounts.google.com/o/oauth2/token"} {
		t.Run(endpoint, func(t *testing.T) {
			account["token_uri"] = endpoint
			audience := endpoint
			if audience == "" {
				audience = googleTokenURL
			}
			calls := []string{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				require.Equal(t, http.MethodPost, r.Method)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/token", "/o/oauth2/token":
					require.NoError(t, r.ParseForm())
					require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", r.Form.Get("grant_type"))
					require.Empty(t, r.Form.Get("refresh_token"))
					token, err := jwt.Parse(r.Form.Get("assertion"), func(token *jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(account["client_email"]), jwt.WithAudience(audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }))
					require.NoError(t, err)
					require.Equal(t, "fixture-key-id", token.Header["kid"])
					claims := token.Claims.(jwt.MapClaims)
					require.Equal(t, webmasterScope, claims["scope"])
					require.EqualValues(t, now.Unix(), claims["iat"])
					require.EqualValues(t, now.Add(time.Hour).Unix(), claims["exp"])
					require.NotContains(t, claims, "sub")
					_, _ = w.Write([]byte(`{"access_token":"fixture-access-token"}`))
				case "/webmasters/v3/sites/sc-domain:fixture.example/searchAnalytics/query":
					require.Equal(t, "Bearer fixture-access-token", r.Header.Get("Authorization"))
					var query Query
					require.NoError(t, json.NewDecoder(r.Body).Decode(&query))
					require.Equal(t, "2026-10-20", query.StartDate)
					_, _ = w.Write([]byte(`{"rows":[{"keys":["fixture mac tools"],"clicks":1,"impressions":5,"position":2}]}`))
				case "/v1/urlInspection/index:inspect":
					require.Equal(t, "Bearer fixture-access-token", r.Header.Get("Authorization"))
					var body map[string]string
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, "sc-domain:fixture.example", body["siteUrl"])
					require.Equal(t, "https://fixture.example/apps/example", body["inspectionUrl"])
					_, _ = w.Write([]byte(`{"inspectionResult":{"indexStatusResult":{"coverageState":"Submitted and indexed"}}}`))
				default:
					t.Errorf("unexpected request path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			provider, err := googleProvider(context.Background(), Credentials{Property: "sc-domain:fixture.example", ServiceAccountJSON: accountJSON(t, account)}, testGoogleClient(server), now)
			require.NoError(t, err)
			analytics, err := provider.Analytics(context.Background(), Query{StartDate: "2026-10-20"})
			require.NoError(t, err)
			require.Len(t, analytics.Rows, 1)
			inspection, err := provider.Inspect(context.Background(), "https://fixture.example/apps/example")
			require.NoError(t, err)
			require.Equal(t, "Submitted and indexed", inspection.CoverageState)
			require.Len(t, calls, 3)
		})
	}
}

func TestServiceAccountValidationAndCredentialIsolation(t *testing.T) {
	_, valid := accountFixture(t)
	require.True(t, ValidServiceAccountJSON(accountJSON(t, valid)))
	for _, tc := range []struct{ field, value string }{
		{"type", "authorized_user"}, {"client_email", ""}, {"client_email", "Display <reader@fixture.example>"},
		{"private_key", "not-a-key"}, {"token_uri", "http://127.0.0.1/token"},
		{"token_uri", "https://unrelated.example/token"}, {"token_uri", googleTokenURL + "?redirect=fixture"},
	} {
		t.Run(tc.field+tc.value, func(t *testing.T) {
			copy := map[string]string{}
			for k, v := range valid {
				copy[k] = v
			}
			copy[tc.field] = tc.value
			require.False(t, ValidServiceAccountJSON(accountJSON(t, copy)))
		})
	}
	require.False(t, ValidServiceAccountJSON("null"))
	require.False(t, ValidServiceAccountJSON(accountJSON(t, valid)+strings.Repeat(" ", 16384)))
	provider, err := googleProvider(context.Background(), Credentials{Property: "sc-domain:fixture.example", ServiceAccountJSON: accountJSON(t, valid), ClientID: "mixed-oauth"}, nil, time.Now())
	require.Nil(t, provider)
	require.ErrorIs(t, err, ErrUpstream)
}

func TestGoogleOAuthAndRedactedTokenErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"oauth", 200, `{"access_token":"fixture-access-token"}`, true},
		{"unauthorized", 401, `{"error":"fixture-secret-must-not-leak"}`, false},
		{"malformed", 200, "fixture-secret-must-not-leak", false},
		{"missing_token", 200, `{}`, false},
		{"redirect", 302, `fixture-secret-must-not-leak`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
				require.Equal(t, "fixture-refresh", r.Form.Get("refresh_token"))
				require.Empty(t, r.Form.Get("assertion"))
				w.Header().Set("Location", "https://unrelated.example/redirect")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, err := googleProvider(context.Background(), Credentials{Property: "sc-domain:fixture.example", ClientID: "fixture-client", ClientSecret: "fixture-secret", RefreshToken: "fixture-refresh"}, testGoogleClient(server), time.Now())
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Equal(t, ErrUpstream, err)
			}
		})
	}
}
