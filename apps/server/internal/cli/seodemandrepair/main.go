// Package seodemandrepair previews or applies keyword task reclassification.
package seodemandrepair

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/seoopportunities"
	"github.com/opennavo/opennavo/server/internal/store"
)

func Main() { os.Exit(run()) }
func run() int {
	apply := flag.Bool("apply", false, "Persist reclassification; default previews and rolls back")
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
	count, err := (&seoopportunities.Service{DB: st.DB}).RepairDemand(ctx, *apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "SEO demand repair failed; inspect database diagnostics locally")
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"applied": *apply, "tasks": count}); err != nil {
		return 1
	}
	return 0
}
