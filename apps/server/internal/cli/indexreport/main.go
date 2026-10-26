// Package indexreport provides an offline, strictly read-only URL inventory.
package indexreport

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("index-report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	format := flags.String("format", "text", "Output: text or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || store.ValidateIndexReportFormat(*format) != nil {
		_, _ = fmt.Fprintln(stderr, "usage: opennavo index-report [-format text|json]")
		return 2
	}
	// Only DATABASE_URL is required; never initialize services or load/migrate application state.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		_, _ = fmt.Fprintln(stderr, "DATABASE_URL is required")
		return 1
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableAutomaticPing: true})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "database unavailable")
		return 1
	}
	sqlDB, err := db.DB()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "database unavailable")
		return 1
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Match Web runtimeConfig.sitemapFonts; use the same NUXT_SITEMAP_FONTS value
	// as the deployed Web process (unset means disabled).
	report, err := (&store.Store{DB: db}).BuildIndexReport(ctx, time.Now(), os.Getenv("WEB_BASE_URL"), os.Getenv("NUXT_SITEMAP_FONTS") == "true")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "index report failed (read-only transaction); inspect local database diagnostics")
		return 1
	}
	if err = Write(out, *format, report); err != nil {
		_, _ = fmt.Fprintln(stderr, "cannot write report")
		return 1
	}
	return 0
}

func Write(w io.Writer, format string, report store.IndexReport) error {
	if err := store.ValidateIndexReportFormat(format); err != nil {
		return err
	}
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(tw, "Generated: %s\nScope: %s\n\nPAGE TYPE\tLOCALE\tDECISION\tSITEMAP\tURLS\n", report.GeneratedAt.Format(time.RFC3339), report.Scope); err != nil {
		return err
	}
	for _, row := range report.Rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%d\n", row.PageType, row.Locale, row.Decision, row.InSitemap, row.Count); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\nMachine-only translated overview snapshots in open locales (status=machine, non-source, complete published text; excludes hidden): %v\n", report.MachineOnlyOverview)
	return err
}
