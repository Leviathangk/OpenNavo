package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
)

func (s *PublicService) securityFacts(ctx context.Context, d *publicapi.PackageDetail, id int64, now time.Time) (map[string]any, error) {
	records, err := s.Store.FactRecords(ctx, id)
	if err != nil {
		return nil, err
	}
	identities := []any{}
	vulnerabilities := []map[string]any{}
	for _, r := range records {
		if r.Kind != "security" {
			continue
		}
		var v facts.SecurityObservation
		if json.Unmarshal(r.Value, &v) != nil || v.Validate() != nil {
			continue
		}
		proof := r.Evidence.At(now)
		if v.AppVersion != "" && v.AppVersion != d.Version {
			proof.Status = "needs_review"
		}
		identities = append(identities, struct {
			facts.SecurityObservation
			Evidence facts.Proof `json:"evidence"`
		}{v, proof})
		if v.ProductCPE != "" && proof.Status == "verified" && v.AppVersion == d.Version {
			matches, err := s.Store.PublicCVEs(ctx, id, v.ProductCPE, d.Version, now)
			if err != nil {
				return nil, err
			}
			vulnerabilities = append(vulnerabilities, matches...)
		}
	}
	if d.OpenSource != nil {
		o := d.OpenSource
		identities = append(identities, struct {
			facts.SecurityObservation
			Evidence facts.Proof `json:"evidence"`
		}{facts.SecurityObservation{Channels: []string{}, OpenSource: "yes", RepositoryURL: o.Url}, facts.NewProof(o.Url, o.CheckedAt, 30*24*time.Hour, "verified", now)})
	}
	return map[string]any{"identities": identities, "vulnerabilities": vulnerabilities, "scanUrl": "opennavo://scan/" + d.Token + "?preview=1"}, nil
}
