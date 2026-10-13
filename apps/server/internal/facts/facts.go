// Package facts holds the evidence rules for app facts (WP12): price type, a manual macOS
// supplement and directional alternatives. Admin, the Agent API, public pages and structured data
// all read these rules, so an unknown value, old evidence or an architecture difference is never
// shown as a verified fact. Public wording comes from the fixed codes below, translated in the
// repository; notes stay internal.
package facts

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/homebrew"
)

const (
	MaxAlternatives = 8
	MaxReasons      = 4
	MaxLimits       = 3
	// StaleAfter: older evidence is shown with its date as possibly outdated and stops feeding offers.
	StaleAfter = 365 * 24 * time.Hour
	maxSkew    = 5 * time.Minute
)

var (
	// PriceTypes: "unknown" clears the price; free downloads never imply "free".
	PriceTypes   = []string{"free", "free_with_iap", "paid", "subscription", "trial", "unknown"}
	PricePeriods = []string{"one_time", "month", "year"}
	Methods      = []string{"vendor_site", "app_store", "vendor_docs", "in_app", "other"}
	Statuses     = []string{"verified", "needs_review"}
	Archs        = []string{"arm64", "x86_64"}
	// Supports: rosetta applies to arm64 only; unsupported ranges carry no versions.
	Supports = []string{"native", "rosetta", "unsupported"}
	Reasons  = []string{"free", "open_source", "lighter", "more_features", "simpler", "native_ui", "apple_silicon", "privacy", "offline", "collaboration", "cross_platform", "one_time_purchase", "active_development", "customizable"}
	Limits   = []string{"paid", "subscription", "fewer_features", "requires_account", "learning_curve", "different_workflow", "no_apple_silicon", "online_only", "limited_languages"}
	Audience = []string{"everyday", "professionals", "developers", "teams", "beginners", "power_users"}
)

// Conflict and gap codes. Conflicts compare the supplement with Homebrew; gaps tell editors and
// agents (dry_run) which evidence is missing or no longer current.
const (
	ConflictMinimum = "minimum"
	ConflictMaximum = "maximum"
	ConflictArm64   = "arm64"
	ConflictX8664   = "x86_64"

	GapPriceUnknown           = "price.unknown"
	GapPriceReview            = "price.review"
	GapPriceStale             = "price.stale"
	GapPriceOffer             = "price.offer"
	GapPriceVersion           = "price.version"
	GapMacosMissing           = "macos.missing"
	GapMacosReview            = "macos.review"
	GapMacosStale             = "macos.stale"
	GapMacosVersion           = "macos.version"
	GapMacosConflict          = "macos.conflict"
	GapAlternativeUnavailable = "alternatives.unavailable"
)

// ErrInvalid marks input the API rejects as a validation error.
var ErrInvalid = errors.New("invalid fact")

// Evidence is stored with every fact.
type Evidence struct {
	AppVersion *string   `json:"appVersion"`
	SourceURL  string    `json:"sourceUrl"`
	Method     string    `json:"method"`
	VerifiedAt time.Time `json:"verifiedAt"`
	VerifiedBy string    `json:"verifiedBy"`
	Verifier   string    `json:"verifier"` // admin | agent
	Status     string    `json:"status"`
	Note       *string   `json:"note"`
}

// Price is stored as package_facts.price; nil means unknown.
type Price struct {
	Type     string   `json:"type"`
	Amount   *float64 `json:"amount"`
	Currency *string  `json:"currency"`
	Region   *string  `json:"region"`
	Plan     *string  `json:"plan"`
	Period   *string  `json:"period"`
	Evidence
}

// Range is one architecture's support; versions are macOS releases such as "13" or "10.15".
type Range struct {
	Arch    string  `json:"arch"`
	Support string  `json:"support"`
	Minimum *string `json:"minimum"`
	Maximum *string `json:"maximum"`
}

// Macos is stored as package_facts.macos; nil means no supplement.
type Macos struct {
	Ranges []Range `json:"ranges"`
	Evidence
}

// Alternative is one row of package_alternatives, in display order.
type Alternative struct {
	PackageID int64    `json:"packageId"`
	Reasons   []string `json:"reasons"`
	Limits    []string `json:"limits"`
	BestFor   *string  `json:"bestFor"`
	Note      *string  `json:"note"`
}

// Actor records who verified a fact.
type Actor struct {
	Name  string
	Agent bool
}

// EvidenceInput is the evidence part of a write; omitted verifiedAt means now and omitted status
// means verified.
type EvidenceInput struct {
	AppVersion *string    `json:"appVersion"`
	SourceURL  *string    `json:"sourceUrl"`
	Method     *string    `json:"method"`
	VerifiedAt *time.Time `json:"verifiedAt"`
	Status     *string    `json:"status"`
	Note       *string    `json:"note"`
}

type PriceInput struct {
	Type     string   `json:"type"`
	Amount   *float64 `json:"amount"`
	Currency *string  `json:"currency"`
	Region   *string  `json:"region"`
	Plan     *string  `json:"plan"`
	Period   *string  `json:"period"`
	EvidenceInput
}

type MacosInput struct {
	Ranges []Range `json:"ranges"`
	EvidenceInput
}

var (
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	regionPattern   = regexp.MustCompile(`^[A-Z]{2}$`)
	macosPattern    = regexp.MustCompile(`^(?:10\.(?:[4-9]|1[0-9])|1[1-9]|[2-9][0-9])(?:\.[0-9]{1,2}){0,2}$`)
	earliest        = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Decode reads a JSON body strictly: unknown fields are rejected so typos never pass silently.
func Decode(body map[string]any, target any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrInvalid
	}
	return nil
}

// label trims a short single-line text; empty input becomes nil.
func label(value *string, limit int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text := strings.TrimSpace(*value)
	if text == "" {
		return nil, nil
	}
	if len([]rune(text)) > limit || strings.ContainsFunc(text, unicode.IsControl) {
		return nil, ErrInvalid
	}
	return &text, nil
}

// note allows line breaks but no other control characters.
func note(value *string, limit int) (*string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	text := strings.TrimSpace(*value)
	if len([]rune(text)) > limit || strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) {
		return nil, ErrInvalid
	}
	return &text, nil
}

// ValidSourceURL accepts absolute https URLs without credentials.
func ValidSourceURL(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func evidence(in EvidenceInput, actor Actor, now time.Time) (Evidence, error) {
	out := Evidence{Status: "verified", VerifiedAt: now.UTC().Truncate(time.Second), VerifiedBy: actor.Name, Verifier: "admin"}
	if actor.Agent {
		out.Verifier = "agent"
	}
	if in.SourceURL == nil || !ValidSourceURL(strings.TrimSpace(*in.SourceURL)) {
		return out, ErrInvalid
	}
	out.SourceURL = strings.TrimSpace(*in.SourceURL)
	if in.Method == nil || !slices.Contains(Methods, *in.Method) {
		return out, ErrInvalid
	}
	out.Method = *in.Method
	if in.VerifiedAt != nil {
		at := in.VerifiedAt.UTC().Truncate(time.Second)
		if at.Before(earliest) || at.After(now.Add(maxSkew)) {
			return out, ErrInvalid
		}
		out.VerifiedAt = at
	}
	if in.Status != nil {
		if !slices.Contains(Statuses, *in.Status) {
			return out, ErrInvalid
		}
		out.Status = *in.Status
	}
	var err error
	if out.AppVersion, err = label(in.AppVersion, 64); err != nil {
		return out, err
	}
	out.Note, err = note(in.Note, 1000)
	return out, err
}

func (in EvidenceInput) empty() bool {
	return in.AppVersion == nil && in.SourceURL == nil && in.Method == nil && in.VerifiedAt == nil && in.Status == nil && in.Note == nil
}

// NormalizePrice validates a price write. Type unknown clears the price and takes no other field.
func NormalizePrice(in PriceInput, actor Actor, now time.Time) (*Price, error) {
	if !slices.Contains(PriceTypes, in.Type) {
		return nil, ErrInvalid
	}
	if in.Type == "unknown" {
		if in.Amount != nil || in.Currency != nil || in.Region != nil || in.Plan != nil || in.Period != nil || !in.empty() {
			return nil, ErrInvalid
		}
		return nil, nil
	}
	proof, err := evidence(in.EvidenceInput, actor, now)
	if err != nil {
		return nil, err
	}
	out := &Price{Type: in.Type, Evidence: proof}
	if out.Plan, err = label(in.Plan, 80); err != nil {
		return nil, err
	}
	if in.Amount == nil {
		// Currency, region and period describe an amount; without one they would claim nothing.
		if in.Currency != nil || in.Region != nil || in.Period != nil {
			return nil, ErrInvalid
		}
		return out, nil
	}
	amount := *in.Amount
	if in.Type == "free" || amount <= 0 || amount > 1e7 || math.Abs(amount*100-math.Round(amount*100)) > 1e-6 {
		return nil, ErrInvalid
	}
	if in.Currency == nil || !currencyPattern.MatchString(*in.Currency) || in.Period == nil || !slices.Contains(PricePeriods, *in.Period) {
		return nil, ErrInvalid
	}
	if in.Region != nil && !regionPattern.MatchString(*in.Region) {
		return nil, ErrInvalid
	}
	// A one-off price is not a subscription, and a subscription renews.
	if (in.Type == "paid" && *in.Period != "one_time") || (in.Type == "subscription" && *in.Period == "one_time") {
		return nil, ErrInvalid
	}
	rounded := math.Round(amount*100) / 100
	out.Amount, out.Currency, out.Region, out.Period = &rounded, in.Currency, in.Region, in.Period
	return out, nil
}

// NormalizeMacos validates a supplement write. No ranges clears it and takes no other field.
func NormalizeMacos(in MacosInput, actor Actor, now time.Time) (*Macos, error) {
	if len(in.Ranges) == 0 {
		if !in.empty() {
			return nil, ErrInvalid
		}
		return nil, nil
	}
	if len(in.Ranges) > len(Archs) {
		return nil, ErrInvalid
	}
	ranges := make([]Range, 0, len(in.Ranges))
	for _, r := range in.Ranges {
		if !slices.Contains(Archs, r.Arch) || !slices.Contains(Supports, r.Support) || slices.ContainsFunc(ranges, func(seen Range) bool { return seen.Arch == r.Arch }) {
			return nil, ErrInvalid
		}
		if r.Support == "rosetta" && r.Arch != "arm64" {
			return nil, ErrInvalid
		}
		r.Minimum, r.Maximum = trimmed(r.Minimum), trimmed(r.Maximum)
		if r.Support == "unsupported" && (r.Minimum != nil || r.Maximum != nil) {
			return nil, ErrInvalid
		}
		for _, version := range []*string{r.Minimum, r.Maximum} {
			if version != nil && !macosPattern.MatchString(*version) {
				return nil, ErrInvalid
			}
		}
		if r.Minimum != nil && r.Maximum != nil {
			if order, _ := homebrew.CompareVersion(*r.Minimum, *r.Maximum); order > 0 {
				return nil, ErrInvalid
			}
		}
		ranges = append(ranges, r)
	}
	slices.SortFunc(ranges, func(a, b Range) int { return slices.Index(Archs, a.Arch) - slices.Index(Archs, b.Arch) })
	proof, err := evidence(in.EvidenceInput, actor, now)
	if err != nil {
		return nil, err
	}
	return &Macos{Ranges: ranges, Evidence: proof}, nil
}

func trimmed(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	text := strings.TrimSpace(*value)
	return &text
}

func codes(values, vocabulary []string, minimum, maximum int) ([]string, error) {
	if values == nil {
		values = []string{}
	}
	if len(values) < minimum || len(values) > maximum {
		return nil, ErrInvalid
	}
	for index, value := range values {
		if !slices.Contains(vocabulary, value) || slices.Contains(values[:index], value) {
			return nil, ErrInvalid
		}
	}
	return values, nil
}

// NormalizeAlternatives validates an ordered list. Zero is valid: the list never pads with
// unrelated apps. Every entry names at least one reason from the fixed vocabulary.
func NormalizeAlternatives(self int64, items []Alternative) ([]Alternative, error) {
	if len(items) > MaxAlternatives {
		return nil, ErrInvalid
	}
	out := make([]Alternative, 0, len(items))
	for _, item := range items {
		if item.PackageID < 1 || item.PackageID == self || slices.ContainsFunc(out, func(seen Alternative) bool { return seen.PackageID == item.PackageID }) {
			return nil, ErrInvalid
		}
		var err error
		if item.Reasons, err = codes(item.Reasons, Reasons, 1, MaxReasons); err != nil {
			return nil, err
		}
		if item.Limits, err = codes(item.Limits, Limits, 0, MaxLimits); err != nil {
			return nil, err
		}
		if item.BestFor != nil && !slices.Contains(Audience, *item.BestFor) {
			return nil, ErrInvalid
		}
		if item.Note, err = note(item.Note, 500); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// Stale reports evidence older than StaleAfter.
func Stale(at, now time.Time) bool {
	return now.Sub(at) > StaleAfter
}

// Shown reports whether public pages present the fact at all: evidence marked for review is held
// back like an unknown value.
func (e Evidence) Shown() bool {
	return e.Status == "verified"
}

// OfferReady reports whether structured data may publish an offer: verified and current, and
// either free to obtain (price 0) or a complete amount with currency, region and plan.
func OfferReady(p *Price, now time.Time) bool {
	if p == nil || !p.Shown() || Stale(p.VerifiedAt, now) {
		return false
	}
	switch p.Type {
	case "free", "free_with_iap":
		return true
	case "paid", "subscription", "trial":
		return p.Amount != nil && p.Currency != nil && p.Region != nil && p.Plan != nil
	}
	return false
}

// Homebrew is what the cask declares, for comparison with the supplement.
type Homebrew struct {
	Known           bool // official platform list or explicit architecture declaration
	Arm64, X8664    bool
	RequiresRosetta bool
	Minimum         string
	Maximum         string
}

// HomebrewFrom collects the comparable requirements of a cask.
func HomebrewFrom(minimum *string, arm64, x8664 bool, metadata homebrew.CaskPlatformMetadata) Homebrew {
	out := Homebrew{Known: metadata.SupportStatus == "known", Arm64: arm64, X8664: x8664, RequiresRosetta: metadata.RequiresRosetta}
	if minimum != nil {
		out.Minimum = *minimum
	}
	for _, field := range []string{"macos", "maximum_macos"} {
		var comparisons map[string][]string
		if json.Unmarshal(metadata.DependsOn[field], &comparisons) != nil {
			continue
		}
		for _, version := range comparisons["<="] {
			if order, ok := homebrew.CompareVersion(version, out.Maximum); out.Maximum == "" || (ok && order > 0) {
				out.Maximum = version
			}
		}
	}
	return out
}

// Conflicts lists where the supplement and Homebrew disagree; pages then show both as pending
// verification instead of picking one. Missing data on either side is not a conflict, and chip
// support alone never implies native code.
func Conflicts(m *Macos, hb Homebrew) []string {
	out := []string{}
	if m == nil {
		return out
	}
	add := func(code string) {
		if !slices.Contains(out, code) {
			out = append(out, code)
		}
	}
	for _, r := range m.Ranges {
		supported := r.Support != "unsupported"
		switch r.Arch {
		case "arm64":
			if hb.Known && hb.Arm64 != supported || hb.RequiresRosetta && r.Support == "native" {
				add(ConflictArm64)
			}
		case "x86_64":
			if hb.Known && hb.X8664 != supported {
				add(ConflictX8664)
			}
		}
		if differs(r.Minimum, hb.Minimum) {
			add(ConflictMinimum)
		}
		if differs(r.Maximum, hb.Maximum) {
			add(ConflictMaximum)
		}
	}
	order := []string{ConflictMinimum, ConflictMaximum, ConflictArm64, ConflictX8664}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(order, a) - slices.Index(order, b) })
	return out
}

func differs(value *string, declared string) bool {
	if value == nil || declared == "" {
		return false
	}
	order, ok := homebrew.CompareVersion(*value, declared)
	return ok && order != 0
}

// Gaps lists missing or outdated evidence, in a fixed order. unavailable counts alternatives that
// pages currently skip (hidden, removed or disabled).
func Gaps(p *Price, m *Macos, hb Homebrew, unavailable int, now time.Time) []string {
	out := []string{}
	if p == nil {
		out = append(out, GapPriceUnknown)
	} else {
		if p.Status == "needs_review" {
			out = append(out, GapPriceReview)
		}
		if Stale(p.VerifiedAt, now) {
			out = append(out, GapPriceStale)
		}
		if slices.Contains([]string{"paid", "subscription", "trial"}, p.Type) && (p.Amount == nil || p.Currency == nil || p.Region == nil || p.Plan == nil) {
			out = append(out, GapPriceOffer)
		}
		if p.AppVersion == nil {
			out = append(out, GapPriceVersion)
		}
	}
	if m == nil {
		if !hb.Known && hb.Minimum == "" {
			out = append(out, GapMacosMissing)
		}
	} else {
		if m.Status == "needs_review" {
			out = append(out, GapMacosReview)
		}
		if Stale(m.VerifiedAt, now) {
			out = append(out, GapMacosStale)
		}
		if m.AppVersion == nil {
			out = append(out, GapMacosVersion)
		}
		if len(Conflicts(m, hb)) > 0 {
			out = append(out, GapMacosConflict)
		}
	}
	if unavailable > 0 {
		out = append(out, GapAlternativeUnavailable)
	}
	return out
}
