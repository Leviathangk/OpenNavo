package facts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)

func ptr[T any](value T) *T { return &value }

func proof() EvidenceInput {
	return EvidenceInput{SourceURL: ptr("https://vendor.example/pricing"), Method: ptr("vendor_site")}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	var in PriceInput
	require.NoError(t, Decode(map[string]any{"type": "free", "sourceUrl": "https://vendor.example"}, &in))
	require.Equal(t, "free", in.Type)
	require.ErrorIs(t, Decode(map[string]any{"type": "free", "price": 0}, &in), ErrInvalid)
	require.ErrorIs(t, Decode(map[string]any{"bad": make(chan int)}, &in), ErrInvalid)
}

func TestNormalizePriceRecordsEvidence(t *testing.T) {
	actor := Actor{Name: "Hermes", Agent: true}
	at := now.Add(-48 * time.Hour)
	in := PriceInput{Type: "subscription", Amount: ptr(9.99), Currency: ptr("USD"), Region: ptr("US"), Plan: ptr(" Pro "), Period: ptr("month"),
		EvidenceInput: EvidenceInput{SourceURL: ptr(" https://vendor.example/pricing "), Method: ptr("vendor_site"), VerifiedAt: &at, AppVersion: ptr("5.2"), Status: ptr("needs_review"), Note: ptr("Checked\nfrom the US store")}}
	p, err := NormalizePrice(in, actor, now)
	require.NoError(t, err)
	require.Equal(t, "Pro", *p.Plan)
	require.Equal(t, "https://vendor.example/pricing", p.SourceURL)
	require.Equal(t, at, p.VerifiedAt)
	require.Equal(t, "agent", p.Verifier)
	require.Equal(t, "Hermes", p.VerifiedBy)
	require.Equal(t, "needs_review", p.Status)
	require.False(t, p.Shown())

	free, err := NormalizePrice(PriceInput{Type: "free", Plan: ptr("  "), EvidenceInput: proof()}, Actor{Name: "kaka"}, now)
	require.NoError(t, err)
	require.Nil(t, free.Plan)
	require.Equal(t, "admin", free.Verifier)
	require.Equal(t, now, free.VerifiedAt, "omitted verifiedAt means now")
	require.Equal(t, "verified", free.Status)
	require.True(t, free.Shown())

	paid, err := NormalizePrice(PriceInput{Type: "paid", Amount: ptr(29.0), Currency: ptr("EUR"), Period: ptr("one_time"), EvidenceInput: proof()}, actor, now)
	require.NoError(t, err)
	require.Nil(t, paid.Region)

	unknown, err := NormalizePrice(PriceInput{Type: "unknown"}, actor, now)
	require.NoError(t, err)
	require.Nil(t, unknown, "unknown clears the price")
}

func TestNormalizePriceRejectsUnsupportedClaims(t *testing.T) {
	future := now.Add(time.Hour)
	ancient := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]PriceInput{
		"type":                {Type: "cheap", EvidenceInput: proof()},
		"unknown with fields": {Type: "unknown", Plan: ptr("Pro")},
		"unknown with source": {Type: "unknown", EvidenceInput: proof()},
		"missing source":      {Type: "free", EvidenceInput: EvidenceInput{Method: ptr("vendor_site")}},
		"http source":         {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("http://vendor.example"), Method: ptr("vendor_site")}},
		"credentials":         {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://user:pass@vendor.example"), Method: ptr("vendor_site")}},
		"long source":         {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://vendor.example/" + strings.Repeat("a", 2048)), Method: ptr("vendor_site")}},
		"method":              {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://vendor.example"), Method: ptr("guess")}},
		"future":              {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://vendor.example"), Method: ptr("vendor_site"), VerifiedAt: &future}},
		"ancient":             {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://vendor.example"), Method: ptr("vendor_site"), VerifiedAt: &ancient}},
		"status":              {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://vendor.example"), Method: ptr("vendor_site"), Status: ptr("maybe")}},
		"version":             {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://vendor.example"), Method: ptr("vendor_site"), AppVersion: ptr(strings.Repeat("1", 65))}},
		"note":                {Type: "free", EvidenceInput: EvidenceInput{SourceURL: ptr("https://vendor.example"), Method: ptr("vendor_site"), Note: ptr("bad\x00note")}},
		"plan":                {Type: "free", Plan: ptr("line\nbreak"), EvidenceInput: proof()},
		"currency alone":      {Type: "paid", Currency: ptr("USD"), EvidenceInput: proof()},
		"free amount":         {Type: "free", Amount: ptr(1.0), Currency: ptr("USD"), Period: ptr("one_time"), EvidenceInput: proof()},
		"zero amount":         {Type: "paid", Amount: ptr(0.0), Currency: ptr("USD"), Period: ptr("one_time"), EvidenceInput: proof()},
		"cents":               {Type: "paid", Amount: ptr(1.999), Currency: ptr("USD"), Period: ptr("one_time"), EvidenceInput: proof()},
		"huge":                {Type: "paid", Amount: ptr(1e8), Currency: ptr("USD"), Period: ptr("one_time"), EvidenceInput: proof()},
		"lowercase currency":  {Type: "paid", Amount: ptr(5.0), Currency: ptr("usd"), Period: ptr("one_time"), EvidenceInput: proof()},
		"no period":           {Type: "paid", Amount: ptr(5.0), Currency: ptr("USD"), EvidenceInput: proof()},
		"region":              {Type: "paid", Amount: ptr(5.0), Currency: ptr("USD"), Region: ptr("USA"), Period: ptr("one_time"), EvidenceInput: proof()},
		"paid renews":         {Type: "paid", Amount: ptr(5.0), Currency: ptr("USD"), Period: ptr("year"), EvidenceInput: proof()},
		"subscription once":   {Type: "subscription", Amount: ptr(5.0), Currency: ptr("USD"), Period: ptr("one_time"), EvidenceInput: proof()},
	}
	for name, in := range cases {
		_, err := NormalizePrice(in, Actor{Name: "kaka"}, now)
		require.ErrorIs(t, err, ErrInvalid, name)
	}
}

func TestNormalizeMacosKeepsRanges(t *testing.T) {
	m, err := NormalizeMacos(MacosInput{Ranges: []Range{
		{Arch: "x86_64", Support: "native", Minimum: ptr("10.15"), Maximum: ptr(" 14 ")},
		{Arch: "arm64", Support: "rosetta", Minimum: ptr(""), Maximum: nil},
	}, EvidenceInput: proof()}, Actor{Name: "kaka"}, now)
	require.NoError(t, err)
	require.Equal(t, "arm64", m.Ranges[0].Arch, "sorted by architecture")
	require.Nil(t, m.Ranges[0].Minimum)
	require.Equal(t, "14", *m.Ranges[1].Maximum)

	cleared, err := NormalizeMacos(MacosInput{}, Actor{}, now)
	require.NoError(t, err)
	require.Nil(t, cleared)

	cases := map[string]MacosInput{
		"evidence without ranges": {EvidenceInput: proof()},
		"three":                   {Ranges: []Range{{Arch: "arm64", Support: "native"}, {Arch: "x86_64", Support: "native"}, {Arch: "arm64", Support: "native"}}, EvidenceInput: proof()},
		"arch":                    {Ranges: []Range{{Arch: "ppc", Support: "native"}}, EvidenceInput: proof()},
		"support":                 {Ranges: []Range{{Arch: "arm64", Support: "maybe"}}, EvidenceInput: proof()},
		"duplicate":               {Ranges: []Range{{Arch: "arm64", Support: "native"}, {Arch: "arm64", Support: "rosetta"}}, EvidenceInput: proof()},
		"intel rosetta":           {Ranges: []Range{{Arch: "x86_64", Support: "rosetta"}}, EvidenceInput: proof()},
		"unsupported version":     {Ranges: []Range{{Arch: "x86_64", Support: "unsupported", Minimum: ptr("12")}}, EvidenceInput: proof()},
		"version":                 {Ranges: []Range{{Arch: "arm64", Support: "native", Minimum: ptr("9.2")}}, EvidenceInput: proof()},
		"inverted":                {Ranges: []Range{{Arch: "arm64", Support: "native", Minimum: ptr("14"), Maximum: ptr("13.6")}}, EvidenceInput: proof()},
		"evidence":                {Ranges: []Range{{Arch: "arm64", Support: "native"}}},
	}
	for name, in := range cases {
		_, err := NormalizeMacos(in, Actor{}, now)
		require.ErrorIs(t, err, ErrInvalid, name)
	}
}

func TestNormalizeAlternatives(t *testing.T) {
	items, err := NormalizeAlternatives(1, []Alternative{
		{PackageID: 2, Reasons: []string{"open_source", "lighter"}, Limits: nil, BestFor: ptr("developers"), Note: ptr(" internal ")},
		{PackageID: 3, Reasons: []string{"free"}, Limits: []string{"fewer_features"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{}, items[0].Limits)
	require.Equal(t, "internal", *items[0].Note)
	empty, err := NormalizeAlternatives(1, nil)
	require.NoError(t, err)
	require.Empty(t, empty, "zero alternatives is valid")

	nine := make([]Alternative, 9)
	for i := range nine {
		nine[i] = Alternative{PackageID: int64(i + 2), Reasons: []string{"free"}}
	}
	cases := map[string][]Alternative{
		"too many":      nine,
		"self":          {{PackageID: 1, Reasons: []string{"free"}}},
		"id":            {{PackageID: 0, Reasons: []string{"free"}}},
		"duplicate":     {{PackageID: 2, Reasons: []string{"free"}}, {PackageID: 2, Reasons: []string{"lighter"}}},
		"no reason":     {{PackageID: 2}},
		"reason":        {{PackageID: 2, Reasons: []string{"better"}}},
		"repeat reason": {{PackageID: 2, Reasons: []string{"free", "free"}}},
		"five reasons":  {{PackageID: 2, Reasons: []string{"free", "lighter", "simpler", "offline", "privacy"}}},
		"limit":         {{PackageID: 2, Reasons: []string{"free"}, Limits: []string{"ugly"}}},
		"audience":      {{PackageID: 2, Reasons: []string{"free"}, BestFor: ptr("everyone")}},
		"note":          {{PackageID: 2, Reasons: []string{"free"}, Note: ptr(strings.Repeat("a", 501))}},
	}
	for name, in := range cases {
		_, err := NormalizeAlternatives(1, in)
		require.ErrorIs(t, err, ErrInvalid, name)
	}
}

func TestOffersNeedVerifiedCurrentCompletePrices(t *testing.T) {
	complete := &Price{Type: "subscription", Amount: ptr(5.0), Currency: ptr("USD"), Region: ptr("US"), Plan: ptr("Pro"), Evidence: Evidence{Status: "verified", VerifiedAt: now}}
	require.True(t, OfferReady(complete, now))
	require.True(t, OfferReady(&Price{Type: "free_with_iap", Evidence: Evidence{Status: "verified", VerifiedAt: now}}, now))
	require.False(t, OfferReady(nil, now))
	require.False(t, OfferReady(&Price{Type: "trial", Evidence: Evidence{Status: "verified", VerifiedAt: now}}, now), "trial without an amount")
	stale := *complete
	stale.VerifiedAt = now.Add(-StaleAfter - time.Hour)
	require.False(t, OfferReady(&stale, now))
	review := *complete
	review.Status = "needs_review"
	require.False(t, OfferReady(&review, now))
	require.False(t, OfferReady(&Price{Type: "other", Evidence: Evidence{Status: "verified", VerifiedAt: now}}, now))
}

func TestHomebrewFromReadsMaximum(t *testing.T) {
	metadata := homebrew.CaskPlatformMetadata{SupportStatus: "known", RequiresRosetta: true, DependsOn: map[string]json.RawMessage{
		"macos":         json.RawMessage(`{">=":["12"],"<=":["14","13"]}`),
		"maximum_macos": json.RawMessage(`{"<=":["15"]}`),
		"arch":          json.RawMessage(`"broken"`),
	}}
	hb := HomebrewFrom(ptr("12.0"), true, false, metadata)
	require.Equal(t, Homebrew{Known: true, Arm64: true, RequiresRosetta: true, Minimum: "12.0", Maximum: "15"}, hb)
	require.Equal(t, Homebrew{}, HomebrewFrom(nil, false, false, homebrew.CaskPlatformMetadata{DependsOn: map[string]json.RawMessage{"macos": json.RawMessage(`"bad"`)}}))
}

func TestConflictsShowDisagreementOnly(t *testing.T) {
	hb := Homebrew{Known: true, Arm64: true, X8664: true, Minimum: "13.0", Maximum: "15"}
	agree := &Macos{Ranges: []Range{{Arch: "arm64", Support: "native", Minimum: ptr("13")}, {Arch: "x86_64", Support: "native"}}}
	require.Empty(t, Conflicts(agree, hb))
	require.Empty(t, Conflicts(nil, hb))
	differ := &Macos{Ranges: []Range{
		{Arch: "x86_64", Support: "unsupported"},
		{Arch: "arm64", Support: "native", Minimum: ptr("12"), Maximum: ptr("14")},
	}}
	require.Equal(t, []string{ConflictMinimum, ConflictMaximum, ConflictX8664}, Conflicts(differ, hb))
	// Homebrew needing Rosetta contradicts "native"; Homebrew without data contradicts nothing.
	require.Equal(t, []string{ConflictArm64}, Conflicts(&Macos{Ranges: []Range{{Arch: "arm64", Support: "native"}}}, Homebrew{RequiresRosetta: true}))
	require.Empty(t, Conflicts(&Macos{Ranges: []Range{{Arch: "arm64", Support: "rosetta", Minimum: ptr("11")}, {Arch: "x86_64", Support: "native"}}}, Homebrew{}))
	require.Equal(t, []string{ConflictArm64, ConflictX8664}, Conflicts(&Macos{Ranges: []Range{{Arch: "arm64", Support: "rosetta"}, {Arch: "x86_64", Support: "native"}}}, Homebrew{Known: true}))
}

func TestGapsListMissingAndOutdatedEvidence(t *testing.T) {
	old := now.Add(-StaleAfter - time.Hour)
	require.Equal(t, []string{GapPriceUnknown, GapMacosMissing}, Gaps(nil, nil, Homebrew{}, 0, now))
	require.Equal(t, []string{GapPriceUnknown}, Gaps(nil, nil, Homebrew{Known: true}, 0, now))
	p := &Price{Type: "paid", Evidence: Evidence{Status: "needs_review", VerifiedAt: old}}
	m := &Macos{Ranges: []Range{{Arch: "arm64", Support: "native", Minimum: ptr("11")}}, Evidence: Evidence{Status: "needs_review", VerifiedAt: old}}
	require.Equal(t, []string{GapPriceReview, GapPriceStale, GapPriceOffer, GapPriceVersion, GapMacosReview, GapMacosStale, GapMacosVersion, GapMacosConflict, GapAlternativeUnavailable},
		Gaps(p, m, Homebrew{Minimum: "13"}, 1, now))
	current := &Price{Type: "free", Evidence: Evidence{Status: "verified", VerifiedAt: now, AppVersion: ptr("2")}}
	supplement := &Macos{Ranges: []Range{{Arch: "arm64", Support: "native"}}, Evidence: Evidence{Status: "verified", VerifiedAt: now, AppVersion: ptr("2")}}
	require.Empty(t, Gaps(current, supplement, Homebrew{}, 0, now))
}
