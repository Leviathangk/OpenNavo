package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLinkGithubFilterValidation(t *testing.T) {
	p := func(s string) *string { return &s }
	for _, tc := range []struct {
		name string
		edit func(*LinkInput)
		want string
	}{
		{"default", func(*LinkInput) {}, ""},
		{"prefix and asset", func(v *LinkInput) { v.GithubTagPrefix = p("rapid-mac-v"); v.GithubAssetPattern = p("*.dmg") }, ""},
		{"regex and beta", func(v *LinkInput) { v.GithubTagRegex = p(`^v1\.0\.0-beta\.[0-9]+$`); v.IncludePrereleases = true }, ""},
		{"RE2 nested repetition", func(v *LinkInput) { v.GithubTagRegex = p(`(a+)+$`) }, ""},
		{"prefix limit", func(v *LinkInput) { v.GithubTagPrefix = p(strings.Repeat("a", 256)) }, ""},
		{"regex limit", func(v *LinkInput) { v.GithubTagRegex = p(strings.Repeat("a", 1024)) }, ""},
		{"glob limit", func(v *LinkInput) { v.GithubAssetPattern = p(strings.Repeat("a", 512)) }, ""},
		{"long prefix", func(v *LinkInput) { v.GithubTagPrefix = p(strings.Repeat("a", 257)) }, "github_filter_length_invalid:githubTagPrefix"},
		{"byte limit", func(v *LinkInput) { v.GithubTagPrefix = p(strings.Repeat("é", 129)) }, "github_filter_length_invalid:githubTagPrefix"},
		{"long regex", func(v *LinkInput) { v.GithubTagRegex = p(strings.Repeat("a", 1025)) }, "github_filter_length_invalid:githubTagRegex"},
		{"long glob", func(v *LinkInput) { v.GithubAssetPattern = p(strings.Repeat("a", 513)) }, "github_filter_length_invalid:githubAssetPattern"},
		{"blank prefix", func(v *LinkInput) { v.GithubTagPrefix = p(" ") }, "github_filter_length_invalid:githubTagPrefix"},
		{"empty regex", func(v *LinkInput) { v.GithubTagRegex = p("") }, "github_filter_length_invalid:githubTagRegex"},
		{"empty glob", func(v *LinkInput) { v.GithubAssetPattern = p("") }, "github_filter_length_invalid:githubAssetPattern"},
		{"invalid regex", func(v *LinkInput) { v.GithubTagRegex = p("[") }, "github_tag_regex_invalid:githubTagRegex"},
		{"lookahead", func(v *LinkInput) { v.GithubTagRegex = p("(?=app)") }, "github_tag_regex_invalid:githubTagRegex"},
		{"backreference", func(v *LinkInput) { v.GithubTagRegex = p(`(app)\1`) }, "github_tag_regex_invalid:githubTagRegex"},
		{"invalid glob", func(v *LinkInput) { v.GithubAssetPattern = p("[") }, "github_asset_pattern_invalid:githubAssetPattern"},
		{"exclusive", func(v *LinkInput) { v.GithubTagPrefix = p("app-"); v.GithubTagRegex = p("app") }, "github_tag_filters_exclusive:githubTagRegex"},
		{"repo required", func(v *LinkInput) { v.GithubRepo = nil; v.GithubAssetPattern = p("*.dmg") }, "github_repo_required:githubRepo"},
		{"prerelease repo required", func(v *LinkInput) { v.GithubRepo = nil; v.IncludePrereleases = true }, "github_repo_required:githubRepo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := LinkInput{Token: "example", Name: "Example", Homepage: p("https://example.com"), GithubRepo: p("owner/app")}
			tc.edit(&v)
			err := v.Validate()
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				var app *AppError
				require.ErrorAs(t, err, &app)
				require.Equal(t, CodeValidation, app.Code)
				require.Equal(t, "rejected: "+tc.want, app.Msg)
			}
		})
	}
}
