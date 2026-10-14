package homebrew

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestParseCaskSourceCorpus checks the static reader against Homebrew's own API output. It needs a
// homebrew-cask checkout and the matching https://formulae.brew.sh/api/cask.json, so it only runs when
// OPENNAVO_CASK_CORPUS (repository root) and OPENNAVO_CASK_JSON (file) are set.
func TestParseCaskSourceCorpus(t *testing.T) {
	root, api := os.Getenv("OPENNAVO_CASK_CORPUS"), os.Getenv("OPENNAVO_CASK_JSON")
	if root == "" || api == "" {
		t.Skip("set OPENNAVO_CASK_CORPUS and OPENNAVO_CASK_JSON to compare against Homebrew's API")
	}
	data, err := os.ReadFile(api) // #nosec G304 G703 -- Opt-in test reading a file the developer supplies.
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	official := map[string]json.RawMessage{}
	for _, raw := range entries {
		var head struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(raw, &head)
		official[head.Token] = raw
	}
	reasons := map[string]int{}
	mismatches := map[string][]string{}
	parsed, total := 0, 0
	corpus := os.DirFS(root)
	err = fs.WalkDir(corpus, "Casks", func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(file, ".rb") {
			return err
		}
		token := strings.TrimSuffix(entry.Name(), ".rb")
		want, ok := official[token]
		if !ok {
			return nil
		}
		total++
		source, err := fs.ReadFile(corpus, file)
		if err != nil {
			return err
		}
		var meta struct {
			TapGitHead string `json:"tap_git_head"`
		}
		_ = json.Unmarshal(want, &meta)
		raw, err := ParseCaskSource(string(source), CaskSource{Tap: "homebrew/cask", Token: token, Path: file, GitHead: meta.TapGitHead}, time.Now())
		var unsupported *UnsupportedCaskError
		if errors.As(err, &unsupported) {
			reason := unsupported.Reason
			if index := strings.IndexAny(reason, "\"#"); index > 0 {
				reason = reason[:index]
			}
			reasons[reason]++
			return nil
		}
		if err != nil {
			t.Errorf("%s: %v", token, err)
			return nil
		}
		parsed++
		for field, diff := range compareCasks(t, raw, want) {
			mismatches[field] = append(mismatches[field], token+": "+diff)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("parsed %d of %d casks", parsed, total)
	keys := make([]string, 0, len(reasons))
	for reason := range reasons {
		keys = append(keys, reason)
	}
	sort.Slice(keys, func(i, j int) bool { return reasons[keys[i]] > reasons[keys[j]] })
	for _, reason := range keys {
		t.Logf("unsupported %5d  %s", reasons[reason], reason)
	}
	fields := make([]string, 0, len(mismatches))
	for field := range mismatches {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		samples := mismatches[field]
		t.Errorf("%s differs for %d casks, e.g.\n  %s", field, len(samples), strings.Join(samples[:min(len(samples), 4)], "\n  "))
	}
}

func compareCasks(t *testing.T, got, want json.RawMessage) map[string]string {
	t.Helper()
	ours, err := Normalize("cask", got)
	if err != nil {
		return map[string]string{"normalize": err.Error()}
	}
	theirs, err := Normalize("cask", want)
	if err != nil {
		return nil
	}
	diffs := map[string]string{}
	check := func(field string, a, b any) {
		if !reflect.DeepEqual(a, b) {
			diffs[field] = fmt.Sprintf("got %v want %v", a, b)
		}
	}
	check("name", ours.Name, theirs.Name)
	// Old tokens come from the tap's cask_renames.json, not from the cask file.
	check("names", ours.Names, slices.DeleteFunc(slices.Clone(theirs.Names), func(name string) bool {
		return slices.Contains(theirs.OldTokens, name) && !slices.Contains(ours.Names, name)
	}))
	check("desc", ours.Desc, theirs.Desc)
	check("homepage", ours.Homepage, theirs.Homepage)
	check("version", ours.Version, theirs.Version)
	check("url", ours.DownloadURL, theirs.DownloadURL)
	check("auto_updates", ours.AutoUpdates, theirs.AutoUpdates)
	check("arm64", ours.SupportsArm64, theirs.SupportsArm64)
	check("x86_64", ours.SupportsX8664, theirs.SupportsX8664)
	check("min_macos", ours.MinMacos, theirs.MinMacos)
	check("deprecated", []any{ours.Deprecated, ours.DeprecationReason, ours.DeprecationReplacement}, []any{theirs.Deprecated, theirs.DeprecationReason, theirs.DeprecationReplacement})
	check("disabled", []any{ours.Disabled, ours.DisableDate, ours.DisableReason, ours.DisableReplacement}, []any{theirs.Disabled, theirs.DisableDate, theirs.DisableReason, theirs.DisableReplacement})
	// caveats blocks are skipped, so only literal caveats are compared.
	if ours.Caveats != "" {
		check("caveats", strings.TrimSpace(ours.Caveats), strings.TrimSpace(theirs.Caveats))
	}
	check("conflicts_with", canonical(ours.ConflictsWith), canonical(theirs.ConflictsWith))
	var a, b CaskArtifacts
	_ = json.Unmarshal(ours.Artifacts, &a)
	_ = json.Unmarshal(theirs.Artifacts, &b)
	check("apps", a.Apps, b.Apps)
	// Homebrew sorts artifacts with Ruby's unstable sort, so order within one artifact type is arbitrary.
	check("binaries", sorted(a.Binaries), sorted(b.Binaries))
	check("pkgs", a.Pkgs, b.Pkgs)
	check("uninstall", canonical(marshal(a.Uninstall)), canonical(marshal(b.Uninstall)))
	check("artifact_entries", sorted(artifactSummary(a)), sorted(artifactSummary(b)))
	var x, y CaskPlatformMetadata
	_ = json.Unmarshal(ours.Dependencies, &x)
	_ = json.Unmarshal(theirs.Dependencies, &y)
	check("depends_on", canonical(marshal(x.DependsOn)), canonical(marshal(y.DependsOn)))
	check("support_status", x.SupportStatus, y.SupportStatus)
	check("rosetta", x.RequiresRosetta, y.RequiresRosetta)
	check("platforms", platformSummary(x), platformSummary(y))
	return diffs
}

func sorted(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

func canonical(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func artifactSummary(artifacts CaskArtifacts) []string {
	out := []string{}
	for _, entry := range artifacts.Entries {
		if strings.HasSuffix(entry.Type, "_steps") || flightBlocks[entry.Type] {
			continue
		}
		target := ""
		if entry.Target != nil {
			target = *entry.Target
		}
		out = append(out, entry.Type+" "+strings.Join(entry.Sources, ",")+" -> "+target+" "+canonical(marshal(entry.Declaration)))
	}
	return out
}

func platformSummary(metadata CaskPlatformMetadata) []string {
	out := []string{}
	for _, platform := range metadata.Platforms {
		out = append(out, fmt.Sprintf("%s %s %s %v", platform.Tag, platform.Version, stringValue(platform.MinMacos), platform.RequiresRosetta))
	}
	return out
}
