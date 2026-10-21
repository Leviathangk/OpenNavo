package seedcmd

import (
	"context"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
)

func Main() {
	if err := run(); err != nil {
		slog.Error("seed failed")
		os.Exit(1)
	}
	slog.Info("seed completed")
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return seed.Run(ctx, st, cfg)
}
