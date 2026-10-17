package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLinkInputRejectsUnsafeDestinationsAndAmbiguousIdentifiers(t *testing.T) {
	for _, target := range []string{"http://example.com", "file:///tmp/app", "https://user:password@example.com", "https://127.0.0.1/app", "https://10.0.0.1/app", "https://[::1]/app", "https://example.com/app#fragment"} {
		input := LinkInput{Token: "example-app", Name: "Example", Homepage: &target, Pricing: "free"}
		require.Error(t, input.Validate(), target)
	}
	home := "https://example.com/download"
	repo := "owner/repo"
	team := "ABCDE12345"
	input := LinkInput{Token: "example-app", Name: "Example", Homepage: &home, GithubRepo: &repo, TeamID: &team, Pricing: "open_source", BundleIDs: []string{"com.example.App"}, AssetRules: map[string]string{"arm64": "arm64.*dmg$"}}
	require.NoError(t, input.Validate())
	input.Token = "owner/repo"
	require.Error(t, input.Validate())
	input.Token = "valid"
	input.AssetRules["arm64"] = "["
	require.Error(t, input.Validate())
	input.AssetRules = nil
	repo = "https://github.com/owner/repo"
	input.GithubRepo = &repo
	require.Error(t, input.Validate())
}
