// update-feeds-probe reads public vendor metadata without a database or installer.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/updatefeeds"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	readonly := flag.Bool("real-read-only", false, "allow bounded public metadata GET/HEAD requests; no assets or DB writes")
	sample := flag.Int("sample", 0, "resolve a weekly rotating sample for a temporary-download CI verifier")
	flag.Parse()
	if !*readonly {
		return fmt.Errorf("--real-read-only required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	service := &updatefeeds.Service{UserAgent: "OpenNavo-update-metadata-probe/1.0"}
	var rows []map[string]any
	var err error
	if *sample > 0 {
		rows, err = service.Sample(ctx, *sample, int(time.Now().Unix()/(7*24*60*60)))
	} else {
		rows, err = service.ProbeSeeds(ctx)
	}
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		return err
	}
	return nil
}
