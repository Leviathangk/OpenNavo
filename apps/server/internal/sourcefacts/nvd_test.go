package sourcefacts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNVDOnlyExactApplicableCPE(t *testing.T) {
	cpe := "cpe:2.3:a:example:editor:2.1:*:*:*:*:macos:*:*"
	for _, tc := range []struct {
		name, criteria, operator, status       string
		vulnerable, negate, rangeVersion, want bool
	}{
		{name: "exact", criteria: cpe, operator: "OR", vulnerable: true, want: true},
		{name: "other product", criteria: strings.Replace(cpe, ":editor:", ":other:", 1), operator: "OR", vulnerable: true},
		{name: "other version", criteria: strings.Replace(cpe, ":2.1:", ":2.0:", 1), operator: "OR", vulnerable: true},
		{name: "wildcard", criteria: strings.Replace(cpe, ":2.1:", ":*:", 1), operator: "OR", vulnerable: true},
		{name: "not vulnerable", criteria: cpe, operator: "OR"},
		{name: "and", criteria: cpe, operator: "AND", vulnerable: true},
		{name: "negation", criteria: cpe, operator: "OR", vulnerable: true, negate: true},
		{name: "range", criteria: cpe, operator: "OR", vulnerable: true, rangeVersion: true},
		{name: "rejected", criteria: cpe, operator: "OR", vulnerable: true, status: "Rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, cpe, r.URL.Query().Get("cpeName"))
				match := map[string]any{"criteria": tc.criteria, "vulnerable": tc.vulnerable}
				if tc.rangeVersion {
					match["versionStartIncluding"] = "1.0"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"totalResults": 1, "vulnerabilities": []any{map[string]any{"cve": map[string]any{"id": "CVE-2026-12345", "vulnStatus": tc.status, "configurations": []any{map[string]any{"nodes": []any{map[string]any{"operator": tc.operator, "negate": tc.negate, "cpeMatch": []any{match}}}}}}}}})
			}))
			defer srv.Close()
			matches, err := (&NVD{Client: srv.Client(), Base: srv.URL}).ExactMatches(context.Background(), cpe)
			require.NoError(t, err)
			if tc.want {
				require.Len(t, matches, 1)
				require.Equal(t, "2.1", matches[0].AppVersion)
			} else {
				require.Empty(t, matches)
			}
		})
	}
}
