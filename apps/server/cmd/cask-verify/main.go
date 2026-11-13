// Command cask-verify checks one cask file against the same static reader the catalog uses, then adds the
// listing rules an editor would otherwise apply by hand. The opennavo/homebrew-tap CI runs it on every
// changed file, and contributors run it locally before opening a pull request.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
)

// caskFileLimit matches the tap sync's read bound; a bigger file is not a cask we would ever import.
const caskFileLimit = 256 << 10

// installingArtifacts are the stanzas that put something on disk. A cask with only uninstall/zap or
// stage_only installs nothing, which is never an acceptable listing.
var installingArtifacts = map[string]bool{
	"app": true, "suite": true, "binary": true, "pkg": true, "installer": true, "manpage": true,
	"font": true, "colorpicker": true, "prefpane": true, "qlplugin": true, "mdimporter": true,
	"dictionary": true, "service": true, "input_method": true, "internet_plugin": true,
	"audio_unit_plugin": true, "vst_plugin": true, "vst3_plugin": true, "screen_saver": true,
	"keyboard_layout": true, "artifact": true,
	"bash_completion": true, "zsh_completion": true, "fish_completion": true,
}

// problem is one rule violation. Line is 0 when the violation is not tied to a statement.
type problem struct {
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

// report is the verification result; the JSON form is what CI parses.
type report struct {
	OK        bool              `json:"ok"`
	File      string            `json:"file"`
	Token     string            `json:"token,omitempty"`
	Version   string            `json:"version,omitempty"`
	URL       string            `json:"url,omitempty"`
	SHA256    string            `json:"sha256,omitempty"`
	Artifacts []string          `json:"artifacts,omitempty"`
	Problems  []problem         `json:"problems"`
	Meta      map[string]string `json:"-"`
}

func main() {
	file := flag.String("file", "", "cask file to verify, for example Casks/g/ghostty.rb")
	tap := flag.String("tap", "opennavo/tap", "tap the file belongs to; decides the full cask name")
	asJSON := flag.Bool("json", false, "print the result as JSON")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "usage: cask-verify -file Casks/<token>.rb [-tap owner/tap] [-json]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if strings.TrimSpace(*file) == "" {
		flag.Usage()
		os.Exit(2)
	}
	result := verify(*file, *tap)
	switch {
	case *asJSON:
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
	case result.OK:
		fmt.Printf("ok %s %s\n", result.Token, result.Version)
		for _, line := range result.Artifacts {
			fmt.Printf("  artifact %s\n", line)
		}
	default:
		for _, issue := range result.Problems {
			if issue.Line > 0 {
				fmt.Fprintf(os.Stderr, "%s:%d: %s\n", result.File, issue.Line, issue.Message)
				continue
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", result.File, issue.Message)
		}
	}
	if !result.OK {
		os.Exit(1)
	}
}

// verify reads and evaluates one cask file. Every failure is reported as a problem so a pull request shows
// all of them at once instead of one per push.
func verify(file, tap string) report {
	result := report{File: file, Problems: []problem{}}
	fail := func(line int, format string, args ...any) report {
		result.Problems = append(result.Problems, problem{Line: line, Message: fmt.Sprintf(format, args...)})
		return result
	}
	if _, err := homebrew.ParseTap(tap); err != nil {
		return fail(0, "tap %q is not usable: %v", tap, err)
	}
	token, layout := caskToken(file)
	if token == "" {
		return fail(0, "%s is not a cask file name; expected Casks/<token>.rb or Casks/<letter>/<token>.rb", filepath.Base(file))
	}
	if layout != "" && layout != "Casks/"+token+".rb" && layout != "Casks/"+token[:1]+"/"+token+".rb" {
		return fail(0, "cask files live at Casks/<token>.rb or Casks/<first letter>/<token>.rb, not %s", layout)
	}
	result.Token = token
	info, err := os.Stat(file)
	if err != nil {
		return fail(0, "cannot read the file: %v", err)
	}
	if info.Size() > caskFileLimit {
		return fail(0, "the file is %d bytes; cask files must stay under %d", info.Size(), caskFileLimit)
	}
	source, err := os.ReadFile(file) // #nosec G304 -- The operator names the cask file to verify on the command line.
	if err != nil {
		return fail(0, "cannot read the file: %v", err)
	}
	path := layout
	if path == "" {
		path = "Casks/" + token + ".rb"
	}
	raw, err := homebrew.ParseCaskSource(string(source), homebrew.CaskSource{Tap: tap, Token: token, Path: path}, time.Now())
	if err != nil {
		var unsupported *homebrew.UnsupportedCaskError
		if errors.As(err, &unsupported) {
			// The report already prefixes the line, so the error's own prefix would repeat it.
			return fail(unsupported.Line, "%s", strings.TrimPrefix(unsupported.Error(), fmt.Sprintf("line %d: ", unsupported.Line)))
		}
		return fail(0, "%v", err)
	}
	item, err := homebrew.Normalize("cask", raw)
	if err != nil {
		return fail(0, "%v", err)
	}
	result.Version, result.URL, result.SHA256 = item.Version, item.DownloadURL, rawSHA256(raw)
	if result.Version == "latest" {
		return fail(0, "version must be a real version, not :latest; the catalog compares versions to report updates")
	}
	if result.SHA256 == "" {
		return fail(0, "sha256 is required; download the release and run `shasum -a 256 <file>`")
	}
	if result.SHA256 == "no_check" {
		return fail(0, "sha256 :no_check is not accepted; every listed cask must pin a checksum")
	}
	if !strings.HasPrefix(result.URL, "https://") {
		return fail(0, "url must use https, got %q", result.URL)
	}
	if item.Desc == "" {
		return fail(0, "desc is required")
	}
	if item.Homepage == "" {
		return fail(0, "homepage is required")
	}
	if len(item.Names) == 0 {
		return fail(0, "name is required")
	}
	artifacts, installing := caskArtifacts(item.Artifacts)
	result.Artifacts = artifacts
	if !installing {
		return fail(0, "the cask installs nothing; add app, pkg, binary or another install stanza")
	}
	result.OK = true
	return result
}

// caskToken derives the cask token from the file name and, when the path is a repository path, the layout
// the file uses (from Casks/ onward). An empty layout means the file was not given as a repository path.
func caskToken(file string) (token, layout string) {
	base := filepath.Base(file)
	if !strings.HasSuffix(base, ".rb") {
		return "", ""
	}
	token = strings.TrimSuffix(base, ".rb")
	if !homebrew.ValidCaskToken(token) {
		return "", ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(file))
	if index := strings.LastIndex(cleaned, "Casks/"); index >= 0 {
		layout = cleaned[index:]
		if parsed, ok := homebrew.TapCaskTokenFromPath(layout); ok {
			return parsed, layout
		}
	}
	return token, layout
}

// caskArtifacts lists the artifact stanzas the cask declares and whether any of them installs something.
// Normalize already resolved targets and phases, so only install-phase stanzas count: uninstall, zap and
// stage_only leave nothing on disk.
func caskArtifacts(raw json.RawMessage) ([]string, bool) {
	var artifacts homebrew.CaskArtifacts
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &artifacts)
	}
	names := make([]string, 0, len(artifacts.Entries))
	installing := false
	for _, entry := range artifacts.Entries {
		names = append(names, entry.Type)
		if entry.Phase == "install" && installingArtifacts[entry.Type] {
			installing = true
		}
	}
	return names, installing
}

// rawSHA256 reads the checksum the cask declares; the normalized package keeps it in the raw document.
func rawSHA256(raw json.RawMessage) string {
	var document struct {
		SHA256 string `json:"sha256"`
	}
	_ = json.Unmarshal(raw, &document)
	return document.SHA256
}
