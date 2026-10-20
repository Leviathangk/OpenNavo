package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
)

type integrationFact struct {
	facts.Integration
	Evidence facts.Proof `json:"evidence"`
}

func (s *PublicService) integrationFacts(ctx context.Context, id int64, version string, now time.Time) ([]integrationFact, error) {
	out := []integrationFact{}
	records, err := s.Store.FactRecords(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.Kind != "integration" || r.Evidence.At(now).Status != "verified" {
			continue
		}
		var v facts.Integration
		if json.Unmarshal(r.Value, &v) != nil || v.Validate(now) != nil || v.AppVersion != "" && v.AppVersion != version {
			continue
		}
		out = append(out, integrationFact{v, r.Evidence.At(now)})
	}
	return out, nil
}
