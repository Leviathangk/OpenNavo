package management

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentProofsExpireLikeCapturedEvidence(t *testing.T) {
	body := map[string]any{"items": []any{
		map[string]any{"kind": "attribute", "evidence": map[string]any{"sourceUrl": "https://vendor.example.test", "checkedAt": "2026-10-01T08:00:00Z", "validUntil": "2099-01-01T00:00:00Z", "status": "verified"}},
		map[string]any{"kind": "attribute", "evidence": map[string]any{"checkedAt": "2026-10-01T08:00:00Z", "validUntil": "2026-10-10T00:00:00Z", "status": "verified"}},
		map[string]any{"nested": []any{map[string]any{"evidence": map[string]any{"checkedAt": "2026-10-01T08:00:00+02:00", "validUntil": "2027-01-01T00:00:00Z"}}}},
		map[string]any{"evidence": map[string]any{"checkedAt": "not a time", "validUntil": "2099-01-01T00:00:00Z"}},
	}}
	capAgentProofs(body)
	items := body["items"].([]any)
	proof := func(i int) map[string]any { return items[i].(map[string]any)["evidence"].(map[string]any) }
	require.Equal(t, "2026-10-31T08:00:00Z", proof(0)["validUntil"], "capped at checkedAt + 30 days")
	require.Equal(t, "2026-10-10T00:00:00Z", proof(1)["validUntil"], "a shorter validity is kept")
	nested := items[2].(map[string]any)["nested"].([]any)[0].(map[string]any)["evidence"].(map[string]any)
	require.Equal(t, "2026-10-31T06:00:00Z", nested["validUntil"])
	require.Equal(t, "2099-01-01T00:00:00Z", proof(3)["validUntil"], "malformed proofs are left to validation")
}
