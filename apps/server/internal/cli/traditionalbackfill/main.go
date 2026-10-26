// Package traditionalbackfill exposes an offline preview and restartable migration.
package traditionalbackfill

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
)

func Main() {
	os.Exit(run())
}

// Return the exit status so signal and store cleanup runs before Main exits.
func run() int {
	apply := flag.Bool("apply", false, "Persist generated Traditional Chinese; default rolls back each preview transaction")
	rollback := flag.Bool("rollback", false, "Restore unchanged generated rows from retained receipts; stop the new worker first")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration unavailable")
		return 1
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "store unavailable")
		return 1
	}
	defer func() { _ = st.Close() }()
	var counts map[string]int
	if *rollback {
		counts, err = st.RollbackTraditional(ctx, !*apply)
	} else {
		// Explicit backfills use configured limits; progress goes to stderr, the result to stdout.
		counts, err = st.RunTraditionalBackfill(ctx, store.TraditionalBackfill{DryRun: !*apply, ObjectsPerSecond: cfg.TraditionalObjectsPerSecond, WritesPerMinute: cfg.TraditionalWritesPerMinute,
			Progress: func(p store.TraditionalProgress) {
				fmt.Fprintf(os.Stderr, "processed=%d stamped=%d written=%d failed=%d pending=%d remaining=%d eta=%s\n", p.Processed, p.Stamped, p.Written, p.Failed, p.Pending, p.Remaining, p.ETA.Round(time.Second))
			}})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Traditional Chinese migration failed; inspect locally without exposing credentials")
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"applied": *apply, "changed": counts}); err != nil {
		fmt.Fprintln(os.Stderr, "migration output unavailable")
		return 1
	}
	return 0
}
