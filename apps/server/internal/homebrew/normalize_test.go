package homebrew

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, token string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("../../testdata/homebrew/" + token + ".json") // #nosec G304 -- Fixture names come only from fixed test parameters in this file.
	require.NoError(t, err)
	return data
}
func TestOfficialCaskMappings(t *testing.T) {
	item, err := Normalize("cask", fixture(t, "visual-studio-code"))
	require.NoError(t, err)
	require.Equal(t, "visual-studio-code", item.Token)
	require.Equal(t, "visual-studio-code", item.FullToken)
	require.Equal(t, "homebrew/cask", item.Tap)
	require.Equal(t, "Microsoft Visual Studio Code", item.Name)
	require.Equal(t, []string{"Microsoft Visual Studio Code", "VS Code", "visual-studio-code"}, item.Names)
	require.Equal(t, "1.140.0", item.Version)
	require.Equal(t, item.Version, item.VersionBase)
	require.True(t, item.AutoUpdates)
	require.True(t, item.SupportsArm64 && item.SupportsX8664)
	require.False(t, item.IsFont || item.KegOnly || item.Deprecated || item.Disabled)
	require.Equal(t, "Casks/v/visual-studio-code.rb", item.RubySourcePath)
	require.JSONEq(t, `{"apps":["Visual Studio Code.app"],"binaries":["code","code-tunnel"],"pkgs":[],"uninstall":[[{"launchctl":"com.microsoft.VSCode.ShipIt","quit":"com.microsoft.VSCode"}]]}`, legacyArtifacts(t, item.Artifacts))
	var metadata CaskPlatformMetadata
	require.NoError(t, json.Unmarshal(item.Dependencies, &metadata))
	require.JSONEq(t, `{"macos":{}}`, string(marshal(metadata.DependsOn)))
	require.Equal(t, CaskNormalizationVersion, metadata.NormalizationVersion)
	require.Len(t, metadata.Platforms, 14)
	var artifacts CaskArtifacts
	require.NoError(t, json.Unmarshal(item.Artifacts, &artifacts))
	require.Len(t, artifacts.Entries, 5)
	require.Equal(t, "/Applications/Visual Studio Code.app", *artifacts.Entries[1].Target)
	require.Equal(t, "cleanup", artifacts.Entries[4].Phase)
	require.JSONEq(t, `{}`, string(item.ConflictsWith))
	require.Equal(t, fixture(t, "visual-studio-code"), item.Raw)
	require.Len(t, item.RawHash, 64)
	wechat, err := Normalize("cask", fixture(t, "wechat"))
	require.NoError(t, err)
	require.Equal(t, "微信 Mac 版", wechat.ChineseName)
	require.Equal(t, "4.1.15.22", wechat.VersionBase)
	require.Equal(t, "12", wechat.MinMacos)
}

func TestOfficialFormulaMappings(t *testing.T) {
	item, err := Normalize("formula", fixture(t, "ripgrep"))
	require.NoError(t, err)
	require.Equal(t, "ripgrep", item.Token)
	require.Equal(t, "ripgrep", item.FullToken)
	require.Equal(t, "homebrew/core", item.Tap)
	require.Equal(t, "ripgrep", item.Name)
	require.Equal(t, "15.2.0", item.Version)
	require.Equal(t, "Unlicense", item.License)
	require.True(t, item.SupportsArm64 && item.SupportsX8664)
	require.False(t, item.AutoUpdates || item.KegOnly || item.IsFont)
	require.JSONEq(t, `{"binaries":["rg"]}`, legacyArtifacts(t, item.Artifacts))
	var dependencies struct {
		Runtime, Build, Test, Optional, Recommended []string
		UsesFromMacos                               []any
	}
	require.NoError(t, json.Unmarshal(item.Dependencies, &dependencies))
	require.Contains(t, dependencies.Runtime, "pcre2")
	require.Contains(t, dependencies.Build, "rust")
	require.Equal(t, []string{}, dependencies.Test)
	require.Equal(t, []string{}, dependencies.Optional)
	require.Equal(t, []string{}, dependencies.Recommended)
	require.JSONEq(t, `{"formulae":[],"reasons":[]}`, string(item.ConflictsWith))
	require.Equal(t, "Formula/r/ripgrep.rb", item.RubySourcePath)
}

func TestAllNonemptyMappingFieldsAndArchitectureVariants(t *testing.T) {
	metadata := `"tap":"custom/tap","desc":"description","homepage":"https://example.com","deprecated":true,"deprecation_date":"2026-01-01","deprecation_reason":"old","deprecation_replacement_cask":"new-app","disabled":true,"disable_date":"2026-02-01","disable_reason":"unsafe","disable_replacement_formula":"new-tool","caveats":"note","ruby_source_path":"Casks/f/font-test.rb","tap_git_head":"abcdef","generated_date":"2026-10-04"`
	raw := json.RawMessage(`{` + metadata + `,"token":"font-test","full_token":"custom/tap/font-test","name":["Font","字体","Font"],"old_tokens":["old-font"],"version":"2,42","auto_updates":true,"url":"https://example.com/font.zip","artifacts":[{"app":["Font.app"]},{"pkg":["font.pkg"]},{"binary":["/bin/tool"],"target":"/bin/newtool"},{"uninstall":[{"pkgutil":"id"}]}],"depends_on":{"arch":[{"type":"arm","bits":64}],"macos":{">=":["13"]}},"conflicts_with":{"cask":["other"]}}`)
	item, err := Normalize("cask", raw)
	require.NoError(t, err)
	require.Equal(t, Metadata{Tap: "custom/tap", Desc: "description", Homepage: "https://example.com", Deprecated: true, DeprecationDate: "2026-01-01", DeprecationReason: "old", DeprecationReplacementCask: "new-app", Disabled: true, DisableDate: "2026-02-01", DisableReason: "unsafe", DisableReplacementFormula: "new-tool", Caveats: "note", RubySourcePath: "Casks/f/font-test.rb", TapGitHead: "abcdef", GeneratedDate: "2026-10-04"}, item.Metadata)
	require.Equal(t, "cask:new-app", item.DeprecationReplacement)
	require.Equal(t, "formula:new-tool", item.DisableReplacement)
	require.Equal(t, []string{"Font", "字体", "old-font", "font-test"}, item.Names)
	require.Equal(t, []string{"old-font"}, item.OldTokens)
	require.Equal(t, "custom/tap/font-test", item.FullToken)
	require.Equal(t, "2", item.VersionBase)
	require.Equal(t, "https://example.com/font.zip", item.DownloadURL)
	require.True(t, item.IsFont && item.AutoUpdates && item.SupportsArm64)
	require.False(t, item.SupportsX8664)
	require.Equal(t, "13", item.MinMacos)
	require.JSONEq(t, `{"apps":["Font.app"],"binaries":["newtool"],"pkgs":["font.pkg"],"uninstall":[[{"pkgutil":"id"}]]}`, legacyArtifacts(t, item.Artifacts))
	require.JSONEq(t, `{"cask":["other"]}`, string(item.ConflictsWith))
	formula, err := Normalize("formula", json.RawMessage(`{`+metadata+`,"name":"tool","full_name":"custom/tap/tool","aliases":["alias","tool"],"oldnames":["old-tool"],"versions":{"stable":"3"},"license":{"any_of":["MIT","Apache-2.0"]},"urls":{"stable":{"url":"https://example.com/tool.tar.gz"}},"executables":["tool"],"dependencies":["runtime"],"build_dependencies":["build"],"test_dependencies":["test"],"optional_dependencies":["optional"],"recommended_dependencies":["recommended"],"uses_from_macos":["zlib"],"conflicts_with":["bad"],"conflicts_with_reasons":["same bin"],"keg_only":true,"bottle":{"stable":{"files":{"arm64_sonoma":{},"x86_64_linux":{}}}},"requirements":[{"name":"macos","version":"13"}]}`))
	require.NoError(t, err)
	require.Equal(t, item.Metadata, formula.Metadata)
	require.Equal(t, []string{"tool", "alias", "old-tool"}, formula.Names)
	require.Equal(t, []string{"old-tool"}, formula.OldTokens)
	require.Equal(t, "custom/tap/tool", formula.FullToken)
	require.Equal(t, `{"any_of":["MIT","Apache-2.0"]}`, formula.License)
	require.Equal(t, "https://example.com/tool.tar.gz", formula.DownloadURL)
	require.True(t, formula.KegOnly && formula.SupportsArm64)
	require.False(t, formula.SupportsX8664)
	require.Equal(t, "13", formula.MinMacos)
	require.JSONEq(t, `{"runtime":["runtime"],"build":["build"],"test":["test"],"optional":["optional"],"recommended":["recommended"],"usesFromMacos":["zlib"]}`, string(formula.Dependencies))
	require.JSONEq(t, `{"formulae":["bad"],"reasons":["same bin"]}`, string(formula.ConflictsWith))
	for _, example := range []struct {
		Kind, Raw  string
		Arm, Intel bool
	}{
		{"cask", `{"token":"intel","depends_on":{"arch":[{"type":"intel","bits":64}]}}`, false, true},
		{"cask", `{"token":"empty"}`, false, false},
		{"formula", `{"name":"source","versions":{"head":"HEAD"}}`, true, true},
		{"formula", `{"name":"intel","bottle":{"stable":{"files":{"sonoma":{},"x86_64_linux":{}}}}}`, false, true},
	} {
		p, err := Normalize(example.Kind, json.RawMessage(example.Raw))
		require.NoError(t, err)
		require.Equal(t, example.Arm, p.SupportsArm64)
		require.Equal(t, example.Intel, p.SupportsX8664)
	}
}

func TestCanonicalHashIgnoresOnlyVolatileFields(t *testing.T) {
	first, err := RawHash(json.RawMessage(`{"token":"tool","depends_on":{"b":2,"a":1},"tap_git_head":"a","generated_date":"a","analytics":{},"installed":[],"installed_time":1,"outdated":false,"pinned":false,"pinned_version":"1"}`))
	require.NoError(t, err)
	second, err := RawHash(json.RawMessage(`{"depends_on":{"a":1,"b":2},"token":"tool","tap_git_head":"b"}`))
	require.NoError(t, err)
	require.Equal(t, first, second)
	third, err := RawHash(json.RawMessage(`{"token":"tool","depends_on":{"a":1,"b":3}}`))
	require.NoError(t, err)
	require.NotEqual(t, first, third)
	for _, raw := range []string{`null`, `invalid`, `{"a":1} trailing`} {
		_, err := RawHash(json.RawMessage(raw))
		require.Error(t, err)
	}
	for _, example := range []struct{ Kind, Raw string }{{"unknown", `{}`}, {"cask", `{}`}, {"cask", `{"token":"; rm"}`}, {"formula", `{"name":9}`}, {"cask", `{"token":9}`}} {
		_, err := Normalize(example.Kind, json.RawMessage(example.Raw))
		require.Error(t, err)
	}
}

func TestRawHashPreservesLegacyFixtureHashes(t *testing.T) {
	// Captured from the original RawHash before Ruby address normalization.
	for token, want := range map[string]string{
		"apple-hewlett-packard-printer-drivers": "d39abb0a8828ae038a6db7483f1c108e7827c441b99dbe0dfd6cfd708b807554",
		"artisan":                               "7a716ab27f84cedaf7bd747bc0406d4769950b868256066d71657bb3c7bae256",
		"raycast":                               "fcff61a8e0e21a21393a4a9650688da4745e7f983be812d305aecc03764025d1",
		"ripgrep":                               "55221e15b0446c2d3fb04e1795f6b239304e499a607e3a14ebf277265cdbc3d5",
		"steam":                                 "cbcb990823d310db37a26514e18d5ddc668039a84add613eb591658517c581cb",
		"visual-studio-code":                    "4e5aea2be4a31b03f3c9ebf1144e558ce0469288e0f238d839b77ff8cfffff3d",
		"wechat":                                "f74632484a088db199ddb4b4b27f29c5deacabef21d24e39fae779205ccf582c",
	} {
		t.Run(token, func(t *testing.T) {
			got, err := RawHash(fixture(t, token))
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
	require.Equal(t, 2, CaskNormalizationVersion, "address cleanup must not trigger catalog renormalization")
}

func TestRawHashIgnoresRubyObjectAddressesInStringValues(t *testing.T) {
	for _, raw := range []string{
		`{"token":"koreader","version":"2026.10","depends_on":{"linux":"#<LinuxRequirement:0x000000012142f910>"}}`,
		`{"token":"zeal","version":"0.7.2","depends_on":{"arch":[{"type":"intel","bits":64}],"linux":"#<LinuxRequirement:0x000000012142f910>"}}`,
		`{"token":"nested","variations":{"arm64_sonoma":{"nested":[null,9007199254740993,1.2300,1e+09,true,{"value":"before #<Homebrew::LinuxRequirement:0x000000012142f910> after #<Other:0x000000012142f910>"}]}}}`,
	} {
		first, err := RawHash(json.RawMessage(raw))
		require.NoError(t, err)
		second, err := RawHash(json.RawMessage(strings.ReplaceAll(raw, "0x000000012142f910", "0xABCDEF0123")))
		require.NoError(t, err)
		require.Equal(t, first, second)
		cleaned, err := RawHash(json.RawMessage(strings.ReplaceAll(raw, ":0x000000012142f910", "")))
		require.NoError(t, err)
		require.Equal(t, cleaned, first, "only the object's address is removed")
		changed, err := RawHash(json.RawMessage(strings.ReplaceAll(raw, "LinuxRequirement", "MacOSRequirement")))
		require.NoError(t, err)
		require.NotEqual(t, first, changed, "the requirement class remains meaningful")
	}
	raw := json.RawMessage(`{"token":"koreader","depends_on":{"linux":"#<LinuxRequirement:0x123>"}}`)
	p, err := Normalize("cask", raw)
	require.NoError(t, err)
	require.Equal(t, raw, p.Raw, "the original source must remain intact")
}

func TestRawHashRetainsNonObjectAddressesAndRealChanges(t *testing.T) {
	for _, raw := range []string{
		`{"token":"tool","url":"https://example.com/0x123"}`,
		`{"token":"tool","sha256":"0x123"}`,
		`{"token":"tool","value":"#<LinuxRequirement:0x123"}`,
		`{"token":"tool","value":"#<LinuxRequirement:0x123xyz>"}`,
		`{"token":"tool","value":"LinuxRequirement:0x123"}`,
		`{"token":"tool","value":"#<LinuxRequirement:0x123 field=value>"}`,
		`{"token":"tool","#<LinuxRequirement:0x123>":"key"}`,
	} {
		first, err := RawHash(json.RawMessage(raw))
		require.NoError(t, err)
		second, err := RawHash(json.RawMessage(strings.ReplaceAll(raw, "0x123", "0x456")))
		require.NoError(t, err)
		require.NotEqual(t, first, second, raw)
	}
	first, err := RawHash(json.RawMessage(`{"token":"tool","version":"1","depends_on":{"linux":"#<LinuxRequirement:0x123>"}}`))
	require.NoError(t, err)
	second, err := RawHash(json.RawMessage(`{"token":"tool","version":"2","depends_on":{"linux":"#<LinuxRequirement:0x456>"}}`))
	require.NoError(t, err)
	require.NotEqual(t, first, second, "real content changes must still be delivered")
}

func legacyArtifacts(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var artifacts map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &artifacts))
	delete(artifacts, "entries")
	return string(marshal(artifacts))
}
