package homebrew

import (
	"strings"
	"testing"
)

func TestCaskSourceVersion(t *testing.T) {
	perArch := `cask "app" do
  arch arm: "arm64", intel: "x64"

  on_arm do
    version "2.0"
  end
  on_intel do
    version "1.9"
  end
end
`
	for name, test := range map[string]struct{ source, want string }{
		"full cask":            {swiftvmCask, "1.3.0"},
		"builds":               {"cask \"app\" do\n  version \"1.2,345\"\nend\n", "1.2,345"},
		"single quotes":        {"cask 'app' do\n  version '0.9-beta.1'\nend\n", "0.9-beta.1"},
		"trailing comment":     {"cask \"app\" do\n  version \"3.1\" # pinned\nend\n", "3.1"},
		"windows line endings": {"cask \"app\" do\r\n  version \"4.0\"\r\nend\r\n", "4.0"},
		"latest":               {"cask \"app\" do\n  version :latest\nend\n", "latest"},
		"arch blocks":          {strings.Replace(swiftvmCask, "  depends_on arch: :arm64\n", "  arch arm: \"arm64\", intel: \"x64\"\n", 1), "1.3.0"},
		"per-architecture":     {perArch, ""},
		"two versions":         {"cask \"app\" do\n  version \"1\"\n  version \"2\"\nend\n", ""},
		"interpolation":        {"cask \"app\" do\n  version \"#{major}.1\"\nend\n", ""},
		"no version":           {"cask \"app\" do\n  url \"https://example.com/app.dmg\"\nend\n", ""},
	} {
		if got := CaskSourceVersion(test.source); got != test.want {
			t.Errorf("%s: CaskSourceVersion() = %q, want %q", name, got, test.want)
		}
	}
}
