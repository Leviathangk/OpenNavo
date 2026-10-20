package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitHubCredentialEncryptionAndPurposeIsolation(t *testing.T) {
	cfg := Config{JWTSecret: strings.Repeat("a", 32)}
	first, err := cfg.EncryptGitHubToken("unit-github-credential")
	require.NoError(t, err)
	second, err := cfg.EncryptGitHubToken("unit-github-credential")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NotContains(t, first, "unit-github-credential")
	plain, err := cfg.DecryptGitHubToken(first)
	require.NoError(t, err)
	require.Equal(t, "unit-github-credential", plain)
	for _, value := range []string{"invalid", "", first[:len(first)-4] + "AAAA"} {
		plain, err = cfg.DecryptGitHubToken(value)
		require.Error(t, err)
		require.Empty(t, plain)
		require.NotContains(t, err.Error(), "unit-github-credential")
	}
	_, err = (Config{JWTSecret: strings.Repeat("b", 32)}).DecryptGitHubToken(first)
	require.Error(t, err)
	_, err = cfg.DecryptSEOSecret("test", first)
	require.Error(t, err)
	translation, err := cfg.EncryptSEOSecret("test", "unit-seo-credential")
	require.NoError(t, err)
	_, err = cfg.DecryptGitHubToken(translation)
	require.Error(t, err)
	dedicated := Config{JWTSecret: cfg.JWTSecret, GitHubSettingsEncryptionKey: strings.Repeat("c", 32)}
	encrypted, err := dedicated.EncryptGitHubToken("dedicated-github-credential")
	require.NoError(t, err)
	dedicated.JWTSecret = strings.Repeat("d", 32)
	plain, err = dedicated.DecryptGitHubToken(encrypted)
	require.NoError(t, err)
	require.Equal(t, "dedicated-github-credential", plain)
	_, err = (Config{}).EncryptGitHubToken("unit-github-credential")
	require.Error(t, err)
}
