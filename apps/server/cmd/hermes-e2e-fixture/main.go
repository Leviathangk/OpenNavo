// Dedicated E2E fixture: append G-prefixed Homebrew facts without running a full seed/reset.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
)

func validate(environment, database, prefix string) error {
	u, err := url.Parse(database)
	if err != nil || environment != "dev" || u.Scheme != "postgres" || u.Path != "/opennavo_e2e" ||
		u.Hostname() != "127.0.0.1" || (u.Port() != "55432" && u.Port() != "55433") ||
		!regexp.MustCompile(`^G-[a-f0-9]{10}$`).MatchString(prefix) {
		return errors.New("requires fixed local E2E database and G fixture prefix")
	}
	return nil
}
func run() error {
	prefix := os.Getenv("HERMES_E2E_PREFIX")
	// Check the process environment before connecting; .env.local must not supply the development database connection.
	if err := validate(os.Getenv("APP_ENV"), os.Getenv("DATABASE_URL"), prefix); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := validate(cfg.AppEnv, cfg.DatabaseURL, prefix); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if os.Getenv("HERMES_E2E_REPLAY") == "seo" {
		report, err := replaySEO(ctx, st.DB)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	newToken, updatedToken := strings.ToLower(prefix)+"-new", strings.ToLower(prefix)+"-updated"
	for _, item := range []struct{ token, version string }{{newToken, "1.0.0"}, {updatedToken, "1.0.0"}, {updatedToken, "1.1.0"}} {
		raw, _ := json.Marshal(map[string]any{"token": item.token, "version": item.version, "name": []string{prefix}, "homepage": "https://example.com", "url": "https://example.com/g.dmg"})
		p, normalizeErr := homebrew.Normalize("cask", raw)
		if normalizeErr != nil {
			return normalizeErr
		}
		previous, readErr := st.CatalogRows(ctx, "cask")
		if readErr != nil {
			return readErr
		}
		if err := st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p, Previous: previous[item.token]}}); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"newToken": newToken, "updatedToken": updatedToken})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "G fixture failed (details withheld)")
		os.Exit(1)
	}
}
