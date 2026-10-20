package facts

import (
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
)

type Compatibility struct {
	AppVersion   string  `json:"appVersion"`
	Minimum      *string `json:"minimumMacOS"`
	Maximum      *string `json:"maximumMacOS"`
	AppleSilicon string  `json:"appleSilicon"`
}

func (v Compatibility) Validate() error {
	if v.AppVersion == "" || len(v.AppVersion) > 100 || !slices.Contains([]string{"yes", "no", "unknown"}, v.AppleSilicon) {
		return ErrInvalid
	}
	for _, value := range []*string{v.Minimum, v.Maximum} {
		if value != nil && !macosPattern.MatchString(*value) {
			return ErrInvalid
		}
	}
	if v.Minimum != nil && v.Maximum != nil {
		if order, ok := homebrew.CompareVersion(*v.Minimum, *v.Maximum); !ok || order > 0 {
			return ErrInvalid
		}
	}
	return nil
}
func (v Compatibility) Requirement(proof Proof) Requirement {
	return Requirement{Versions: []string{v.AppVersion}, Architectures: []string{}, PlatformMacOS: []string{}, Minimum: v.Minimum, Maximum: v.Maximum, AppleSilicon: v.AppleSilicon, Evidence: proof}
}

type VersionCompatibility struct {
	Compatibility
	Requirements []Requirement `json:"requirements"`
}

func SummarizeCompatibility(version string, rows []Requirement, now time.Time) VersionCompatibility {
	out := VersionCompatibility{Compatibility: Compatibility{AppVersion: version, AppleSilicon: "unknown"}, Requirements: []Requirement{}}
	minSet, maxSet, nativeSet := false, false, false
	minConflict, maxConflict, nativeConflict := false, false, false
	for _, r := range rows {
		if !slices.Contains(r.Versions, version) {
			continue
		}
		r.Evidence = r.Evidence.At(now)
		out.Requirements = append(out.Requirements, r)
		if r.Evidence.Status != "verified" {
			continue
		}
		if r.Minimum != nil {
			if minSet && *out.Minimum != *r.Minimum {
				minConflict = true
			}
			out.Minimum = r.Minimum
			minSet = true
		}
		if r.Maximum != nil {
			if maxSet && *out.Maximum != *r.Maximum {
				maxConflict = true
			}
			out.Maximum = r.Maximum
			maxSet = true
		}
		if r.AppleSilicon != "unknown" {
			if nativeSet && out.AppleSilicon != r.AppleSilicon {
				nativeConflict = true
			}
			out.AppleSilicon = r.AppleSilicon
			nativeSet = true
		}
	}
	if minConflict {
		out.Minimum = nil
	}
	if maxConflict {
		out.Maximum = nil
	}
	if nativeConflict {
		out.AppleSilicon = "unknown"
	}
	return out
}
