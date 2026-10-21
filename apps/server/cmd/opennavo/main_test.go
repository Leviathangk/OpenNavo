package main

import (
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestDockerfileLinksEveryCommand(t *testing.T) {
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
	if want := slices.Sorted(maps.Keys(commands)); !slices.Equal(linked, want) {
		t.Fatalf("the Dockerfile links %v; opennavo runs %v", linked, want)
	}
}
