package seede2e

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/e2e"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/opennavo/opennavo/server/internal/store"
)

func Main() {
	if err := run(); err != nil {
		slog.Error("seed-e2e failed", "reason", err.Error())
		os.Exit(1)
	}
}
func run() error {
	// Reject explicit production environments before loading configuration or opening connections.
	if os.Getenv("APP_ENV") == "prod" {
		return errors.New("seed-e2e is forbidden in prod")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.AppEnv == "prod" {
		return errors.New("seed-e2e is forbidden in prod")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	objects, err := storage.New(cfg)
	if err != nil {
		return err
	}
	stats, err := (&e2e.Service{Store: st, Objects: assets.S3Objects{Client: objects}}).Run(ctx, cfg)
	if err != nil {
		return err
	}
	slog.Info("seed-e2e completed", "stats", stats)
	return nil
}
