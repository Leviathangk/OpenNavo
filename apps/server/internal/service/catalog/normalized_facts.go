package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *PublicService) normalizedFacts(ctx context.Context, d *publicapi.PackageDetail, p store.PublicPackage, locale string) error {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	var metadata homebrew.CaskPlatformMetadata
	if err := json.Unmarshal(p.Dependencies, &metadata); err != nil {
		return err
	}
	var artifacts homebrew.CaskArtifacts
	if err := json.Unmarshal(p.Artifacts, &artifacts); err != nil {
		return err
	}
	proof := facts.NewProof(d.SourceUrl, p.HomebrewCheckedAt, facts.StaleAfter, "verified", now)
	if p.Kind == "link" {
		proof = facts.Proof{SourceURL: d.SourceUrl, Status: "unknown"}
	}
	aliases := []string{}
	if err := s.Store.DB.WithContext(ctx).Raw("SELECT token FROM package_aliases WHERE package_id=? ORDER BY token", p.ID).Scan(&aliases).Error; err != nil {
		return err
	}
	dependencyCount := 0
	if d.DependsOn != nil {
		dependencyCount = len(d.DependsOn.Casks) + len(d.DependsOn.Formulae)
	}
	requirements := facts.Requirements(metadata.Platforms, proof)
	if m := d.MacosSupplement; m != nil {
		status := "verified"
		if len(m.Conflicts) > 0 || m.AppVersion == nil || *m.AppVersion != d.Version {
			status = "needs_review"
		}
		evidence := facts.NewProof(m.SourceUrl, m.VerifiedAt, facts.StaleAfter, status, now)
		for _, r := range m.Ranges {
			native := "unknown"
			if r.Arch == "arm64" {
				if r.Support == "native" {
					native = "yes"
				} else {
					native = "no"
				}
			}
			versions := []string{}
			if m.AppVersion != nil {
				versions = append(versions, *m.AppVersion)
			}
			requirements = append(requirements, facts.Requirement{Versions: versions, Architectures: []string{string(r.Arch)}, PlatformMacOS: []string{}, Minimum: r.Minimum, Maximum: r.Maximum, AppleSilicon: native, Evidence: evidence})
		}
	}
	versionRows, err := s.Store.VersionRequirements(ctx, p.ID, p.Version, now)
	if err != nil {
		return err
	}
	for _, row := range versionRows {
		if len(row.Architectures) == 0 {
			requirements = append(requirements, row)
		}
	}
	view := map[string]any{
		"requirements": requirements, "uninstall": facts.Uninstall(artifacts, proof),
		"overview":     map[string]any{"version": d.Version, "downloadUrl": d.DownloadUrl, "sha256": facts.FullSHA256(d.DownloadSha256), "artifacts": d.Artifacts, "caveats": d.Caveats, "aliases": aliases, "dependencyCount": dependencyCount, "conflictCount": len(d.ConflictsWith.Casks) + len(d.ConflictsWith.Formulae)},
		"alternatives": []publicapi.PackageAlternative{},
	}
	if d.Alternatives != nil {
		view["alternatives"] = *d.Alternatives
	}
	if d.Price != nil {
		view["price"] = d.Price
	}
	if d.MacosSupplement != nil {
		view["macosSupplement"] = d.MacosSupplement
	}
	prices, err := s.priceFacts(ctx, d, p.ID, now)
	if err != nil {
		return err
	}
	security, err := s.securityFacts(ctx, d, p.ID, now)
	if err != nil {
		return err
	}
	alternatives, err := s.alternativeFacts(ctx, d, p.ID, locale, now)
	if err != nil {
		return err
	}
	issues, err := s.issueFacts(ctx, p.ID, locale, now)
	if err != nil {
		return err
	}
	view["issues"] = issues
	integrations, err := s.integrationFacts(ctx, p.ID, p.Version, now)
	if err != nil {
		return err
	}
	view["integrations"] = integrations
	view["alternatives"] = alternatives
	view["security"] = security
	view["prices"] = prices
	view["offers"] = []facts.PriceFact{}
	data, err := json.Marshal(view)
	if err != nil {
		return err
	}
	d.Facts = &publicapi.AppFacts{}
	return json.Unmarshal(data, d.Facts)
}
