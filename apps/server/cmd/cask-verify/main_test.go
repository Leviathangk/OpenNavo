package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every rule a pull request can fail must be reported with the file and, where the parser knows it, a line.
func TestVerify(t *testing.T) {
	path := func(name string) string { return filepath.Join("testdata", "cask", name) }
	valid := verify(path("valid.rb"), "opennavo/tap")
	require.True(t, valid.OK, "problems: %+v", valid.Problems)
	require.Equal(t, "valid", valid.Token)
	require.Equal(t, "1.2.0", valid.Version)
	require.Equal(t, "https://example.com/valid-1.2.0.dmg", valid.URL)
	require.Len(t, valid.SHA256, 64)
	require.Equal(t, []string{"app"}, valid.Artifacts)

	cases := []struct {
		name     string
		file     string
		wantLine int
		want     string
	}{
		{name: "unsupported stanza", file: "unsupported.rb", wantLine: 10, want: "`language` blocks are not supported"},
		{name: "missing checksum", file: "no-sha256.rb", want: "sha256 is required"},
		{name: "plain http", file: "http-url.rb", want: "url must use https"},
		{name: "unpinned version", file: "latest.rb", want: "not :latest"},
		{name: "installs nothing", file: "no-artifacts.rb", want: "installs nothing"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := verify(path(testCase.file), "opennavo/tap")
			require.False(t, result.OK)
			require.NotEmpty(t, result.Problems)
			message := ""
			for _, issue := range result.Problems {
				message += issue.Message
				if testCase.wantLine > 0 && issue.Line != testCase.wantLine {
					t.Fatalf("expected line %d, got %d (%s)", testCase.wantLine, issue.Line, issue.Message)
				}
			}
			require.Contains(t, message, testCase.want)
		})
	}
}

// The token comes from the file name, and a repository path must use one of the two supported layouts.
func TestVerifyTokenAndLayout(t *testing.T) {
	valid := filepath.Join("testdata", "cask", "valid.rb")
	token, layout := caskToken(valid)
	require.Equal(t, "valid", token)
	require.Empty(t, layout)

	token, layout = caskToken("Casks/v/valid.rb")
	require.Equal(t, "valid", token)
	require.Equal(t, "Casks/v/valid.rb", layout)

	token, layout = caskToken("Casks/valid.rb")
	require.Equal(t, "valid", token)
	require.Equal(t, "Casks/valid.rb", layout)

	token, layout = caskToken("Casks/v/a/valid.rb")
	require.Equal(t, "valid", token)
	require.Equal(t, "Casks/v/a/valid.rb", layout)
	require.NotEqual(t, "Casks/v/valid.rb", layout, "a nested path must not be normalized into a valid layout")

	_, layout = caskToken("valid.rb")
	require.Empty(t, layout)

	// A file whose name is not a token cannot be verified: the cask header would not match it.
	require.Empty(t, func() string { token, _ := caskToken("Casks/v/Valid App.rb"); return token }())

	// A tap that the sync would refuse is reported before the file is read.
	result := verify(valid, "homebrew/cask")
	require.False(t, result.OK)
	require.Contains(t, result.Problems[0].Message, "not usable")
}
