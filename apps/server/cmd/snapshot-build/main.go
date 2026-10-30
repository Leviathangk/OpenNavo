package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/opennavo/opennavo/server/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		slog.Error("snapshot build failed")
		os.Exit(1)
	}
}
func run(ctx context.Context) error {
	output := flag.String("output", "", "Build read-only under tmp/ without publishing objects or replacing latest")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.AppEnv != "dev" {
		return errors.New("icon acceptance command requires development environment")
	}
	if *output != "" {
		directory, err := filepath.Abs(*output)
		if err != nil {
			return err
		}
		allowed, err := filepath.Abs("tmp")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(directory, allowed+string(os.PathSeparator)) {
			return errors.New("local output must be inside apps/server/tmp")
		}
		db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			return errors.New("read-only database unavailable")
		}
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		defer func() { _ = sqlDB.Close() }()
		service := &snapshot.Service{Store: &store.Store{DB: db}, Objects: localObjects{}}
		bundle, err := service.BuildLocal(ctx)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		for _, file := range bundle.Files {
			if err = os.WriteFile(filepath.Join(directory, filepath.Base(file.Key)), file.Data, 0600); err != nil {
				return err
			}
		}
		metadata, err := json.MarshalIndent(bundle.Info, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(directory, "manifest.json"), metadata, 0600); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(bundle.Info)
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	objects, err := storage.New(cfg)
	if err != nil {
		return err
	}
	service := &snapshot.Service{Store: st, Objects: assets.S3Objects{Client: objects}, Purge: func(ctx context.Context, prefixes []string) error {
		return (&urlnotify.Notifier{Store: st, Config: cfg}).PurgeSnapshots(ctx, prefixes)
	}}
	var stats map[string]any
	handler := jobs.WrapHandler(st, nil, func(ctx context.Context, _ jobs.Payload) (map[string]any, error) {
		var err error
		stats, err = service.Build(ctx)
		return stats, err
	}, jobs.Metadata{Trigger: "manual"})
	if err := handler.ProcessTask(ctx, asynq.NewTask("snapshot:build", []byte(`{}`))); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(stats)
}

type localObjects struct{}

func (localObjects) URL(key string) string { return "http://localhost.invalid/" + key }
func (localObjects) Put(context.Context, string, []byte, string) error {
	return errors.New("read-only local build cannot publish")
}
