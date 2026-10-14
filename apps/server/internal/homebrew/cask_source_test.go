package homebrew

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var sourceDay = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

const swiftvmCask = `cask "swiftvm" do
  version "1.3.0"
  sha256 "0f523cf1fb6ee2f1b16a4eb24789c54032fc91c62e3981595637376f142fcaba"

  url "https://github.com/swiftvm-app/swiftvm/releases/download/v#{version}/SwiftVM-#{version}.dmg"
  name "SwiftVM"
  desc "Run macOS and Linux virtual machines"
  homepage "https://github.com/swiftvm-app/swiftvm"

  livecheck do
    url :url
    strategy :github_latest
  end

  auto_updates true
  depends_on arch: :arm64

  app "SwiftVM.app"
  binary "#{appdir}/SwiftVM.app/Contents/Helpers/swiftvm"

  # The virtual machines, in ~/Library/Application Support/SwiftVM, are left alone on purpose.
  zap trash: [
    "~/Library/Caches/SwiftVM",
    "~/Library/Preferences/com.swiftvm.app.plist",
  ]
end
`

func parseSource(t *testing.T, source, token string) map[string]any {
	t.Helper()
	raw, err := ParseCaskSource(source, CaskSource{Tap: "example/tap", Token: token, Path: "Casks/" + token + ".rb", GitHead: "abc123"}, sourceDay)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonText(value any) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return strings.TrimSuffix(out.String(), "\n")
}

func TestParseCaskSourceProducesAPIShape(t *testing.T) {
	raw, err := ParseCaskSource(swiftvmCask, CaskSource{Tap: "swiftvm-app/tap", Token: "swiftvm", Path: "Casks/swiftvm.rb", GitHead: "99e3218"}, sourceDay)
	if err != nil {
		t.Fatal(err)
	}
	item, err := Normalize("cask", raw)
	if err != nil {
		t.Fatal(err)
	}
	if item.Token != "swiftvm" || item.FullToken != "swiftvm-app/tap/swiftvm" || item.Tap != "swiftvm-app/tap" || item.Name != "SwiftVM" ||
		item.Version != "1.3.0" || item.DownloadURL != "https://github.com/swiftvm-app/swiftvm/releases/download/v1.3.0/SwiftVM-1.3.0.dmg" ||
		item.Desc != "Run macOS and Linux virtual machines" || item.Homepage != "https://github.com/swiftvm-app/swiftvm" ||
		!item.AutoUpdates || !item.SupportsArm64 || item.SupportsX8664 || item.RubySourcePath != "Casks/swiftvm.rb" || item.TapGitHead != "99e3218" {
		t.Fatalf("unexpected package: %+v", item)
	}
	var artifacts CaskArtifacts
	if err := json.Unmarshal(item.Artifacts, &artifacts); err != nil {
		t.Fatal(err)
	}
	if jsonText(artifacts.Apps) != `["SwiftVM.app"]` || jsonText(artifacts.Binaries) != `["swiftvm"]` {
		t.Fatalf("artifacts = %s", item.Artifacts)
	}
	var metadata CaskPlatformMetadata
	if err := json.Unmarshal(item.Dependencies, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.Platforms) != len(apiMacosTags) {
		t.Fatalf("platforms = %+v", metadata.Platforms)
	}
	for _, platform := range metadata.Platforms {
		if platform.Arch != "arm64" {
			t.Fatalf("arm64-only cask lists %s", platform.Tag)
		}
	}
	out := parseSource(t, swiftvmCask, "swiftvm")
	if got := jsonText(out["artifacts"]); got != `[{"app":["SwiftVM.app"],"target":"/Applications/SwiftVM.app"},`+
		`{"binary":["$APPDIR/SwiftVM.app/Contents/Helpers/swiftvm"],"target":"$HOMEBREW_PREFIX/bin/swiftvm"},`+
		`{"zap":[{"trash":["~/Library/Caches/SwiftVM","~/Library/Preferences/com.swiftvm.app.plist"]}]}]` {
		t.Fatalf("artifacts JSON = %s", got)
	}
}

func TestParseCaskSourceVersionHelpers(t *testing.T) {
	for _, tc := range []struct{ version, expression, want string }{
		{"1.2.3", "version.major", "1"},
		{"1.2.3", "version.minor", "2"},
		{"1.2.3", "version.patch", "3"},
		{"1.2", "version.patch", ""},
		{"1.2", "version.major_minor_patch", "1.2"},
		{"1.2.3,456", "version.csv.first", "1.2.3"},
		{"1.2.3,456", "version.csv.second", "456"},
		{"1.2.3,456", "version.csv.first.major", "1"},
		{"1.2.3,456", "version.csv[1]", "456"},
		{"1.2.3,456", "version.csv.third", ""},
		{"1.2.3,456", "version.before_comma", "1.2.3"},
		{"1.2.3,456", "version.after_comma", "456"},
		{"1.2.3", "version.no_dots", "123"},
		{"1.2-3_4", "version.no_dividers", "1234"},
		{"1.2.3", "version.dots_to_underscores", "1_2_3"},
		{"1.2.3", "version.dots_to_hyphens", "1-2-3"},
		{"1-2", "version.hyphens_to_dots", "1.2"},
		{"2024-01-02", "version.major", "2024-01-02"},
		{"1.2.3-beta", `version.chomp("-beta")`, "1.2.3"},
		{"latest", "version.major", "latest"},
	} {
		source := "cask \"demo\" do\n  version \"" + tc.version + "\"\n  sha256 :no_check\n  url \"https://example.com/#{" + tc.expression + "}\"\n  name \"Demo\"\n  app \"Demo.app\"\nend\n"
		if tc.version == "latest" {
			source = strings.Replace(source, `version "latest"`, "version :latest", 1)
		}
		out := parseSource(t, source, "demo")
		if got := out["url"]; got != "https://example.com/"+tc.want {
			t.Errorf("%s of %s = %v, want %s", tc.expression, tc.version, got, tc.want)
		}
	}
}

func TestParseCaskSourceSkipsRuntimeBlocks(t *testing.T) {
	source := `# typed: false
cask "demo" do
  version "2.0"
  sha256 "` + strings.Repeat("a", 64) + `"

  url "https://example.com/demo-#{version}.zip",
      verified: "example.com/"
  name "Demo", "Demo App"
  homepage "https://example.com"

  livecheck do
    url "https://example.com/releases"
    regex(/href=.*?demo[._-]v?(\d+(?:\.\d+)+)\.zip/i)
    strategy :page_match do |page, regex|
      next if page.blank?

      page.scan(regex).map { |match| match[0] if match[0] != "end" }
    end
  end

  depends_on macos: ">= :ventura"

  app "Demo.app", target: "Demo Renamed.app"

  postflight do
    if File.exist?("#{staged_path}/x")
      system_command "/bin/echo", args: ["do", "end"]
    end
    while false do end
  end

  uninstall quit: "com.example.demo",
            delete: "#{appdir}/Demo Renamed.app"

  caveats <<~EOS
    Thanks for installing #{token} #{version.major}.
      Indented line.
  EOS

  caveats do
    requires_rosetta
  end
end
`
	out := parseSource(t, source, "demo")
	if got := jsonText(out["name"]); got != `["Demo","Demo App"]` {
		t.Fatalf("names = %s", got)
	}
	if got := out["caveats"]; got != "Thanks for installing demo 2.\n  Indented line.\n" {
		t.Fatalf("caveats = %q", got)
	}
	if out["caveats_rosetta"] != true {
		t.Fatal("requires_rosetta in a caveats block should be kept")
	}
	if got := jsonText(out["depends_on"]); got != `{"macos":{">=":["13"]}}` {
		t.Fatalf("depends_on = %s", got)
	}
	if got := jsonText(out["supported_platforms"]); got != `["golden_gate","arm64_golden_gate","tahoe","arm64_tahoe","sequoia","arm64_sequoia","sonoma","arm64_sonoma","ventura","arm64_ventura"]` {
		t.Fatalf("platforms = %s", got)
	}
	if got := jsonText(out["artifacts"]); got != `[{"uninstall":[{"delete":"$APPDIR/Demo Renamed.app","quit":"com.example.demo"}]},`+
		`{"app":["Demo.app",{"target":"Demo Renamed.app"}],"target":"/Applications/Demo Renamed.app"},{"postflight":null}]` {
		t.Fatalf("artifacts = %s", got)
	}
	if got := jsonText(out["url_specs"]); got != `{"verified":"example.com/"}` {
		t.Fatalf("url_specs = %s", got)
	}
}

func TestParseCaskSourceDependsOnAndLifecycle(t *testing.T) {
	base := "cask \"demo\" do\n  version \"1\"\n  sha256 :no_check\n  url \"https://example.com/demo.dmg\"\n  name \"Demo\"\n  app \"Demo.app\"\n"
	out := parseSource(t, base+"  depends_on :macos\n  depends_on arch: [:intel], cask: \"helper\"\n  depends_on formula: [\"jq\", \"yq\"]\nend\n", "demo")
	if got := jsonText(out["depends_on"]); got != `{"arch":[{"bits":64,"type":"intel"}],"cask":["helper"],"formula":["jq","yq"],"macos":{}}` {
		t.Fatalf("depends_on = %s", got)
	}
	if got := jsonText(out["supported_platforms"]); strings.Contains(got, "arm64") || !strings.Contains(got, `"big_sur"`) {
		t.Fatalf("intel-only platforms = %s", got)
	}
	out = parseSource(t, base+"  depends_on maximum_macos: :sonoma\nend\n", "demo")
	if got := jsonText(out["supported_platforms"]); got != `["sonoma","arm64_sonoma","ventura","arm64_ventura","monterey","arm64_monterey","big_sur","arm64_big_sur"]` {
		t.Fatalf("maximum_macos platforms = %s", got)
	}
	out = parseSource(t, base+"  deprecate! date: \"2026-01-01\", because: :unmaintained, replacement_cask: \"demo2\"\nend\n", "demo")
	if out["deprecated"] != true || out["deprecation_reason"] != "unmaintained" || out["deprecation_replacement_cask"] != "demo2" {
		t.Fatalf("deprecation = %v", out)
	}
	out = parseSource(t, base+"  disable! date: \"2027-01-01\", because: :discontinued\nend\n", "demo")
	if out["disabled"] != false || out["deprecated"] != true || out["disable_date"] != "2027-01-01" {
		t.Fatalf("future disable should deprecate: %v", out)
	}
	out = parseSource(t, base+"  disable! date: \"2026-10-14\", because: :unsigned\nend\n", "demo")
	if out["disabled"] != true || out["disable_reason"] != "fails_gatekeeper_check" {
		t.Fatalf("disable = %v", out)
	}
}

func TestParseCaskSourceRejectsWhatItCannotEvaluate(t *testing.T) {
	base := "cask \"demo\" do\n  version \"1.0\"\n  sha256 :no_check\n  url \"https://example.com/demo.dmg\"\n  name \"Demo\"\n"
	for name, tc := range map[string]struct{ body, reason string }{
		"arch":           {`  arch arm: "arm64", intel: "x64"`, "`arch`"},
		"on_arm":         {"  on_arm do\n    app \"Demo.app\"\n  end", "`on_arm` blocks"},
		"on_macos":       {"  on_sonoma :or_newer do\n    app \"Demo.app\"\n  end", "`on_sonoma` blocks"},
		"language":       {"  language \"en\", default: true do\n    \"en\"\n  end", "`language` blocks"},
		"local variable": {`  folder = "demo"`, "local variables"},
		"conditional":    {"  if MacOS.version > :big_sur\n    app \"Demo.app\"\n  end", "conditionals"},
		"unknown":        {`  something_new "x"`, "`something_new`"},
		"interpolation":  {`  app "Demo #{name}.app"`, "unsupported interpolation"},
		"expression":     {`  app "Demo.app", target: staged_path`, "only literal values"},
		"arch sha256":    {`  sha256 arm: "x", intel: "y"`, "declared more than once"},
		"url block":      {"  livecheck\n", "requires a block"},
		"caveats code":   {`  caveats File.read("x")`, "only literal values"},
	} {
		_, err := ParseCaskSource(base+tc.body+"\nend\n", CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
		var unsupported *UnsupportedCaskError
		if !errors.As(err, &unsupported) || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.reason)
		}
	}
	if _, err := ParseCaskSource(swiftvmCask, CaskSource{Tap: "example/tap", Token: "other"}, sourceDay); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("token mismatch: %v", err)
	}
	for name, source := range map[string]string{
		"no version": "cask \"demo\" do\n  url \"https://example.com/x\"\n  name \"Demo\"\nend\n",
		"no url":     "cask \"demo\" do\n  version \"1\"\n  name \"Demo\"\nend\n",
		"unclosed":   "cask \"demo\" do\n  version \"1\"\n  livecheck do\n    url :url\n",
		"trailing":   swiftvmCask + "system \"rm -rf /\"\n",
		"two casks":  swiftvmCask + swiftvmCask,
		"deep":       "cask \"demo\" do\n  version \"1\"\n  url \"https://example.com/x\"\n  name \"Demo\"\n  zap trash: " + strings.Repeat("[", 100000) + "\nend\n",
	} {
		if _, err := ParseCaskSource(source, CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestInterpolationRejectsTrailingStatements(t *testing.T) {
	for _, expression := range []string{
		"version; \"2.0\"",
		"version\n\"2.0\"",
		"version;\n\n\"2.0\"",
		"version.major; token",
	} {
		t.Run(expression, func(t *testing.T) {
			source := "cask \"demo\" do\n version \"1.0\"\n sha256 :no_check\n url \"https://example.com/#{" + expression + "}\"\n app \"Demo.app\"\nend\n"
			_, err := ParseCaskSource(source, CaskSource{Tap: "example/tap", Token: "demo", Path: "Casks/demo.rb"}, sourceDay)
			var unsupported *UnsupportedCaskError
			if !errors.As(err, &unsupported) || !strings.Contains(err.Error(), "unsupported interpolation") {
				t.Fatalf("expected unsupported interpolation, got %v", err)
			}
		})
	}
}

func TestCaskBackticksAreDynamicNotStringLiterals(t *testing.T) {
	for _, stanza := range []string{
		"app `printf Renamed.app`",
		"url `printf https://example.com/download.dmg`",
		"app \"Source.app\", target: `printf Renamed.app`",
		"app \"Source.app\" `printf Renamed.app`",
	} {
		t.Run(stanza, func(t *testing.T) {
			source := "cask \"demo\" do\n version \"1.0\"\n sha256 :no_check\n url \"https://example.com/app.dmg\"\n " + stanza + "\nend\n"
			if strings.HasPrefix(stanza, "url ") {
				source = strings.Replace(source, " url \"https://example.com/app.dmg\"\n", "", 1)
			}
			_, err := ParseCaskSource(source, CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
			var unsupported *UnsupportedCaskError
			if !errors.As(err, &unsupported) || (!strings.Contains(err.Error(), "shell command") && !strings.Contains(err.Error(), "unsupported expression after app")) {
				t.Fatalf("expected unsupported dynamic command, got %v", err)
			}
		})
	}
	source := strings.Replace(swiftvmCask, "  app \"SwiftVM.app\"", "  preflight do\n    `printf runtime`\n  end\n  app \"SwiftVM.app\"", 1)
	parseSource(t, source, "swiftvm")
}
