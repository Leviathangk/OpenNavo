package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
)

type issueFact struct {
	facts.Issue
	Evidence facts.Proof `json:"evidence"`
}

func (s *PublicService) issueFacts(ctx context.Context, id int64, locale string, now time.Time) ([]issueFact, error) {
	records, err := s.Store.FactRecords(ctx, id)
	if err != nil {
		return nil, err
	}
	localized, fallback := []issueFact{}, []issueFact{}
	for _, r := range records {
		if r.Kind != "issue" || r.Evidence.At(now).Status != "verified" {
			continue
		}
		var v facts.Issue
		if json.Unmarshal(r.Value, &v) != nil || v.Validate() != nil {
			continue
		}
		item := issueFact{Issue: v, Evidence: r.Evidence.At(now)}
		if v.Locale == locale {
			localized = append(localized, item)
		} else if v.Locale == "en-US" {
			fallback = append(fallback, item)
		}
	}
	if len(localized) > 0 {
		return localized, nil
	}
	return fallback, nil
}
