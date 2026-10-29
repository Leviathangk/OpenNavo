package config

import (
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{AppEnv: "dev", PublicBaseURL: "http://localhost:8080", WebBaseURL: "http://localhost:3000", AdminOrigin: "http://localhost:9527", DatabaseURL: "postgres://localhost:55432/opennavo", RedisURL: "redis://localhost:56379", CDNBaseURL: "http://localhost:9000/opennavo", HomebrewAPIBase: "https://formulae.brew.sh/api", JWTSecret: strings.Repeat("x", 32), DBMaxOpenConns: 30, JWTAccessTTL: 2 * time.Hour, JWTRefreshTTL: 168 * time.Hour, TZSchedule: "Asia/Shanghai", LogLevel: "info", TraditionalObjectsPerSecond: 20, TraditionalWritesPerMinute: 30}
}
func TestConfigurationValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"CATALOG_KINDS", func(c *Config) { c.CatalogKinds = "cask,formula" }},
		{"APP_ENV", func(c *Config) { c.AppEnv = "other" }}, {"DATABASE_URL", func(c *Config) { c.DatabaseURL = "mysql://localhost/db" }},
		{"JWT_SECRET", func(c *Config) { c.JWTSecret = "private-value" }},
		{"GITHUB_SETTINGS_ENCRYPTION_KEY", func(c *Config) { c.GitHubSettingsEncryptionKey = "short" }},
		{"SEO_SETTINGS_ENCRYPTION_KEY", func(c *Config) { c.SEOSettingsEncryptionKey = "short" }},
		{"WEB_INTERNAL_SECRET", func(c *Config) { c.WebInternalSecret = "short" }},
		{"negative probe interval", func(c *Config) { c.IndexNowProbeInterval = -time.Second }},
		{"excessive probe interval", func(c *Config) { c.IndexNowProbeInterval = 6 * time.Second }},
		{"prod", func(c *Config) { c.AppEnv = "prod" }},
		{"duration", func(c *Config) { c.JWTAccessTTL = 0 }}, {"url", func(c *Config) { c.CDNBaseURL = "file:///tmp" }},
		{"timezone", func(c *Config) { c.TZSchedule = "bad-zone" }}, {"log", func(c *Config) { c.LogLevel = "verbose" }},
		{"traditional objects", func(c *Config) { c.TraditionalObjectsPerSecond = 0 }},
		{"traditional writes", func(c *Config) { c.TraditionalWritesPerMinute = math.NaN() }},
	}
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			c := validConfig()
			test.change(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatal("error exposes a value")
			}
		})
	}
}
func TestProductionAllowsGitHubPATToBeConfiguredAfterStartup(t *testing.T) {
	cfg := validConfig()
	cfg.AppEnv = "prod"
	cfg.CIReleaseToken = "production-ci-fixture"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestNonDevelopmentRejectsPublicExampleSecrets(t *testing.T) {
	raw, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	example := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			example[name] = value
		}
	}
	database, err := url.Parse(example["DATABASE_URL"])
	if err != nil || database.User == nil {
		t.Fatal("example DATABASE_URL has no credentials")
	}
	databasePassword, _ := database.User.Password()
	changes := map[string]func(*Config){
		"JWT_SECRET":               func(c *Config) { c.JWTSecret = example["JWT_SECRET"] },
		"S3_ACCESS_KEY":            func(c *Config) { c.S3AccessKey = example["S3_ACCESS_KEY"] },
		"S3_SECRET_KEY":            func(c *Config) { c.S3SecretKey = example["S3_SECRET_KEY"] },
		"ADMIN_BOOTSTRAP_PASSWORD": func(c *Config) { c.AdminBootstrapPassword = example["ADMIN_BOOTSTRAP_PASSWORD"] },
		"DATABASE_URL":             func(c *Config) { c.DatabaseURL = example["DATABASE_URL"] },
	}
	for name, change := range changes {
		value := example[name]
		if name == "DATABASE_URL" {
			value = databasePassword
		}
		if len(value) < 8 {
			t.Fatalf("example %s no longer carries a development value", name)
		}
		for _, appEnv := range []string{"staging", "prod"} {
			c := validConfig()
			c.AppEnv, c.CIReleaseToken = appEnv, "production-ci-fixture"
			change(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("%s accepted the public example %s", appEnv, name)
			}
			if strings.Contains(err.Error(), value) {
				t.Fatal("error exposes a value")
			}
		}
		c := validConfig()
		change(&c)
		if err := c.Validate(); err != nil {
			t.Fatalf("dev rejected the example %s: %v", name, err)
		}
	}
}
func TestLocalEnvironmentFileDoesNotOverrideEnvironment(t *testing.T) {
	t.Setenv("ONV_TEST_EXISTING", "environment")
	path := filepath.Join(t.TempDir(), "local.env")
	if err := os.WriteFile(path, []byte("# fixture\nONV_TEST_EXISTING=file\nONV_TEST_NEW='quoted value'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("ONV_TEST_NEW") })
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ONV_TEST_EXISTING") != "environment" || os.Getenv("ONV_TEST_NEW") != "quoted value" {
		t.Fatal("environment precedence or quoted value incorrect")
	}
}
func TestEveryDocumentedEnvironmentVariableHasAField(t *testing.T) {
	example, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	typ := reflect.TypeFor[Config]()
	for i := 0; i < typ.NumField(); i++ {
		names[strings.Split(typ.Field(i).Tag.Get("env"), ",")[0]] = true
	}
	for _, line := range strings.Split(string(example), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, _ := strings.Cut(line, "=")
		if !names[name] {
			t.Errorf("missing config field %s", name)
		}
		delete(names, name)
	}
	if len(names) != 0 {
		t.Fatalf("example omits variables: %v", names)
	}
}
