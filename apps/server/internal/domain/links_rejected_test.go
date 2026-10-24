package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLinkRejectionsNameRulesWithoutEchoingValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*LinkInput)
		reason string
	}{
		{"token", func(v *LinkInput) { v.Token = "private/value" }, "token_invalid:token"},
		{"name", func(v *LinkInput) { v.Name = " " }, "name_invalid:name"},
		{"locale", func(v *LinkInput) { v.SourceLocale = "private-locale" }, "locale_invalid:sourceLocale"},
		{"destination", func(v *LinkInput) { v.Homepage = nil }, "link_destination_required"},
		{"url", func(v *LinkInput) { x := "https://127.0.0.1/private"; v.Homepage = &x }, "url_invalid:homepage"},
		{"bundle", func(v *LinkInput) { v.BundleIDs = []string{"com.example.App", "com.example.App"} }, "bundle_id_invalid_or_duplicate:bundleIds"},
		{"notes key", func(v *LinkInput) { v.Notes = map[string]*string{"private-key": nil} }, "notes_invalid:notes"},
		{"regex", func(v *LinkInput) { v.AssetRules = map[string]string{"arm64": "["} }, "asset_rule_pattern_invalid:assetRules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homepage := "https://example.com/app"
			v := LinkInput{Token: "fixture-app", Name: "Fixture App", Homepage: &homepage}
			tc.edit(&v)
			err := v.Validate()
			var app *AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, CodeValidation, app.Code)
			require.Equal(t, "rejected: "+tc.reason, app.Msg)
			require.NotContains(t, app.Msg, "private")
		})
	}
}
