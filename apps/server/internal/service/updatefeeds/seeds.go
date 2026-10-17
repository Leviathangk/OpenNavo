package updatefeeds

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/opennavo/opennavo/server/internal/domain"
)

// Derived data and notices are pinned in docs/legal/native/*-upstream.json.
//
//go:embed data/identities.json
var identityData []byte

//go:embed data/vendor-rules.json
var vendorRuleData []byte

// ZIP names must identify macOS; a .dmg or .pkg is intrinsically a Mac archive.
const defaultMacPattern = `(?i)(\.(dmg|pkg)$|(?:mac|darwin|osx)[^/]*\.zip$)`

type seededIdentity struct {
	BundleID      string   `json:"bundleId"`
	TeamID        string   `json:"teamId"`
	Architectures []string `json:"architectures"`
}

func seedTeam(bundle string) *string {
	var identities []seededIdentity
	if json.Unmarshal(identityData, &identities) != nil {
		return nil
	}
	team := ""
	for _, row := range identities {
		if row.BundleID == bundle {
			if team != "" && team != row.TeamID {
				return nil
			}
			team = row.TeamID
		}
	}
	if team == "" {
		return nil
	}
	return &team
}
func SeedRules() ([]domain.UpdateFeedInput, error) {
	var rules []domain.UpdateFeedInput
	if err := json.Unmarshal(vendorRuleData, &rules); err != nil {
		return nil, err
	}
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			return nil, err
		}
		team := seedTeam(rules[i].BundleID)
		if team == nil || rules[i].TeamID == nil || *team != *rules[i].TeamID {
			return nil, errors.New("seed identity mismatch")
		}
	}
	return rules, nil
}
func (s *Service) importSeeds(ctx context.Context) error {
	rules, err := SeedRules()
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if err := s.Store.WriteUpdateFeed(ctx, rule, "seed"); err != nil {
			return err
		}
	}
	return nil
}
