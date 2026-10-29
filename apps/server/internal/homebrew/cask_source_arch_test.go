package homebrew

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseOrcaArchitectures(t *testing.T) {
	source, err := os.ReadFile("testdata/casks/orca.rb")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ParseCaskSource(string(source), CaskSource{Tap: "stablyai/orca", Token: "orca", Path: "Casks/orca.rb", GitHead: "a98f4efbafd0a0959fde2de1680179d3e01bab88"}, sourceDay)
	if err != nil {
		t.Fatal(err)
	}
	item, err := Normalize("cask", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !item.SupportsArm64 || !item.SupportsX8664 {
		t.Fatal("both architectures must be supported")
	}
	var meta CaskPlatformMetadata
	if err := json.Unmarshal(item.Dependencies, &meta); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, platform := range meta.Platforms {
		arch, checksum := "arm64", "3b8da3886ddf5381eaf756bcc47b15bd2829082ef797b47394674c51a9a727ba"
		if platform.Arch == "x86_64" {
			arch, checksum = "x64", "996f7bdcc70d575503f1e59d141cc1332c98bd721317863deb2089085d873f4e"
		}
		if stringValue(platform.DownloadURL) != "https://github.com/stablyai/orca/releases/download/v1.4.244/orca-macos-"+arch+".dmg" || stringValue(platform.DownloadSHA256) != checksum || stringValue(platform.MinMacos) != "11" || platform.Version != "1.4.244" {
			t.Fatalf("unexpected platform: %+v", platform)
		}
		if jsonText(platform.Artifacts.Apps) != `["Orca.app"]` || jsonText(platform.Artifacts.Binaries) != `["orca"]` {
			t.Fatalf("artifacts: %+v", platform.Artifacts)
		}
		counts[platform.Arch]++
	}
	if counts["arm64"] == 0 || counts["arm64"] != counts["x86_64"] {
		t.Fatalf("platform counts: %v", counts)
	}
}

func TestCaskArchitectureSelectors(t *testing.T) {
	base := "cask \"demo\" do\n  version \"1\"\n  name \"Demo\"\n  app \"Demo.app\"\n"
	for _, tc := range []struct {
		name, body string
		line       int
	}{
		{"missing intel", `arch arm: "a"`, 5},
		{"duplicate key", `arch arm: "a", arm: "b"`, 5},
		{"unknown key", `arch arm: "a", linux: "b"`, 5},
		{"dynamic value", `arch arm: "a", intel: system("id")`, 5},
		{"empty value", `arch arm: "a", intel: ""`, 5},
		{"symbol value", `arch arm: :arm64, intel: :intel`, 5},
		{"duplicate stanza", "arch arm: \"a\", intel: \"b\"\narch arm: \"c\", intel: \"d\"", 6},
		{"missing declaration", `url "https://example.com/#{arch}.dmg"`, 5},
		{"arch method", "arch arm: \"a\", intel: \"b\"\nurl \"https://example.com/#{arch.upcase}.dmg\"", 6},
		{"bad intel checksum", `sha256 arm: :no_check, intel: "bad"`, 5},
		{"bad arm checksum", `sha256 arm: "bad", intel: :no_check`, 5},
		{"missing checksum", `sha256 arm: :no_check`, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCaskSource(base+tc.body+"\nurl \"https://example.com/demo.dmg\"\nend\n", CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
			var unsupported *UnsupportedCaskError
			if !errors.As(err, &unsupported) || unsupported.Line != tc.line {
				t.Fatalf("want rejection at line %d, got %v", tc.line, err)
			}
		})
	}
	// Checksums may select architectures without an arch interpolation stanza.
	raw, err := ParseCaskSource(base+"sha256 arm: :no_check, intel: \""+strings.Repeat("B", 64)+"\"\nurl \"https://example.com/universal.dmg\"\nend\n", CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
	if err != nil {
		t.Fatal(err)
	}
	item, err := Normalize("cask", raw)
	if err != nil {
		t.Fatal(err)
	}
	var meta CaskPlatformMetadata
	if err := json.Unmarshal(item.Dependencies, &meta); err != nil {
		t.Fatal(err)
	}
	for _, platform := range meta.Platforms {
		if platform.Arch == "arm64" && platform.DownloadSHA256 != nil || platform.Arch == "x86_64" && stringValue(platform.DownloadSHA256) != strings.Repeat("b", 64) {
			t.Fatalf("checksum: %+v", platform)
		}
	}
}

func TestRejectArchitectureDependentRequirements(t *testing.T) {
	base := "cask \"demo\" do\n  arch arm: \"arm64\", intel: \"x86_64\"\n  version \"1\"\n  sha256 :no_check\n  url \"https://example.com/#{arch}.dmg\"\n  app \"Demo.app\"\n  name \"Demo\"\n"
	for _, requirement := range []string{
		`depends_on arch: "#{arch}"`,
		`depends_on arch: ["#{ arch }"]`,
		"depends_on(\n    arch: \"#{arch}\"\n  )",
		`depends_on arch: "#{arch # comment
}"`,
	} {
		t.Run(requirement, func(t *testing.T) {
			_, err := ParseCaskSource(base+"  "+requirement+"\nend\n", CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
			var unsupported *UnsupportedCaskError
			line := 8
			if strings.HasPrefix(requirement, "depends_on(\n") {
				line++
			}
			if !errors.As(err, &unsupported) || unsupported.Line != line || !strings.Contains(unsupported.Reason, "arch interpolation in depends_on") {
				t.Fatalf("want architecture requirement rejection at line %d, got %v", line, err)
			}
		})
	}
	for _, arch := range []string{"arm64", "intel"} {
		raw, err := ParseCaskSource(base+"  depends_on arch: :"+arch+"\nend\n", CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
		if err != nil {
			t.Fatal(err)
		}
		item, err := Normalize("cask", raw)
		if err != nil {
			t.Fatal(err)
		}
		if item.SupportsArm64 != (arch == "arm64") || item.SupportsX8664 != (arch == "intel") {
			t.Fatalf("literal %s restriction was lost: arm64=%v x86_64=%v", arch, item.SupportsArm64, item.SupportsX8664)
		}
	}
}
