package facts

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
)

// Proof is the shared, public provenance contract. Unknown checked dates are never replaced by now.
type Proof struct {
	SourceURL  string     `json:"sourceUrl"`
	CheckedAt  *time.Time `json:"checkedAt"`
	ValidUntil *time.Time `json:"validUntil"`
	Status     string     `json:"status"`
}

// WithDefaultValidity applies the same 30-day lifetime as captured agent evidence.
// Call this only on writes: reading legacy evidence must not invent expiry dates.
func (p Proof) WithDefaultValidity() Proof {
	if p.ValidUntil == nil && p.CheckedAt != nil {
		until := p.CheckedAt.Add(30 * 24 * time.Hour)
		p.ValidUntil = &until
	}
	return p
}

func (p Proof) At(now time.Time) Proof {
	if !ValidSourceURL(p.SourceURL) || p.CheckedAt == nil || p.ValidUntil == nil || p.CheckedAt.After(now.Add(maxSkew)) || !p.ValidUntil.After(*p.CheckedAt) {
		p.Status = "unknown"
	} else if p.Status == "verified" && !now.Before(*p.ValidUntil) {
		p.Status = "stale"
	}
	return p
}

func (p Proof) Validate(now time.Time) error {
	if p.Status != "verified" && p.Status != "needs_review" {
		return rejected("evidence_status_invalid")
	}
	if !ValidSourceURL(p.SourceURL) {
		return rejected("evidence_source_url_invalid")
	}
	if p.CheckedAt == nil || p.CheckedAt.Before(earliest) || p.CheckedAt.After(now.Add(maxSkew)) {
		return rejected("evidence_checked_at_invalid")
	}
	p = p.WithDefaultValidity()
	if !p.ValidUntil.After(*p.CheckedAt) {
		return rejected("evidence_valid_until_invalid")
	}
	return nil
}

func NewProof(source string, checked time.Time, ttl time.Duration, status string, now time.Time) Proof {
	if checked.IsZero() {
		return Proof{SourceURL: source, Status: "unknown"}
	}
	until := checked.Add(ttl)
	return (Proof{source, &checked, &until, status}).At(now)
}

type Requirement struct {
	Versions      []string `json:"versions"`
	Architectures []string `json:"architectures"`
	PlatformMacOS []string `json:"platformMacOS"`
	Minimum       *string  `json:"minimumMacOS"`
	Maximum       *string  `json:"maximumMacOS"`
	AppleSilicon  string   `json:"appleSilicon"`
	DownloadURL   *string  `json:"downloadUrl"`
	SHA256        *string  `json:"sha256"`
	Evidence      Proof    `json:"evidence"`
}

var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func FullSHA256(value *string) *string {
	if value == nil || !sha256Pattern.MatchString(*value) {
		return nil
	}
	result := strings.ToLower(*value)
	return &result
}

// Requirements compresses only identical declarations. Platform labels do not set minimum OS,
// and installability on arm64 does not establish native binary architecture.
func Requirements(platforms []homebrew.CaskPlatform, proof Proof) []Requirement {
	out := []Requirement{}
	keys := map[string]int{}
	for _, p := range platforms {
		max := maximumMacOS(p.DependsOn.Requirements)
		native := "unknown"
		if p.RequiresRosetta && p.Arch == "arm64" {
			native = "no"
		}
		architectures := []string{}
		if p.Arch != "" {
			architectures = append(architectures, p.Arch)
		}
		row := Requirement{[]string{p.Version}, architectures, []string{}, p.MinMacos, max, native, p.DownloadURL, FullSHA256(p.DownloadSHA256), proof}
		keyData, _ := json.Marshal(row)
		key := string(keyData)
		if i, exists := keys[key]; exists {
			if p.Macos != "" && !slices.Contains(out[i].PlatformMacOS, p.Macos) {
				out[i].PlatformMacOS = append(out[i].PlatformMacOS, p.Macos)
			}
		} else {
			keys[key] = len(out)
			if p.Macos != "" {
				row.PlatformMacOS = append(row.PlatformMacOS, p.Macos)
			}
			out = append(out, row)
		}
	}
	return out
}

func maximumMacOS(requirements map[string]json.RawMessage) *string {
	var maximum *string
	for _, field := range []string{"macos", "maximum_macos"} {
		var conditions map[string][]string
		if json.Unmarshal(requirements[field], &conditions) != nil {
			continue
		}
		for _, v := range conditions["<="] {
			if maximum == nil {
				value := v
				maximum = &value
			} else if order, ok := homebrew.CompareVersion(v, *maximum); ok && order < 0 {
				value := v
				maximum = &value
			}
		}
	}
	return maximum
}

type UninstallItem struct {
	Kind            string `json:"kind"`
	Value           string `json:"value"`
	Phase           string `json:"phase"`
	DeletesUserData bool   `json:"deletesUserData"`
	Evidence        Proof  `json:"evidence"`
}

// Uninstall extracts declarations for display. It never executes scripts or treats a label as a
// local identity. Only explicitly declared strings are emitted; unknown objects remain in artifacts.
func Uninstall(artifacts homebrew.CaskArtifacts, proof Proof) []UninstallItem {
	out := []UninstallItem{}
	seen := map[string]bool{}
	for _, entry := range artifacts.Entries {
		if entry.Phase != "uninstall" && entry.Phase != "cleanup" {
			continue
		}
		phase := "uninstall"
		if entry.Phase == "cleanup" {
			phase = "zap"
		}
		add := func(kind, value string) {
			if value == "" {
				return
			}
			key := phase + "\x00" + kind + "\x00" + value
			if !seen[key] {
				seen[key] = true
				out = append(out, UninstallItem{kind, value, phase, phase == "zap", proof})
			}
		}
		var walk func(map[string]json.RawMessage)
		walk = func(object map[string]json.RawMessage) {
			for _, key := range []string{"uninstall", "zap"} {
				var nested map[string]json.RawMessage
				if json.Unmarshal(object[key], &nested) == nil {
					walk(nested)
				} else {
					var list []map[string]json.RawMessage
					if json.Unmarshal(object[key], &list) == nil {
						for _, value := range list {
							walk(value)
						}
					}
				}
			}
			for _, key := range []string{"launchctl", "login_item", "pkgutil", "trash", "delete", "rmdir"} {
				kind := key
				if key == "trash" || key == "delete" || key == "rmdir" {
					kind = "zap"
				}
				var values []string
				if json.Unmarshal(object[key], &values) != nil {
					var one string
					if json.Unmarshal(object[key], &one) == nil {
						values = []string{one}
					}
				}
				for _, v := range values {
					add(kind, v)
				}
			}
		}
		walk(entry.Declaration)
	}
	return out
}
