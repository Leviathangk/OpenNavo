package articles

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaimsRequireWholeStatementsAndCompleteCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, claim, source string
		want                bool
	}{
		{"negative", "Safe", "Not Safe.", false},
		{"clipped sentence", "The app is safe", "The app is safe only for offline files.", false},
		{"exact sentence", "The app supports macOS.", "Release 1.0. The app supports macOS. See the guide.", true},
		{"Chinese", "支持 macOS", "软件不支持 macOS。", false},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, CompleteAssertion(tc.claim, tc.source)) })
	}
	require.False(t, ClaimsCover("Not safe", []Claim{{Text: "Not"}, {Text: "safe"}}))
	claims := []Claim{{Text: "Supports macOS."}, {Text: "Supports macOS. Requires macOS 15."}}
	require.True(t, ClaimsCover("# Supports macOS. Requires macOS 15.\n\n::app[example-app]", claims))
	for _, text := range []string{"Supports macOS. It is safer.", "Not Supports macOS.", "Supports macOS.\n| Faster | yes |", "Supports macOS.\n![Exports 10 files in 2 seconds](https://assets.example.test/x.png)"} {
		require.False(t, ClaimsCover(text, claims), text)
	}
	for _, text := range []string{"We tested it.", "Our tests show it is faster.", "我們實測比它更快。", "我们测试了这款应用。"} {
		require.True(t, TestingClaim(text))
	}
}

func TestOnlyCheckableStatementsNeedClaims(t *testing.T) {
	prose := "Open Finder and drag the app to the Trash. This guide walks through each step.\n\nFree up disk space afterwards.\n\n## Pricing\n\n::app[example-app]"
	require.True(t, ClaimsCover(prose, nil), "explanatory prose, a plain heading and disk cleanup need no claim")
	for _, text := range []string{
		"Version 2.0 adds a dark mode.",
		"Obsidian is free for personal use.",
		"It is open source.",
		"It requires Apple silicon.",
		"The app is safe to install.",
		"Run `brew install --cask obsidian` to install it.",
		"## Version 2.0",
		"| Plan | Price |\n|---|---|\n| Pro | $50 |",
		"```\nbrew install --cask obsidian\n```",
		"Obsidian 个人使用免费。",
		"Obsidian ist kostenlos.",
	} {
		require.False(t, ClaimsCover(text, nil), text)
	}
	require.True(t, ClaimsCover("| Plan | Notes |\n|---|---|", nil), "a table header row is a label")
	for _, text := range []string{
		"Clop supports images, videos, PDF and audio.",
		"支持图片、视频、PDF 和音频。",
		"带签名、表单或归档要求的文档应保留原件。",
	} {
		require.True(t, ClaimsCover(text, nil), "feature verbs and e-signatures are not platform or safety facts: "+text)
	}
	for _, text := range []string{"支持 Apple 芯片。", "It runs on macOS Sonoma.", "本文依据官方资料整理，不是实测评测。"} {
		require.False(t, ClaimsCover(text, nil), "chips, macOS releases and testing language need a claim: "+text)
	}
	references := "Clop compresses files on the Mac.[1]\n\n### References\n\n[1] [Clop features and free plan](https://lowtechguys.com/clop/)\n\n- [Free trial terms](https://vendor.example.test/trial)"
	require.True(t, ClaimsCover(references, nil), "citation markers and reference entries are attribution")
	require.False(t, ClaimsCover("It costs $20.[1]", nil), "a cited fact still needs its claim")
	require.True(t, ClaimsCover("It costs $20.[1]", []Claim{{Text: "It costs $20.", Excerpt: "Pro costs $20."}}))
	table := "| Plan | Price |\n|---|---|\n| Pro | $50 |"
	require.True(t, ClaimsCover(table, []Claim{{Text: "$50", Excerpt: "Pro costs $50 per year."}}), "a claim covers one table cell")
	require.False(t, ClaimsCover(table, []Claim{{Text: "| Pro | $50 |", Excerpt: "Pro costs $50 per year."}}), "cells are units, not whole rows")
}

func TestReworkedClaimsMustAgreeWithTheirExcerpt(t *testing.T) {
	for _, tc := range []struct {
		name, claim, excerpt string
		want                 bool
	}{
		{"verbatim", "The app supports macOS 13.", "Release notes. The app supports macOS 13. See the guide.", true},
		{"own words", "Version 2.0 adds a dark mode.", "What's new in 2.0: dark mode for every window.", true},
		{"translated", "Obsidian 个人使用免费。", "Obsidian is free for personal use.", true},
		{"other number", "It requires macOS 14.", "Requires macOS 13 or later.", false},
		{"missing price", "It costs $50 per year.", "Pro costs 50 per year.", false},
		{"other category", "It is free and open source.", "It is free for personal use.", false},
		{"turned positive", "It supports Intel Macs.", "It does not support Intel Macs.", false},
		{"turned negative", "Obsidian ist nicht kostenlos.", "Obsidian is free for personal use.", false},
		{"code quoted", "Run `brew install --cask obsidian` to install it.", "Install with brew install --cask obsidian from a terminal.", true},
		{"code invented", "Run `brew install obsidian` to install it.", "Install with brew install --cask obsidian from a terminal.", false},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, Supports(tc.claim, tc.excerpt)) })
	}
	code := "Run `brew install --cask obsidian` to install it."
	quoted := []Claim{{Text: code, Excerpt: "Install with brew install --cask obsidian from a terminal."}}
	require.True(t, ClaimsCover(code, quoted))
	require.False(t, ClaimsCover(code, []Claim{{Text: code, Excerpt: "Install it from the website."}}), "inline code must come from the excerpt")
	fence := "```\nbrew install --cask obsidian\n```"
	require.True(t, ClaimsCover(fence, quoted))
	require.False(t, ClaimsCover(fence, []Claim{{Text: "Install it.", Excerpt: "Install it from the website."}}))
}
