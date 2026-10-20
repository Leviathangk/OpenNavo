package facts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntegrationRequiresBoundedPackagePermissionsAndHistory(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	good := Integration{Kind: "mcp", Name: "Example connector", RepositoryURL: "https://github.com/example/connector", Version: "1.0", Installation: IntegrationInstall{Method: "npm", Package: "@example/connector"}, Clients: []string{"codex"}, Permissions: IntegrationPermissions{Files: []IntegrationFile{{Path: "~/Documents/Example", Access: "read"}}, Network: []string{"https://api.example.test"}}, AppleSilicon: "unknown", Versions: []IntegrationVersion{{Version: "1.0", ReleasedAt: now.Add(-time.Hour), SourceURL: "https://github.com/example/connector/releases/tag/1.0"}}}
	require.NoError(t, good.Validate(now))
	for _, mutate := range []func(*Integration){func(v *Integration) { v.Installation.Package = "example; rm -rf /" }, func(v *Integration) { v.Installation.Package = "https://example.test/code" }, func(v *Integration) { v.Installation.Method = "shell" }, func(v *Integration) { v.Permissions.Network = []string{"https://example.test/path"} }, func(v *Integration) { v.Versions[0].SourceURL = "https://github.com/unrelated/project/releases/1.0" }, func(v *Integration) { v.Versions[0].ReleasedAt = now.Add(24 * time.Hour) }, func(v *Integration) { v.Version = "2.0" }, func(v *Integration) { v.Clients = []string{"codex", "codex"} }} {
		raw, _ := json.Marshal(good)
		var bad Integration
		require.NoError(t, json.Unmarshal(raw, &bad))
		mutate(&bad)
		require.ErrorIs(t, bad.Validate(now), ErrInvalid)
	}
}
func TestCompatibilityUsesOnlyExactCurrentEvidence(t *testing.T) {
	now := time.Now()
	min, max := "13.0", "15.0"
	proof := NewProof("https://vendor.example/requirements", now, time.Hour, "verified", now)
	row := Compatibility{AppVersion: "1.2", Minimum: &min, Maximum: &max, AppleSilicon: "yes"}.Requirement(proof)
	summary := SummarizeCompatibility("1.2", []Requirement{row}, now)
	require.Equal(t, &min, summary.Minimum)
	require.Equal(t, "yes", summary.AppleSilicon)
	summary = SummarizeCompatibility("0.9", []Requirement{row}, now)
	require.Nil(t, summary.Minimum)
	require.Equal(t, "unknown", summary.AppleSilicon)
	require.Empty(t, summary.Requirements)
	summary = SummarizeCompatibility("1.2", []Requirement{row}, now.Add(time.Hour))
	require.Nil(t, summary.Minimum)
	require.Equal(t, "unknown", summary.AppleSilicon)
	require.Equal(t, "stale", summary.Requirements[0].Evidence.Status)
	other := row
	otherMin := "14.0"
	other.Minimum = &otherMin
	other.AppleSilicon = "no"
	summary = SummarizeCompatibility("1.2", []Requirement{row, other}, now)
	require.Nil(t, summary.Minimum)
	require.Equal(t, &max, summary.Maximum)
	require.Equal(t, "unknown", summary.AppleSilicon)
}
