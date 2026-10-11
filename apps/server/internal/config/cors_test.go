package config

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicOriginsReadFromEnvironmentAndScopedToDevelopment(t *testing.T) {
	// A temporary working directory prevents configuration tests from reading the developer's local secrets.
	t.Chdir(t.TempDir())
	base := validConfig()
	for name, value := range map[string]string{"APP_ENV": "dev", "PUBLIC_BASE_URL": "https://api.opennavo.example", "WEB_BASE_URL": "https://opennavo.example", "ADMIN_ORIGIN": "https://admin.opennavo.example", "PUBLIC_CORS_ORIGINS": "https://preview.opennavo.example, https://opennavo.example/", "DATABASE_URL": base.DatabaseURL, "REDIS_URL": base.RedisURL, "S3_ENDPOINT": "localhost:9000", "S3_REGION": "us-east-1", "S3_BUCKET": "opennavo", "S3_ACCESS_KEY": base.JWTSecret[:16], "S3_SECRET_KEY": base.JWTSecret, "CDN_BASE_URL": "https://cdn.opennavo.example", "JWT_SECRET": base.JWTSecret, "LLM_ENABLED": "false"} {
		t.Setenv(name, value)
	}
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, []string{"https://preview.opennavo.example", " https://opennavo.example/"}, cfg.PublicCORSOrigins)
	require.Len(t, cfg.PublicOrigins(), 7)
	for _, environment := range []string{"dev", "staging", "prod"} {
		cfg.AppEnv = environment
		for _, origin := range []string{"https://opennavo.example", "https://preview.opennavo.example", "tauri://localhost", "http://tauri.localhost", "https://tauri.localhost"} {
			require.Contains(t, cfg.PublicOrigins(), origin)
		}
		for _, origin := range []string{"http://localhost:3000", "http://localhost:1420"} {
			require.Equal(t, environment == "dev", slices.Contains(cfg.PublicOrigins(), origin))
		}
	}
	for _, value := range []string{"*", "https://*.example", "null", "https://user:password@example.test", "https://example.test/path", "https://example.test?query=1", "https://example.test#fragment", "file://localhost", "tauri://foreign"} {
		c := validConfig()
		c.PublicCORSOrigins = []string{value}
		require.ErrorContains(t, c.Validate(), "PUBLIC_CORS_ORIGINS")
	}
}

func TestAdminLoopbackAliasIsOnlyAvailableForDevelopment(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1"} {
		for _, environment := range []string{"dev", "staging", "prod"} {
			cfg := Config{AppEnv: environment, AdminOrigin: "http://" + host + ":9527"}
			if environment == "dev" {
				require.Equal(t, []string{"http://127.0.0.1:9527", "http://localhost:9527"}, cfg.AdminOrigins())
			} else {
				require.Equal(t, []string{cfg.AdminOrigin}, cfg.AdminOrigins())
			}
		}
	}
	cfg := Config{AppEnv: "dev", AdminOrigin: "https://admin.opennavo.example"}
	require.Equal(t, []string{cfg.AdminOrigin}, cfg.AdminOrigins())
}

func TestAdminAPIBaseURLFollowsDeploymentRouting(t *testing.T) {
	// Deployed proxies expose /admin-api only on the admin origin; development serves it from the API process.
	for environment, expected := range map[string]string{"dev": "http://localhost:8080/admin-api", "staging": "https://admin.opennavo.example/admin-api", "prod": "https://admin.opennavo.example/admin-api"} {
		cfg := Config{AppEnv: environment, PublicBaseURL: "http://localhost:8080/", AdminOrigin: "https://admin.opennavo.example/"}
		require.Equal(t, expected, cfg.AdminAPIBaseURL(), environment)
	}
}
