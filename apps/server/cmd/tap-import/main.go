// Command tap-import registers every cask a third-party tap repository lists, the way an editor adding each
// one from the admin would. It is idempotent, so running it after a merge only adds the new casks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/store"
)

// tokenList collects repeated -token flags, which are also accepted as one comma-separated value.
type tokenList []string

func (l *tokenList) String() string { return strings.Join(*l, ",") }

func (l *tokenList) Set(value string) error {
	for _, token := range strings.Split(value, ",") {
		if token = strings.TrimSpace(token); token != "" {
			*l = append(*l, token)
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		slog.Error("tap import failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	tap := flag.String("tap", "", "tap to import, for example opennavo/tap (repository opennavo/homebrew-tap)")
	dryRun := flag.Bool("dry-run", false, "read and evaluate every cask without writing anything")
	limit := flag.Int("limit", 0, "stop after this many casks; zero imports every cask the repository lists")
	var tokens tokenList
	flag.Var(&tokens, "token", "import only this cask token (repeatable, or comma separated)")
	flag.Parse()
	if strings.TrimSpace(*tap) == "" {
		flag.Usage()
		return errors.New("-tap is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	// The import reads GitHub with the token and quota observer the admin already uses, so no separate
	// credential is configured for it.
	github := pipeline.NewResolver(cfg.FetchUserAgent)
	github.ObserveQuota = st.ObserveGitHubQuota
	github.TokenProvider = func(ctx context.Context) (string, error) { return st.GitHubToken(ctx, cfg) }
	if token, err := st.GitHubToken(ctx, cfg); err == nil && token == "" {
		fmt.Fprintln(os.Stderr, "warning: no GitHub token is configured; the unauthenticated limit is 60 requests per hour")
	}
	service := &taps.Service{Store: st, GitHub: github}
	result, err := service.Import(ctx, *tap, taps.ImportOptions{DryRun: *dryRun, Limit: *limit, Tokens: tokens})
	if result != nil {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(result); encodeErr != nil && err == nil {
			err = encodeErr
		}
	}
	return err
}
