package config

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	IndexNowDailyLimit          int           `env:"INDEXNOW_DAILY_LIMIT" envDefault:"5000"`
	UsageCountryTrustedProxies  []string      `env:"USAGE_COUNTRY_TRUSTED_PROXIES" envSeparator:","`
	AgentGatewaySecret          string        `env:"AGENT_GATEWAY_SECRET"`
	AppEnv                      string        `env:"APP_ENV,required"`
	HTTPAddr                    string        `env:"HTTP_ADDR" envDefault:":8080"`
	MetricsAddr                 string        `env:"METRICS_ADDR" envDefault:":9090"`
	TrustedProxies              []string      `env:"TRUSTED_PROXIES" envSeparator:","`
	PublicBaseURL               string        `env:"PUBLIC_BASE_URL,required"`
	GEOObservationBaseURL       string        `env:"GEO_OBSERVATION_BASE_URL"`
	WebBaseURL                  string        `env:"WEB_BASE_URL,required"`
	AdminOrigin                 string        `env:"ADMIN_ORIGIN,required"`
	PublicCORSOrigins           []string      `env:"PUBLIC_CORS_ORIGINS" envSeparator:","`
	DatabaseURL                 string        `env:"DATABASE_URL,required"`
	DBMaxOpenConns              int           `env:"DB_MAX_OPEN_CONNS" envDefault:"30"`
	RedisURL                    string        `env:"REDIS_URL,required"`
	CacheRedisURL               string        `env:"CACHE_REDIS_URL"`
	S3Endpoint                  string        `env:"S3_ENDPOINT,required"`
	S3Region                    string        `env:"S3_REGION,required"`
	S3Bucket                    string        `env:"S3_BUCKET,required"`
	S3AccessKey                 string        `env:"S3_ACCESS_KEY,required"`
	S3SecretKey                 string        `env:"S3_SECRET_KEY,required"`
	S3UseSSL                    bool          `env:"S3_USE_SSL" envDefault:"true"`
	CDNBaseURL                  string        `env:"CDN_BASE_URL,required"`
	JWTSecret                   string        `env:"JWT_SECRET,required"`
	JWTAccessTTL                time.Duration `env:"JWT_ACCESS_TTL" envDefault:"2h"`
	JWTRefreshTTL               time.Duration `env:"JWT_REFRESH_TTL" envDefault:"168h"`
	GitHubSettingsEncryptionKey string        `env:"GITHUB_SETTINGS_ENCRYPTION_KEY"`
	SEOSettingsEncryptionKey    string        `env:"SEO_SETTINGS_ENCRYPTION_KEY"`
	WebInternalURL              string        `env:"WEB_INTERNAL_URL"`
	WebInternalSecret           string        `env:"WEB_INTERNAL_SECRET"`
	CatalogKinds                string        `env:"CATALOG_KINDS" envDefault:"cask"`
	HomebrewAPIBase             string        `env:"HOMEBREW_API_BASE" envDefault:"https://formulae.brew.sh/api"`
	FetchUserAgent              string        `env:"FETCH_USER_AGENT" envDefault:"OpenNavoBot/1.0 (+https://opennavo.example/bot)"`
	CIReleaseToken              string        `env:"CI_RELEASE_TOKEN"`
	AdminBootstrapUsername      string        `env:"ADMIN_BOOTSTRAP_USERNAME"`
	AdminBootstrapPassword      string        `env:"ADMIN_BOOTSTRAP_PASSWORD"`
	E2EAdminPassword            string        `env:"E2E_ADMIN_PASSWORD" envDefault:"opennavo-e2e-dev-only"`
	LogLevel                    string        `env:"LOG_LEVEL" envDefault:"info"`
	OTELExporterOTLPEndpoint    string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	TZSchedule                  string        `env:"TZ_SCHEDULE" envDefault:"Asia/Shanghai"`
	// Explicit Traditional Chinese backfill after a converter upgrade: objects derived again per
	// second, and derived rows written per minute.
	TraditionalObjectsPerSecond float64 `env:"TRADITIONAL_BACKFILL_OBJECTS_PER_SECOND" envDefault:"20"`
	TraditionalWritesPerMinute  float64 `env:"TRADITIONAL_BACKFILL_WRITES_PER_MINUTE" envDefault:"30"`
}

// Load reads local secrets internally; existing environment variables take precedence and errors contain no values.
func Load() (Config, error) {
	for _, path := range []string{".env.local", filepath.Join("server", ".env.local")} {
		if err := LoadEnvFile(path); err != nil {
			return Config{}, err
		}
	}
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, errors.New("missing or invalid environment variables")
	}

	return cfg, cfg.Validate()
}

func LoadEnvFile(path string) error {
	file, err := os.Open(path) // #nosec G304 -- Only startup code and tests supply local configuration paths; network input is not accepted.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot open local environment file")
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" || strings.ContainsAny(name, " \t\r\n") {
			return errors.New("invalid local environment file syntax")
		}
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if _, exists := os.LookupEnv(name); !exists {
			if err := os.Setenv(name, value); err != nil {
				return errors.New("cannot set local environment variable")
			}
		}
	}
	if scanner.Err() != nil {
		return errors.New("cannot read local environment file")
	}
	return nil
}

// publicExampleSecrets contains development examples from apps/server/.env.example, visible to everyone in the public repository.
// Reject them outside dev: they offer no secrecy and allow forged admin tokens or bootstrap administrator logins.
var publicExampleSecrets = []string{
	"opennavo-local-development-only-placeholder",
	"opennavo-dev",
	"opennavo-dev-only",
	"local-development-only",
}

func (c Config) Validate() error {
	if c.CatalogKinds != "" && c.CatalogKinds != "cask" {
		return errors.New("CATALOG_KINDS must be cask")
	}
	if c.AppEnv != "dev" && c.AppEnv != "staging" && c.AppEnv != "prod" {
		return errors.New("invalid APP_ENV")
	}
	for _, origin := range c.PublicCORSOrigins {
		if strings.TrimSpace(origin) != "" && corsOrigin(origin) == "" {
			return errors.New("invalid PUBLIC_CORS_ORIGINS")
		}
	}
	for name, value := range map[string]string{"PUBLIC_BASE_URL": c.PublicBaseURL, "WEB_BASE_URL": c.WebBaseURL, "ADMIN_ORIGIN": c.AdminOrigin, "CDN_BASE_URL": c.CDNBaseURL, "HOMEBREW_API_BASE": c.HomebrewAPIBase} {
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return fmt.Errorf("invalid %s", name)
		}
	}
	for name, value := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL, "CACHE_REDIS_URL": c.CacheRedisURL} {
		if name == "CACHE_REDIS_URL" && value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" {
			return fmt.Errorf("invalid %s", name)
		}
		if name == "DATABASE_URL" && u.Scheme != "postgres" && u.Scheme != "postgresql" {
			return errors.New("invalid DATABASE_URL scheme")
		}
		if (name == "REDIS_URL" || name == "CACHE_REDIS_URL") && u.Scheme != "redis" && u.Scheme != "rediss" {
			return fmt.Errorf("invalid %s scheme", name)
		}
	}
	if c.GitHubSettingsEncryptionKey != "" && len(c.GitHubSettingsEncryptionKey) < 32 {
		return errors.New("GITHUB_SETTINGS_ENCRYPTION_KEY must contain at least 32 bytes")
	}
	if c.SEOSettingsEncryptionKey != "" && len(c.SEOSettingsEncryptionKey) < 32 {
		return errors.New("SEO_SETTINGS_ENCRYPTION_KEY must contain at least 32 bytes")
	}
	// The URL change feed (WP4) refreshes page caches only with a secret; without one it is off.
	if c.WebInternalSecret != "" && len(c.WebInternalSecret) < 32 {
		return errors.New("WEB_INTERNAL_SECRET must contain at least 32 bytes")
	}
	if len(c.JWTSecret) < 32 {
		return errors.New("JWT_SECRET must contain at least 32 bytes")
	}
	if c.AppEnv != "dev" {
		databasePassword := ""
		if u, err := url.Parse(c.DatabaseURL); err == nil && u.User != nil {
			databasePassword, _ = u.User.Password()
		}
		for name, value := range map[string]string{"JWT_SECRET": c.JWTSecret, "S3_ACCESS_KEY": c.S3AccessKey, "S3_SECRET_KEY": c.S3SecretKey, "ADMIN_BOOTSTRAP_PASSWORD": c.AdminBootstrapPassword, "DATABASE_URL": databasePassword} {
			if value != "" && slices.Contains(publicExampleSecrets, value) {
				return fmt.Errorf("%s must not use the public example value outside dev", name)
			}
		}
	}
	if c.AppEnv == "prod" && c.CIReleaseToken == "" {
		return errors.New("CI_RELEASE_TOKEN is required in prod")
	}
	if c.DBMaxOpenConns < 1 || c.JWTAccessTTL <= 0 || c.JWTRefreshTTL <= c.JWTAccessTTL {
		return errors.New("invalid connection, duration or budget limits")
	}
	if _, err := time.LoadLocation(c.TZSchedule); err != nil {
		return errors.New("invalid TZ_SCHEDULE")
	}
	for _, rate := range []float64{c.TraditionalObjectsPerSecond, c.TraditionalWritesPerMinute} {
		if !(rate > 0) || math.IsInf(rate, 1) {
			return errors.New("TRADITIONAL_BACKFILL limits must be positive numbers")
		}
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("invalid LOG_LEVEL")
	}
	return nil
}

// PublicOrigins merges extra environment origins, site and client origins; development ports are allowed automatically only in dev.
func (c Config) PublicOrigins() []string {
	web, _ := url.Parse(c.WebBaseURL)
	configured := []string{"tauri://localhost", "http://tauri.localhost", "https://tauri.localhost"}
	if web != nil && web.Host != "" {
		configured = append(configured, web.Scheme+"://"+web.Host)
	}
	configured = append(configured, c.PublicCORSOrigins...)
	if c.AppEnv == "dev" {
		configured = append(configured, "http://localhost:3000", "http://localhost:1420")
	}
	result, seen := []string{}, map[string]bool{}
	for _, raw := range configured {
		origin := corsOrigin(raw)
		if origin != "" && !seen[origin] {
			result, seen[origin] = append(result, origin), true
		}
	}
	return result
}
func corsOrigin(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Host == "" || strings.Contains(u.Host, "*") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" && (u.Scheme != "tauri" || u.Host != "localhost") {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// AdminOrigins allows both loopback hostnames only for local admin development; production origins still match strictly.
func (c Config) AdminOrigins() []string {
	result := []string{c.AdminOrigin}
	u, err := url.Parse(c.AdminOrigin)
	if err == nil && c.AppEnv == "dev" && u.Scheme == "http" && u.Port() == "9527" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost") {
		return []string{"http://127.0.0.1:9527", "http://localhost:9527"}
	}
	return result
}

// AdminAPIBaseURL is where clients outside the admin UI reach /admin-api: development serves it from the API itself,
// while deployments route it only through the admin origin.
func (c Config) AdminAPIBaseURL() string {
	base := c.AdminOrigin
	if c.AppEnv == "dev" {
		base = c.PublicBaseURL
	}
	return strings.TrimRight(base, "/") + "/admin-api"
}
