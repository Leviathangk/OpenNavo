// Package assetsbackfill exposes a throttled, restartable rendition backfill.
package assetsbackfill

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/opennavo/opennavo/server/internal/store"
)

func Main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func parseOptions(args []string, output io.Writer) (assets.BackfillOptions, error) {
	options := assets.BackfillOptions{}
	flags := flag.NewFlagSet("assets-backfill", flag.ContinueOnError)
	flags.SetOutput(output)
	var dryRun bool
	flags.BoolVar(&dryRun, "dry-run", true, "Preview missing manifests only; no image reads, uploads or writes")
	flags.BoolVar(&options.Apply, "apply", false, "Generate and append missing renditions (disables the default dry-run)")
	flags.StringVar(&options.Kind, "kind", "", "icon or screenshot; omit to process both")
	flags.IntVar(&options.Limit, "limit", 0, "Maximum candidate assets to attempt; 0 means all")
	flags.Float64Var(&options.Rate, "rate", 2, "Maximum assets started per second across all workers")
	flags.IntVar(&options.Concurrency, "concurrency", 1, "Concurrent assets (1..8); keep 1 on a small production host")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() > 0 {
		return options, errors.New("unexpected positional arguments")
	}
	explicitDryRun := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "dry-run" {
			explicitDryRun = true
		}
	})
	if options.Apply && explicitDryRun && dryRun {
		return options, errors.New("choose either --apply or --dry-run")
	}
	if !options.Apply && !dryRun {
		return options, errors.New("writes require explicit --apply")
	}
	return options, options.Validate()
}

func run(args []string, stdout, stderr io.Writer) int {
	options, err := parseOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	// The standalone process defaults to one CPU lane as well as one asset,
	// keeping spare CPU for the existing worker on a two-core host.
	runtime.GOMAXPROCS(options.Concurrency)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "configuration unavailable")
		return 1
	}
	cfg.DBMaxOpenConns = options.Concurrency + 1
	st, err := store.Open(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "store unavailable")
		return 1
	}
	defer func() { _ = st.Close() }()
	service := &assets.Service{Store: st}
	if options.Apply {
		objects, err := storage.New(cfg)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "object storage configuration unavailable")
			return 1
		}
		service.Objects = assets.S3Objects{Client: objects}
	}
	mode := "dry-run"
	if options.Apply {
		mode = "apply"
	}
	_, _ = fmt.Fprintf(stderr, "assets-backfill mode=%s kind=%q rate=%g concurrency=%d limit=%d\n", mode, options.Kind, options.Rate, options.Concurrency, options.Limit)
	last := time.Time{}
	options.Progress = func(p assets.BackfillProgress) {
		if p.Failure != "" {
			_, _ = fmt.Fprintf(stderr, "asset_id=%d failed_reason=%q\n", p.AssetID, p.Failure)
		}
		if p.SkipReason != "" {
			_, _ = fmt.Fprintf(stderr, "asset_id=%d skipped_reason=%q retry=next_run\n", p.AssetID, p.SkipReason)
		}
		if p.Processed%25 == 0 || p.Remaining == 0 || time.Since(last) >= 10*time.Second {
			writeProgress(stderr, p)
			last = time.Now()
		}
	}
	p, err := service.BackfillRenditions(ctx, options)
	writeProgress(stdout, p)
	if errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(stderr, "stopped; rerun the same command to resume missing assets")
		return 130
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assets-backfill failed: %s\n", assets.BackfillFailureReason(err))
		return 1
	}
	if p.Failed > 0 {
		return 1
	}
	return 0
}

func writeProgress(output io.Writer, p assets.BackfillProgress) {
	_, _ = fmt.Fprintf(output, "total=%d processed=%d written=%d files=%d skipped=%d deferred=%d failed=%d remaining=%d pending=%d eta=%s\n", p.Total, p.Processed, p.Written, p.Files, p.Skipped, p.Deferred, p.Failed, p.Remaining, p.Pending, p.ETA.Round(time.Second))
}
