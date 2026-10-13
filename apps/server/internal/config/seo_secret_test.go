package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSEOCredentialEncryptionAndFieldIsolation(t *testing.T) {
	cfg := Config{JWTSecret: strings.Repeat("a", 32)}
	first, err := cfg.EncryptSEOSecret("indexnow", "unit-indexnow-key")
	require.NoError(t, err)
	second, err := cfg.EncryptSEOSecret("indexnow", "unit-indexnow-key")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NotContains(t, first, "unit-indexnow-key")
	plain, err := cfg.DecryptSEOSecret("indexnow", first)
	require.NoError(t, err)
	require.Equal(t, "unit-indexnow-key", plain)

	// A ciphertext cannot be read as another field or as a GitHub/translation credential.
	_, err = cfg.DecryptSEOSecret("cloudflare", first)
	require.Error(t, err)
	_, err = cfg.DecryptGitHubToken(first)
	require.Error(t, err)
	github, err := cfg.EncryptGitHubToken("unit-github-credential")
	require.NoError(t, err)
	_, err = cfg.DecryptSEOSecret("indexnow", github)
	require.Error(t, err)

	for _, value := range []string{"invalid", "", first[:len(first)-4] + "AAAA"} {
		plain, err = cfg.DecryptSEOSecret("indexnow", value)
		require.Error(t, err)
		require.Empty(t, plain)
		require.NotContains(t, err.Error(), "unit-indexnow-key")
	}
	_, err = (Config{JWTSecret: strings.Repeat("b", 32)}).DecryptSEOSecret("indexnow", first)
	require.Error(t, err)

	// A dedicated key survives JWT secret rotation.
	dedicated := Config{JWTSecret: cfg.JWTSecret, SEOSettingsEncryptionKey: strings.Repeat("c", 32)}
	encrypted, err := dedicated.EncryptSEOSecret("cloudflare", "dedicated-cloudflare-token")
	require.NoError(t, err)
	dedicated.JWTSecret = strings.Repeat("d", 32)
	plain, err = dedicated.DecryptSEOSecret("cloudflare", encrypted)
	require.NoError(t, err)
	require.Equal(t, "dedicated-cloudflare-token", plain)

	_, err = (Config{JWTSecret: "short"}).EncryptSEOSecret("indexnow", "x")
	require.Error(t, err)
}
