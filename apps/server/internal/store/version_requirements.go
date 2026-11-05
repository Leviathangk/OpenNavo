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
	// A chip-unknown bound with no recorded app version applies to the requested version.
	// A bound recorded for a different version stays off this version's list.
	if m := legacy.Macos; m != nil && m.Shown() && ((m.AppVersion != nil && *m.AppVersion == version) || (facts.UnknownOnly(m.Ranges) && m.AppVersion == nil)) {
		status := m.Status
		if len(facts.Conflicts(m, legacy.Homebrew)) > 0 {
			status = "needs_review"
		}
		proof := facts.NewProof(m.SourceURL, m.VerifiedAt, facts.StaleAfter, status, now)
		shown := version
		if m.AppVersion != nil {
			shown = *m.AppVersion
		}
		for _, r := range m.Ranges {
			out = append(out, r.Requirement(shown, proof))
		}
	}
	return out, nil
}
