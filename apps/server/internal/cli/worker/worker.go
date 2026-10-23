package worker

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/hibiken/asynq"

	"encoding/json"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/observability"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/changelog"
	"github.com/opennavo/opennavo/server/internal/service/links"
	"github.com/opennavo/opennavo/server/internal/service/maintenance"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seoexperiments"
	"github.com/opennavo/opennavo/server/internal/service/seoingest"
	"github.com/opennavo/opennavo/server/internal/service/seoopportunities"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/service/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/service/updatefeeds"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/opennavo/opennavo/server/internal/store"
)

func Main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		slog.Error("worker failed", "error", err)
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
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	location, err := time.LoadLocation(cfg.TZSchedule)
	if err != nil {
		return err
	}
	queue := &jobs.Enqueuer{Client: asynq.NewClientFromRedisClient(st.Redis), Redis: st.Redis}
	service := &catalog.Service{Store: st, Source: homebrew.NewClient(cfg.HomebrewAPIBase, cfg.FetchUserAgent), Queue: queue}
	analytics := &catalog.AnalyticsService{Store: st, Source: homebrew.NewClient(cfg.HomebrewAPIBase, cfg.FetchUserAgent), Queue: queue, Location: location}
	handlers := jobs.Handlers{"catalog:sync": func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return service.Run(ctx) }}
	handlers["analytics:sync"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return analytics.Run(ctx) }
	handlers["seo:pages"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) {
		count, err := st.RefreshPackagePageStates(ctx, 100)
		if err != nil {
			return map[string]any{"packages": count}, err
		}
		// Versions pages written before their notes facts were stored get them a few hundred apps per run.
		notes, err := st.BackfillPageNotes(ctx, 200)
		return map[string]any{"packages": count, "notesBackfilled": notes}, err
	}
	seoData := seoingest.New(st, cfg)
	handlers["seo:gsc"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return seoData.GSC(ctx) }
	handlers["seo:inspect"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return seoData.Inspect(ctx) }
	handlers["seo:bing"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return seoData.BingStats(ctx) }
	handlers["seo:audit"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return seoData.AuditPages(ctx) }
	handlers["seo:opportunities"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) {
		return (&seoopportunities.Service{DB: st.DB}).Run(ctx)
	}
	handlers["seo:evaluate"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) {
		return (&management.Service{Store: st, Config: cfg}).EvaluateSEO(ctx)
	}
	handlers["seo:experiments"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) {
		return (&seoexperiments.Service{DB: st.DB, WebURL: cfg.WebBaseURL}).Checkpoints(ctx)
	}
	objects, err := storage.New(cfg)
	if err != nil {
		return err
	}
	sizeService := &assets.SizeService{Store: st, Fetcher: assets.NewHEADFetcher(cfg.FetchUserAgent)}
	handlers["assets:download-size"] = func(ctx context.Context, p jobs.Payload) (map[string]any, error) {
		return sizeService.Run(ctx, p.PackageID)
	}
	cleanup := &maintenance.Service{Store: st, Objects: maintenance.S3Objects{Client: objects}}
	handlers["cleanup"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return cleanup.Run(ctx) }
	snapshots := &snapshot.Service{Store: st, Objects: assets.S3Objects{Client: objects}}
	handlers["snapshot:build"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return snapshots.Build(ctx) }
	releaseService := &changelog.Service{Store: st, Resolver: pipeline.NewResolver(cfg.FetchUserAgent), Fetcher: pipeline.NewFetcher(cfg.FetchUserAgent), Queue: queue}
	releaseService.Resolver.ObserveQuota = st.ObserveGitHubQuota
	releaseService.Fetcher.HTTP.ObserveQuota = st.ObserveGitHubQuota
	releaseService.Fetcher.HTTP.TokenProvider = func(ctx context.Context) (string, error) {
		return st.GitHubToken(ctx, cfg)
	}
	handlers["changelog:resolve"] = func(ctx context.Context, p jobs.Payload) (map[string]any, error) {
		return releaseService.Resolve(ctx, p.PackageID)
	}
	handlers["changelog:fetch"] = func(ctx context.Context, p jobs.Payload) (map[string]any, error) {
		return releaseService.Fetch(ctx, p.SourceID)
	}
	handlers["changelog:schedule"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return releaseService.Schedule(ctx) }
	// Source facts read GitHub with the changelog's token and leave it the last tenth of the quota.
	githubFacts := pipeline.NewResolver(cfg.FetchUserAgent)
	githubFacts.ObserveQuota = st.ObserveGitHubQuota
	githubFacts.TokenProvider = releaseService.Fetcher.HTTP.TokenProvider
	factSources := &sourcefacts.Service{Store: st, NVD: &sources.NVD{Client: fetch.NewClient(20 * time.Second), UserAgent: cfg.FetchUserAgent}, GitHub: &sources.GitHub{HTTP: githubFacts},
		AppStore:    &sources.AppStore{Client: fetch.NewClient(15 * time.Second), UserAgent: cfg.FetchUserAgent},
		GitHubPause: 200 * time.Millisecond, AppStorePause: 3500 * time.Millisecond}
	handlers["facts:sources"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return factSources.Run(ctx) }
	updateFeeds := &updatefeeds.Service{Store: st, UserAgent: cfg.FetchUserAgent, GitHub: githubFacts, SeedRules: true}
	handlers["update-feeds:verify"] = func(ctx context.Context, _ jobs.Payload) (map[string]any, error) { return updateFeeds.Run(ctx) }
	linkApps := &links.Service{Store: st, GitHub: githubFacts, UserAgent: cfg.FetchUserAgent}
	handlers["links:sync"] = func(ctx context.Context, p jobs.Payload) (map[string]any, error) {
		return linkApps.Sync(ctx, p.PackageID)
	}
	tapCasks := &taps.Service{Store: st, GitHub: githubFacts}
	handlers["taps:sync"] = func(ctx context.Context, p jobs.Payload) (map[string]any, error) {
		return tapCasks.Run(ctx, p.PackageID)
	}
	// Independent delivery polling keeps the 30-second source-save debounce independent of catalog sync intervals.
	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	defer cancelDispatch()
	// Content written before a site language existed never queued that language.
	go func() {
		queued, ran, err := st.BackfillTranslationTasksForLocales(dispatchCtx)
		if err != nil {
			logger.Warn("translation backfill failed")
		} else if ran {
			logger.Info("translation backfill queued", "tasks", queued)
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-dispatchCtx.Done():
				return
			case <-ticker.C:
				_, err := st.DispatchOutbox(dispatchCtx, func(ctx context.Context, kind string, raw json.RawMessage) error {
					payload, meta, err := jobs.DecodeOutbox(raw)
					if err != nil {
						return err
					}
					_, err = queue.Enqueue(ctx, kind, payload, meta)
					return jobs.OutboxDeliveryError(kind, err)
				})
				if err != nil && dispatchCtx.Err() == nil {
					logger.Error("outbox delivery failed")
				}
			}
		}
	}()
	defer func() { cancelDispatch(); <-done }()
	// URL change feed (WP4): refresh page caches and notify search engines after catalog changes.
	notifier := &urlnotify.Notifier{Store: st, Config: cfg, Logger: logger}
	notifyDone := make(chan struct{})
	go func() {
		defer close(notifyDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-dispatchCtx.Done():
				return
			case <-ticker.C:
				if result, err := notifier.Run(dispatchCtx); err != nil && dispatchCtx.Err() == nil {
					logger.Error("url change feed failed", "error", err)
				} else if result.Changes > 0 {
					logger.Info("url change feed", "changes", result.Changes, "paths", result.Paths, "purged", result.Purged, "announced", result.Announced)
				}
			}
		}
	}()
	defer func() { cancelDispatch(); <-notifyDone }()
	return jobs.Run(ctx, st.Redis, st, handlers, location, logger)
}
