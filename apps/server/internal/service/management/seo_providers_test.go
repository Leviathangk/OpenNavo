package management

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/seoingest"
	"github.com/stretchr/testify/require"
)

func serviceAccountCredentials(t *testing.T) seoingest.Credentials {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]string{"type": "service_account", "client_email": "reader@fixture-project.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "project_id": "fixture-project"})
	require.NoError(t, err)
	return seoingest.Credentials{Property: "sc-domain:fixture.example", ServiceAccountJSON: string(raw)}
}

func TestProviderCredentialModesAndBounds(t *testing.T) {
	valid := serviceAccountCredentials(t)
	for _, tc := range []struct {
		name, source string
		edit         func(*seoingest.Credentials)
		want         bool
	}{
		{"service_account", "gsc", func(*seoingest.Credentials) {}, true},
		{"url_property", "gsc", func(c *seoingest.Credentials) { c.Property = "https://fixture.example/" }, true},
		{"oauth", "gsc", func(c *seoingest.Credentials) {
			c.ServiceAccountJSON = ""
			c.ClientID = "client"
			c.ClientSecret = "secret"
			c.RefreshToken = "refresh"
		}, true},
		{"mixed", "gsc", func(c *seoingest.Credentials) { c.ClientID = "client" }, false},
		{"bad_property", "gsc", func(c *seoingest.Credentials) { c.Property = "http://127.0.0.1/" }, false},
		{"too_long", "gsc", func(c *seoingest.Credentials) { c.ServiceAccountJSON += strings.Repeat(" ", 16384) }, false},
		{"malformed", "gsc", func(c *seoingest.Credentials) { c.ServiceAccountJSON = "{}" }, false},
		{"bing_rejects_service_account", "bing", func(c *seoingest.Credentials) { c.Property = "https://fixture.example/"; c.APIKey = "key" }, false},
		{"audit_rejects_service_account", "audit", func(c *seoingest.Credentials) { c.Property = ""; c.OriginURL = "https://origin.fixture.example/" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.edit(&c)
			require.Equal(t, tc.want, validProviderCredentials(tc.source, c, "https://fixture.example"))
		})
	}
}
