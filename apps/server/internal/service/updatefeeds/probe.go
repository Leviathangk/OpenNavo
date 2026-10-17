package updatefeeds

import (
	"context"
	"fmt"
)

// ProbeSeeds performs metadata GET/HEAD requests only. It opens no database and
// never follows a download URL returned by a metadata document.
func (s *Service) ProbeSeeds(ctx context.Context) ([]map[string]any, error) {
	rules, err := SeedRules()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		row := map[string]any{"bundleId": rule.BundleID, "source": "vendor"}
		result, err := s.ResolveVendor(ctx, rule)
		if err != nil {
			row["error"] = err.Error()
		} else {
			row["version"] = result.Version
			row["files"] = len(result.Files)
			hosts := []string{}
			for _, f := range result.Files {
				hosts = append(hosts, fmt.Sprintf("%s (%s)", f.Name, value(f.Architecture)))
			}
			row["artifacts"] = hosts
		}
		out = append(out, row)
	}
	return out, nil
}
func value(p *string) string {
	if p == nil {
		return "unknown"
	}
	return *p
}

// Sample rotates through the pinned rules weekly. A failed resolver is an error,
// not a reason to silently replace the sample with a more convenient product.
func (s *Service) Sample(ctx context.Context, count, week int) ([]map[string]any, error) {
	rules, err := SeedRules()
	if err != nil {
		return nil, err
	}
	if count < 1 || count > len(rules) {
		return nil, fmt.Errorf("sample count must be 1..%d", len(rules))
	}
	result := []map[string]any{}
	for n := 0; n < count; n++ {
		rule := rules[(week*count+n)%len(rules)]
		release, err := s.ResolveVendor(ctx, rule)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rule.BundleID, err)
		}
		result = append(result, map[string]any{"bundleId": rule.BundleID, "teamId": rule.TeamID, "release": release})
	}
	return result, nil
}
