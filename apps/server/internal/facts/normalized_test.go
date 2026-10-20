package facts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/stretchr/testify/require"
)

func TestProofExpiresAtBoundaryAndNeverInventsDate(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	proof := NewProof("https://vendor.example/docs", now, 24*time.Hour, "verified", now)
	require.Equal(t, "verified", proof.Status)
	require.Equal(t, "stale", proof.At(now.Add(24*time.Hour)).Status)
	require.Equal(t, "unknown", NewProof(proof.SourceURL, time.Time{}, time.Hour, "verified", now).Status)
	proof.Status = "needs_review"
	require.Equal(t, "needs_review", proof.At(now.Add(24*time.Hour)).Status)
}

func TestRequirementCompressionPreservesVersionDownloadsAndUnknownNative(t *testing.T) {
	hash := strings.Repeat("a", 64)
	minOS := "12"
	otherHash := strings.Repeat("b", 64)
	base := homebrew.CaskPlatform{Version: "2.0", Arch: "arm64", Macos: "14", MinMacos: &minOS, DownloadSHA256: &hash}
	second := base
	second.Macos = "15"
	older := base
	older.Version = "1.0"
	older.DownloadSHA256 = &otherHash
	rows := Requirements([]homebrew.CaskPlatform{base, second, older}, Proof{Status: "unknown"})
	require.Len(t, rows, 2)
	require.Equal(t, []string{"14", "15"}, rows[0].PlatformMacOS)
	require.Equal(t, "unknown", rows[0].AppleSilicon)
	require.Equal(t, &minOS, rows[0].Minimum)
	require.Nil(t, rows[0].Maximum)
	require.Equal(t, hash, *rows[0].SHA256)
	noCheck := "no_check"
	require.Nil(t, FullSHA256(&noCheck))
}

func TestUninstallPreservesArraysSharedLabelsAndUserData(t *testing.T) {
	artifacts := homebrew.CaskArtifacts{Entries: []homebrew.CaskArtifact{
		{Phase: "uninstall", Declaration: map[string]json.RawMessage{"uninstall": json.RawMessage(`[{"launchctl":["com.example.shared","com.example.shared"],"pkgutil":"com.example.pkg","login_item":"Example"}]`)}},
		{Phase: "cleanup", Declaration: map[string]json.RawMessage{"zap": json.RawMessage(`[{"trash":["~/Library/Application Support/Example"],"launchctl":"com.example.shared"}]`)}},
	}}
	rows := Uninstall(artifacts, Proof{Status: "unknown"})
	require.Len(t, rows, 5)
	require.Equal(t, "com.example.shared", rows[0].Value)
	require.False(t, rows[0].DeletesUserData)
	require.True(t, rows[3].DeletesUserData)
	require.Equal(t, "~/Library/Application Support/Example", rows[4].Value)
}
