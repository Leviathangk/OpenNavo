package main

import (
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestDockerfileLinksEntrypoints(t *testing.T) {
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`for name in ([a-z0-9 -]+); do ln -s opennavo /out/"\$name"; done`).FindSubmatch(dockerfile)
	if match == nil {
		t.Fatal("the Dockerfile no longer links the commands to opennavo")
	}
	linked := strings.Fields(string(match[1]))
	slices.Sort(linked)
	entrypoints := maps.Clone(commands)
	// Offline maintenance is invoked as /opennavo assets-backfill through
	// docker exec; it needs no additional Docker image entrypoint or symlink.
	if entrypoints["assets-backfill"] == nil {
		t.Fatal("assets-backfill must be registered as an opennavo subcommand")
	}
	delete(entrypoints, "assets-backfill")
	if entrypoints["index-report"] == nil {
		t.Fatal("index-report must be registered as an offline subcommand")
	}
	delete(entrypoints, "index-report")
	if want := slices.Sorted(maps.Keys(entrypoints)); !slices.Equal(linked, want) {
		t.Fatalf("the Dockerfile links %v; opennavo runs %v", linked, want)
	}
}
