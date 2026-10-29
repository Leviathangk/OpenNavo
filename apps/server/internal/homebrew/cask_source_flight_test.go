package homebrew

import (
	"embed"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

//go:embed testdata/casks/*
var caskSourceFixtures embed.FS

func caskFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := caskSourceFixtures.ReadFile("testdata/casks/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseCaskSourceFixtures(t *testing.T) {
	for _, tc := range []struct {
		ref    CaskSource
		source string
	}{
		{CaskSource{Tap: "swiftvm-app/tap", Token: "swiftvm", Path: "Casks/swiftvm.rb", GitHead: "99e3218"}, swiftvmCask},
		{CaskSource{Tap: "homebrew/cask", Token: "rectangle", Path: "Casks/r/rectangle.rb", GitHead: "62514aa815052bc35a8fd17d2e7fadff8784c85a"}, ""},
		{CaskSource{Tap: "noahelhadedy/tap", Token: "double-shot", Path: "Casks/double-shot.rb", GitHead: "68fb39413dbaa57e20ffa4c3b977f7ac76b9f53f"}, ""},
	} {
		t.Run(tc.ref.Token, func(t *testing.T) {
			source := tc.source
			if source == "" {
				source = caskFixture(t, tc.ref.Token+".rb")
			}
			raw, err := ParseCaskSource(source, tc.ref, sourceDay)
			if err != nil {
				t.Fatal(err)
			}
			want := json.RawMessage(caskFixture(t, tc.ref.Token+".json"))
			if !json.Valid(want) || canonical(raw) != canonical(want) {
				t.Fatalf("cask JSON changed:\ngot  %s\nwant %s", raw, want)
			}
			if diffs := compareCasks(t, raw, want); len(diffs) > 0 {
				t.Fatalf("normalized package changed: %v", diffs)
			}
		})
	}
}

func TestParseCaskSourceDoubleShot(t *testing.T) {
	source := caskFixture(t, "double-shot.rb")
	raw, err := ParseCaskSource(source, CaskSource{Tap: "noahelhadedy/tap", Token: "double-shot", Path: "Casks/double-shot.rb"}, sourceDay)
	if err != nil {
		t.Fatal(err)
	}
	item, err := Normalize("cask", raw)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "Double Shot" || item.Version != "1.0.6" || item.MinMacos != "14" || !item.SupportsArm64 || !item.SupportsX8664 ||
		item.DownloadURL != "https://github.com/noahelhadedy/double-shot/releases/download/v1.0.6/DoubleShot-1.0.6.zip" {
		t.Fatalf("unexpected package: %+v", item)
	}
	if got := CaskSourceVersion(source); got != item.Version {
		t.Fatalf("version reader = %q, package version = %q", got, item.Version)
	}
	var artifacts CaskArtifacts
	if err := json.Unmarshal(item.Artifacts, &artifacts); err != nil {
		t.Fatal(err)
	}
	if jsonText(artifacts.Apps) != `["Double Shot.app"]` {
		t.Fatalf("apps = %v", artifacts.Apps)
	}
}

const flightGuardBase = `cask "demo" do
  version "1.0"
  sha256 :no_check
  url "https://example.com/demo-#{version}.zip"
  name "Demo"
  app "Demo.app"
`

func TestParseCaskSourceFlightGuards(t *testing.T) {
	for _, flight := range []string{"preflight", "postflight", "uninstall_preflight", "uninstall_postflight"} {
		t.Run(flight, func(t *testing.T) {
			modern := flight + "_steps"
			// Supported nested blocks and strings containing end/else must not hide
			// the metadata after the compatibility guard.
			hook := modern + ` do
    ["end"].each do
      unless File.exist?(app_path)
        run "/bin/echo", args: ["end", app_path]
      else
        run "/bin/echo", args: ["else", app_path]
      end
    end
  end
`
			guard := "  if respond_to?(:" + modern + ")\n" + `    app_path = "#{appdir}/Demo.app"
    ` + hook + `  else
    legacy_path = "#{staged_path}/Demo.app"
    ` + flight + ` do
      if File.exist?(legacy_path)
        system_command "/bin/echo", args: ["else", legacy_path]
      end
    end
  end
`
			out := parseSource(t, flightGuardBase+guard+"  desc \"After guard\"\nend\n", "demo")
			literalHook := strings.Replace(hook, "app_path", `"#{appdir}/Demo.app"`, 1)
			want := parseSource(t, flightGuardBase+literalHook+"  desc \"After guard\"\nend\n", "demo")
			if jsonText(out) != jsonText(want) {
				t.Fatalf("guard changed catalog metadata:\ngot %s\nwant %s", jsonText(out), jsonText(want))
			}
		})
	}
}

func TestParseCaskSourceRejectsUnsafeFlightGuards(t *testing.T) {
	const guard = `  if respond_to?(:postflight_steps)
    app_path = "#{appdir}/Demo.app"
    postflight_steps do
      run "/bin/echo", args: [app_path]
    end
  else
    postflight do
      system_command "/bin/echo"
    end
  end
`
	for name, body := range map[string]string{
		"other condition":        strings.Replace(guard, "respond_to?(:postflight_steps)", "MacOS.version >= :sonoma", 1),
		"unknown feature":        strings.Replace(guard, ":postflight_steps", ":future_steps", 1),
		"metadata feature":       strings.Replace(guard, ":postflight_steps", ":version", 1),
		"string feature":         strings.Replace(guard, ":postflight_steps", `"postflight_steps"`, 1),
		"extra condition":        strings.Replace(guard, "respond_to?(:postflight_steps)", "respond_to?(:postflight_steps) && true", 1),
		"extra argument":         strings.Replace(guard, "respond_to?(:postflight_steps)", "respond_to?(:postflight_steps, true)", 1),
		"receiver":               strings.Replace(guard, "respond_to?", "self.respond_to?", 1),
		"unless":                 strings.Replace(guard, "if respond_to?", "unless respond_to?", 1),
		"new branch metadata":    strings.Replace(guard, "    app_path =", "    desc \"Changed\"\n    app_path =", 1),
		"legacy branch metadata": strings.Replace(guard, "  else\n", "  else\n    version \"2.0\"\n", 1),
		"new branch app":         strings.Replace(guard, "    app_path =", "    app \"Other.app\"\n    app_path =", 1),
		"legacy branch app":      strings.Replace(guard, "  else\n", "  else\n    app \"Other.app\"\n", 1),
		"wrong modern hook":      strings.Replace(guard, "    postflight_steps do", "    preflight_steps do", 1),
		"wrong legacy hook":      strings.Replace(guard, "    postflight do", "    preflight do", 1),
		"missing modern hook":    strings.Replace(guard, "    postflight_steps do\n      run \"/bin/echo\", args: [app_path]\n    end\n", "", 1),
		"missing legacy hook":    strings.Replace(guard, "    postflight do\n      system_command \"/bin/echo\"\n    end\n", "", 1),
		"missing else":           "  if respond_to?(:postflight_steps)\n    postflight_steps do\n    end\n  end\n",
		"duplicate hook":         strings.Replace(guard, "  else\n", "    postflight_steps do\n    end\n  else\n", 1),
		"hook arguments":         strings.Replace(guard, "postflight_steps do", "postflight_steps :extra do", 1),
		"block parameters":       strings.Replace(guard, "postflight_steps do", "postflight_steps do |helper|", 1),
		"dynamic helper":         strings.Replace(guard, `"#{appdir}/Demo.app"`, `File.read("path")`, 1),
		"dynamic interpolation":  strings.Replace(guard, "#{appdir}", `#{system("echo")}`, 1),
		"non-string helper":      strings.Replace(guard, `"#{appdir}/Demo.app"`, "nil", 1),
		"global helper":          strings.Replace(guard, "app_path =", "$app_path =", 1),
		"instance helper":        strings.Replace(guard, "app_path =", "@app_path =", 1),
		"compound assignment":    strings.Replace(guard, "app_path =", "app_path ||=", 1),
		"helper method call":     strings.Replace(guard, `"#{appdir}/Demo.app"`, `"#{appdir}/Demo.app".freeze`, 1),
		"nested guard":           strings.Replace(guard, "  else\n", "  else\n    "+guard, 1),
		"elsif":                  strings.Replace(guard, "  else\n", "  elsif true\n", 1),
		"missing guard end":      strings.TrimSuffix(guard, "  end\n"),
		"trailing modifier":      strings.TrimSuffix(guard, "\n") + " if false\n",
		"helper escapes guard":   guard + "  desc \"#{app_path}\"\n",
		"outside helper":         "  app_path = \"Demo.app\"\n" + guard,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCaskSource(flightGuardBase+body+"end\n", CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
			var unsupported *UnsupportedCaskError
			if !errors.As(err, &unsupported) || unsupported.Line == 0 {
				t.Fatalf("expected a line-specific unsupported cask error, got %v", err)
			}
		})
	}
	// Assignments in either branch must not shadow the roots the static interpolator resolves.
	for _, root := range []string{"version", "token", "appdir", "staged_path", "caskroom_path"} {
		for _, branch := range []string{"modern", "legacy"} {
			t.Run("shadow "+root+" in "+branch, func(t *testing.T) {
				body := strings.Replace(guard, "app_path =", root+" =", 1)
				if branch == "legacy" {
					body = strings.Replace(guard, "  else\n", "  else\n    "+root+" = \"shadow\"\n", 1)
				}
				_, err := ParseCaskSource(flightGuardBase+body+"end\n", CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
				if err == nil {
					t.Fatal("accepted an assignment that can change catalog interpolation")
				}
			})
		}
	}
}

func TestParseCaskSourceRejectsAmbiguousFlightBlocks(t *testing.T) {
	for _, branch := range []string{"modern", "legacy"} {
		for _, tc := range []struct {
			name string
			code string
			line int
		}{
			{"endless def", "def helper = 1", 1},
			{"method definition", "def helper\n        1\n      end", 1},
			{"singleton def", "def self.helper = 1", 1},
			{"inline if", "x = 1 + if true\n        1\n      end", 1},
			{"inline unless", "x = 1 + unless false\n        1\n      end", 1},
			{"inline case", "x = case 1\n      when 1\n        1\n      end", 1},
			{"inline while", "x = 1 + while false\n        1\n      end", 1},
			{"inline until", "x = 1 + until true\n        1\n      end", 1},
			{"continued inline if", "x = 1 +\n      if true\n        1\n      end", 2},
			{"parenthesized inline if", "x = (\n      if true\n        1\n      end\n      )", 2},
			{"begin expression", "x = begin\n        1\n      end", 1},
			{"loop with do", "while false do\n        run \"/bin/echo\"\n      end", 1},
		} {
			t.Run(branch+"/"+tc.name, func(t *testing.T) {
				prefix := flightGuardBase + "  if respond_to?(:postflight_steps)\n"
				flight := "postflight_steps"
				suffix := "  else\n    postflight do\n    end\n  end\nend\n"
				if branch == "legacy" {
					prefix += "    postflight_steps do\n    end\n  else\n"
					flight = "postflight"
					suffix = "  end\nend\n"
				}
				prefix += "    " + flight + " do\n"
				source := prefix + "      " + tc.code + "\n    end\n" + suffix
				_, err := ParseCaskSource(source, CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
				var unsupported *UnsupportedCaskError
				wantLine := strings.Count(prefix, "\n") + tc.line
				if !errors.As(err, &unsupported) || unsupported.Line != wantLine {
					t.Fatalf("expected UnsupportedCaskError at line %d, got %v", wantLine, err)
				}
			})
		}
	}
}

func TestParseCaskSourceRejectsFlightBoundarySmuggling(t *testing.T) {
	for _, branch := range []string{"modern", "legacy"} {
		t.Run(branch, func(t *testing.T) {
			prefix := flightGuardBase + "  if respond_to?(:postflight_steps)\n"
			flight := "postflight_steps"
			suffix := "  else\n    postflight do\n    end\n  end\nend\n"
			if branch == "legacy" {
				prefix += "    postflight_steps do\n    end\n  else\n"
				flight = "postflight"
				suffix = "  end\nend\n"
			}
			// An endless def adds a phantom level to the permissive skipper; an inline if
			// cancels it. Together they used to hide a second hook and catalog metadata.
			prefix += "    " + flight + " do\n"
			source := prefix + `      def helper = 1
    end
    depends_on arch: :arm64
    ` + flight + ` do
      x = 1 + if true
        1
      end
    end
` + suffix
			_, err := ParseCaskSource(source, CaskSource{Tap: "example/tap", Token: "demo"}, sourceDay)
			var unsupported *UnsupportedCaskError
			wantLine := strings.Count(prefix, "\n") + 1
			if !errors.As(err, &unsupported) || unsupported.Line != wantLine {
				t.Fatalf("expected UnsupportedCaskError at line %d, got %v", wantLine, err)
			}
		})
	}
}
