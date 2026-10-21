package migrate

import (
	"context"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
)

func Main() {
	if err := run(); err != nil {
		slog.Error("migration failed")
		os.Exit(1)
	}
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
	db, err := st.DB.DB()
	if err != nil {
		return err
	}
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	return goose.RunContext(ctx, command, db, ".", os.Args[min(2, len(os.Args)):]...)
}
