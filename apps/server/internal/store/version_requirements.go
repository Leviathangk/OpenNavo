package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
)

func (s *Store) VersionRequirements(ctx context.Context, id int64, version string, now time.Time) ([]facts.Requirement, error) {
	var snapshot struct {
		Platforms []byte
		SourceURL string
		CheckedAt time.Time
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT r.platforms,r.source_url,
 CASE WHEN p.version=r.app_version OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(p.dependencies->'platforms','[]')) v WHERE v->>'version'=r.app_version)
 THEN p.homebrew_checked_at ELSE r.checked_at END AS checked_at
 FROM package_version_requirements r JOIN packages p ON p.id=r.package_id WHERE r.package_id=? AND r.app_version=?`, id, version).Scan(&snapshot).Error
	if err != nil {
		return nil, err
	}
	out := []facts.Requirement{}
	if len(snapshot.Platforms) > 0 {
		var platforms []homebrew.CaskPlatform
		if err = json.Unmarshal(snapshot.Platforms, &platforms); err != nil {
			return nil, err
		}
		out = facts.Requirements(platforms, facts.NewProof(snapshot.SourceURL, snapshot.CheckedAt, facts.StaleAfter, "verified", now))
	}
	records, err := s.FactRecords(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.Kind != "compatibility" {
			continue
		}
		var v facts.Compatibility
		if json.Unmarshal(r.Value, &v) == nil && v.AppVersion == version && v.Validate() == nil {
			out = append(out, v.Requirement(r.Evidence.At(now)))
		}
	}
	legacy, _, err := s.PackageFacts(ctx, id)
	if err != nil {
		return nil, err
	}
	if m := legacy.Macos; m != nil && m.AppVersion != nil && *m.AppVersion == version {
		status := m.Status
		if len(facts.Conflicts(m, legacy.Homebrew)) > 0 {
			status = "needs_review"
		}
		proof := facts.NewProof(m.SourceURL, m.VerifiedAt, facts.StaleAfter, status, now)
		for _, r := range m.Ranges {
			native := "unknown"
			if r.Arch == "arm64" {
				if r.Support == "native" {
					native = "yes"
				} else {
					native = "no"
				}
			}
			out = append(out, facts.Requirement{Versions: []string{version}, Architectures: []string{r.Arch}, PlatformMacOS: []string{}, Minimum: r.Minimum, Maximum: r.Maximum, AppleSilicon: native, Evidence: proof})
		}
	}
	return out, nil
}
