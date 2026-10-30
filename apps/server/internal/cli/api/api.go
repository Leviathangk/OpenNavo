package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/cache"
	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/observability"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/search"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

func Main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx)
	stop()
	if err != nil {
		slog.Error("API startup failed", "error", err)
		os.Exit(1)
	}
}
func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	st, err := store.Open(connectCtx, cfg)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("close data connections failed")
		}
	}()
	cacheClient := st.Redis
	if cfg.CacheRedisURL != "" {
		opts, err := redis.ParseURL(cfg.CacheRedisURL)
		if err != nil {
			return errors.New("invalid cache Redis connection configuration")
		}
		opts.MaxRetries = 1
		opts.DialTimeout = time.Second
		opts.ReadTimeout = time.Second
		opts.WriteTimeout = time.Second
		cacheClient = redis.NewClient(opts)
		defer func() { _ = cacheClient.Close() }()
	}
	cacheBackend := cache.Visibility{Backend: cache.Redis{Client: cacheClient}, Generation: st.VisibilityGeneration}
	catalogService := &catalog.PublicService{Store: st, Cache: cacheBackend, WebBaseURL: cfg.WebBaseURL, Secrets: cfg}
	warmContext, stopWarm := context.WithCancel(ctx)
	defer stopWarm()
	go catalogService.WarmBrowse(warmContext)
	searchService := search.New(ctx, st, cacheBackend, logger)
	defer searchService.Close()
	stopCache, err := cache.Subscribe(ctx, st.Redis, logger, cacheClient)
	if err != nil {
		return fmt.Errorf("subscribe cache: %w", err)
	}
	defer stopCache()
	objects, err := storage.New(cfg)
	if err != nil {
		return err
	}
	assetService := &assets.Service{Store: st, Objects: assets.S3Objects{Client: objects}}
	authentication, err := auth.New(st, cfg)
	if err != nil {
		return err
	}
	desktopService := &desktop.Service{Store: st, Objects: desktop.S3Objects{S3Objects: assets.S3Objects{Client: objects}}}
	// Adding a third-party tap cask reads it from GitHub with the changelog's token and quota observer.
	tapGitHub := pipeline.NewResolver(cfg.FetchUserAgent)
	tapGitHub.ObserveQuota = st.ObserveGitHubQuota
	tapGitHub.TokenProvider = func(ctx context.Context) (string, error) { return st.GitHubToken(ctx, cfg) }
	adminService := &management.Service{Desktop: desktopService, Store: st, Config: cfg, Queue: &jobs.Enqueuer{Client: asynq.NewClientFromRedisClient(st.Redis), Redis: st.Redis}, IconAccent: assetService.IconAccent, Taps: &taps.Service{Store: st, GitHub: tapGitHub}}
	adminService.InvalidatePublic = func(ctx context.Context) error {
		return cache.Invalidate(ctx, cacheClient, []string{"c:*"})
	}
	adminService.InvalidateShareImages = func(ctx context.Context, token string) (string, error) {
		return (&urlnotify.Notifier{Store: st, Config: cfg, Logger: logger}).QueueShareImagePurgeWithWarning(ctx, token)
	}
	snapshots := &snapshot.Service{Store: st, Objects: assets.S3Objects{Client: objects}, Purge: func(ctx context.Context, prefixes []string) error {
		return (&urlnotify.Notifier{Store: st, Config: cfg, Logger: logger}).PurgeSnapshots(ctx, prefixes)
	}}
	adminService.Snapshots = snapshots
	queueMetrics := observability.NewQueueMetrics(st.Redis)
	defer func() { _ = queueMetrics.Close() }()
	app, err := httpserver.New(cfg, logger, st, httpserver.Dependencies{Collectors: []prometheus.Collector{observability.NewBusinessMetrics(st), observability.NewRedisMetrics(st.Redis, cacheClient), queueMetrics}, Admin: &admin.Handler{Management: adminService, Auth: authentication, Assets: assetService, CIToken: cfg.CIReleaseToken}, Public: &public.Handler{Desktop: desktopService, Snapshot: snapshots, Catalog: catalogService, Search: searchService}, Rate: &middleware.RateLimiter{Client: st.Redis}})
	if err != nil {
		return fmt.Errorf("create HTTP server: %w", err)
	}
	api := &http.Server{Addr: cfg.HTTPAddr, Handler: app.Engine, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	metrics := &http.Server{Addr: cfg.MetricsAddr, Handler: app.Metrics, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	failures := make(chan error, 2)
	for _, server := range []*http.Server{api, metrics} {
		go func() {
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				failures <- err
			}
		}()
	}
	logger.Info("API listening", "httpAddr", cfg.HTTPAddr, "metricsAddr", cfg.MetricsAddr)
	var result error
	select {
	case <-ctx.Done():
	case result = <-failures:
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, server := range []*http.Server{api, metrics} {
		if err := server.Shutdown(shutdown); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
