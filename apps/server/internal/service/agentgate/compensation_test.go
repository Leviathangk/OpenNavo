package agentgate

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCompensationCannotOverwriteNewRevisionOrWidenFields(t *testing.T) {
	before := map[string]any{"summary": "before", "price": 10}
	after := map[string]any{"summary": "after", "price": 10}
	fields := ChangedFields(before, after)
	require.Equal(t, []string{"/summary"}, fields)
	require.NoError(t, CheckCompensation(before, after, after, fields, nil, "implementation_error"))
	require.Error(t, CheckCompensation(before, after, map[string]any{"summary": "human edit", "price": 10}, fields, nil, "implementation_error"))
	require.Error(t, CheckCompensation(before, after, after, fields, []string{"/summary", "/price"}, "implementation_error"))
	require.Error(t, CheckCompensation(before, after, after, fields, nil, "traffic_decline"))
}

func TestCompensationCannotRestoreExpiredFacts(t *testing.T) {
	now := time.Now().UTC()
	evidence := []Evidence{{SourceURL: "https://vendor.example.test/pricing", FinalURL: "https://vendor.example.test/pricing", Authority: "official"}}
	proof := map[string]any{"sourceUrl": evidence[0].SourceURL, "checkedAt": now.Add(-48 * time.Hour).Format(time.RFC3339), "validUntil": now.Add(-time.Hour).Format(time.RFC3339), "status": "verified"}
	require.ErrorContains(t, ValidatePriorFacts(map[string]any{"evidence": proof}, evidence, now), "prior_fact_expired")
	proof["validUntil"] = now.Add(time.Hour).Format(time.RFC3339)
	require.NoError(t, ValidatePriorFacts(map[string]any{"evidence": proof}, evidence, now))
}
