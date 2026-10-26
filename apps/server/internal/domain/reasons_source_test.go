package domain

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Agents match refusal reasons literally, so every reason written in server code
// is lowercase snake_case with optional ":"-separated details. A trailing ":" is
// a prefix that code completes with a detail.
func TestLiteralRefusalReasonsFollowTheFormat(t *testing.T) {
	literal := regexp.MustCompile(`(?:\bRejected|\bRejectedDetail|\brejected|\breject|\bforbidden)\(\s*"([^"]*)"|\bForbidden\([^,()]+,\s*"([^"]*)"|"rejected: ([^"]*)"`)
	format := regexp.MustCompile(`^[a-z][a-z0-9_]*(:[A-Za-z0-9_.@+/-]+)*:?$`)
	checked := 0
	root := os.DirFS("..")
	err := fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		source, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(source), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, match := range literal.FindAllStringSubmatch(line, -1) {
				reason := match[1] + match[2] + match[3]
				if reason == "" {
					continue
				}
				checked++
				if !format.MatchString(reason) {
					t.Errorf("%s: reason %q does not follow the format", path, reason)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("checked only %d reasons; the scan no longer finds them", checked)
	}
}
