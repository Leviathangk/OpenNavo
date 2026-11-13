package homebrew

import "testing"

// Importing a whole tap must read file paths exactly the way tap_casks.source_path accepts them.
func TestTapCaskTokenFromPath(t *testing.T) {
	valid := map[string]string{
		"Casks/ghostty.rb":        "ghostty",
		"Casks/g/ghostty.rb":      "ghostty",
		"Casks/o/orca.rb":         "orca",
		"Casks/x/xcode-cli.rb":    "xcode-cli",
		"Casks/a/1password.rb":    "1password",
		"Casks/s/swiftvm@beta.rb": "swiftvm@beta",
		"Casks/p/py+extra.rb":     "py+extra",
		"Casks/u/ui_test.rb":      "ui_test",
	}
	for path, want := range valid {
		token, ok := TapCaskTokenFromPath(path)
		if !ok || token != want {
			t.Errorf("TapCaskTokenFromPath(%q) = %q, %v; want %q, true", path, token, ok, want)
		}
	}
	invalid := []string{
		"",
		"Casks/.rb",
		"Casks/g/.rb",
		"Casks/g/Ghostty.rb",   // tokens are lowercase
		"Casks/g/ghostty.sh",   // not a cask file
		"Casks/g/ghostty",      // no extension
		"Casks/a/b/ghostty.rb", // deeper than one shard directory
		"Formula/g/ghostty.rb", // casks live under Casks/
		"CasksG/ghostty.rb",
		"Homebrew/homebrew-cask/Casks/g/ghostty.rb",
		"Casks//ghostty.rb",
		"Casks/g/../ghostty.rb",
	}
	for _, path := range invalid {
		if token, ok := TapCaskTokenFromPath(path); ok {
			t.Errorf("TapCaskTokenFromPath(%q) = %q, true; want rejected", path, token)
		}
	}
}
