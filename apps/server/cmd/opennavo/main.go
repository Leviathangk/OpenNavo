// opennavo runs the server image's heavy commands from one binary. They all link
// the same packages, lingua-go's 123 MB of language models among them, so the
// image ships those once and links /api, /worker and the rest to this binary.
package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opennavo/opennavo/server/internal/cli/api"
	"github.com/opennavo/opennavo/server/internal/cli/dbbootstrap"
	"github.com/opennavo/opennavo/server/internal/cli/desktoppublish"
	"github.com/opennavo/opennavo/server/internal/cli/migrate"
	"github.com/opennavo/opennavo/server/internal/cli/seedcmd"
	"github.com/opennavo/opennavo/server/internal/cli/seede2e"
	"github.com/opennavo/opennavo/server/internal/cli/worker"
)

// The Dockerfile links each name to this binary.
var commands = map[string]func(){
	"api":             api.Main,
	"db-bootstrap":    dbbootstrap.Main,
	"desktop-publish": desktoppublish.Main,
	"migrate":         migrate.Main,
	"seed":            seedcmd.Main,
	"seed-e2e":        seede2e.Main,
	"worker":          worker.Main,
}

// The command is the name the binary runs as (/migrate up), or the first argument
// (opennavo migrate up), which then becomes the command's own argv[0].
func main() {
	if run, ok := commands[filepath.Base(os.Args[0])]; ok {
		run()
		return
	}
	if len(os.Args) > 1 {
		if run, ok := commands[os.Args[1]]; ok {
			os.Args = os.Args[1:]
			run()
			return
		}
	}
	fmt.Fprintf(os.Stderr, "usage: opennavo %s [arguments]\n", strings.Join(slices.Sorted(maps.Keys(commands)), "|"))
	os.Exit(2)
}
