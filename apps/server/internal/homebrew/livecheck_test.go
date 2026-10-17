package homebrew

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSparkleLivecheckIsIndependentAndNeverEvaluatesRuby(t *testing.T) {
	valid := `cask "fixture" do
 version "1"
 url "https://example.org/app.zip"
 name "Fixture"
 livecheck do
  url "https://example.org/appcast.xml"
  strategy :sparkle do |item|
   item.short_version
  end
 end
end`
	got, err := SparkleLivecheck(valid)
	require.NoError(t, err)
	require.Equal(t, "https://example.org/appcast.xml", got)
	for _, body := range []string{
		`url "http://example.org/feed"` + "\nstrategy :sparkle",
		`url "https://example.org/#{system('touch /tmp/never')}"` + "\nstrategy :sparkle",
		"url `curl example.org`\nstrategy :sparkle",
		"url :url\nstrategy :sparkle",
		`url "https://example.org/feed"` + "\nstrategy :github_latest",
		`skip "discontinued"`,
		`url "https://example.org/feed"` + "\nurl \"https://elsewhere.org/feed\"\nstrategy :sparkle",
	} {
		got, _ := SparkleLivecheck("cask \"fixture\" do\nlivecheck do\n" + body + "\nend\nend")
		require.Empty(t, got, body)
	}
	got, _ = SparkleLivecheck("cask \"fixture\" do\non_arm do\nlivecheck do\nurl \"https://example.org/feed\"\nstrategy :sparkle\nend\nend\nend")
	require.Empty(t, got)
}

func TestElectronAndGithubLivecheckStayStatic(t *testing.T) {
	for _, tc := range []struct{ source, url, strategy string }{
		{"electron", "https://example.org/latest-mac.yml", "electron_builder"},
		{"github", "https://github.com/example/app", "github_latest"},
	} {
		rule, err := ParseUpdateLivecheck("cask \"test\" do\nlivecheck do\nurl \"" + tc.url + "\"\nstrategy :" + tc.strategy + "\nend\nend")
		require.NoError(t, err)
		require.Equal(t, tc.source, rule.Source)
	}
	rule, err := ParseUpdateLivecheck("cask \"test\" do\nurl \"https://github.com/example/app/releases/download/v#{version}/App.zip\"\nlivecheck do\nurl :url\nstrategy :github_latest\nend\nend")
	require.NoError(t, err)
	require.Equal(t, "example/app", rule.GithubRepo)
	for _, ruby := range []string{"on_arm do\nlivecheck do\nurl \"https://example.org/latest-mac.yml\"\nstrategy :electron_builder\nend\nend", "livecheck do\nurl `touch /tmp/never`\nstrategy :github_latest\nend"} {
		rule, _ := ParseUpdateLivecheck("cask \"test\" do\n" + ruby + "\nend")
		require.Empty(t, rule.Source)
	}
}
