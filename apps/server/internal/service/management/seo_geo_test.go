package management

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func geoFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/seo_geo_import.json")
	require.NoError(t, err)
	var v map[string]any
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}
func geoDecodeFixture(t *testing.T, v any) (geoRequest, error) {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return decodeGeo(raw, "fixture.example", time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
}
func TestSEOGeoClosedImportAndBoundaries(t *testing.T) {
	_, err := geoDecodeFixture(t, geoFixture(t))
	require.NoError(t, err)
	cases := map[string]func(map[string]any, map[string]any){
		"answerText":   func(v, c map[string]any) { c["answerText"] = "do not store" },
		"bodyText":     func(v, c map[string]any) { v["bodyText"] = "do not store" },
		"rawResponse":  func(v, c map[string]any) { v["rawResponse"] = map[string]any{"secret": "fixture"} },
		"html":         func(v, c map[string]any) { c["html"] = "<p>do not store</p>" },
		"arbitraryURL": func(v, c map[string]any) { c["observationUrl"] = "https://wrong.example" },
		"wrongHost":    func(v, c map[string]any) { c["ownUrls"] = []string{"https://wrong.example/page"}; c["cited"] = true },
		"userinfo": func(v, c map[string]any) {
			c["ownUrls"] = []string{"https://secret@fixture.example/page"}
			c["cited"] = true
		},
		"query": func(v, c map[string]any) {
			c["ownUrls"] = []string{"https://fixture.example/page?token=fixture"}
			c["cited"] = true
		},
		"scheme":         func(v, c map[string]any) { c["ownUrls"] = []string{"javascript:fixture"}; c["cited"] = true },
		"domainURL":      func(v, c map[string]any) { c["thirdPartyDomains"] = []string{"https://vendor.example"} },
		"internalDomain": func(v, c map[string]any) { c["thirdPartyDomains"] = []string{"host.internal"} },
		"longDomain":     func(v, c map[string]any) { c["thirdPartyDomains"] = []string{strings.Repeat("a", 64) + ".example"} },
		"topN": func(v, c map[string]any) {
			c["thirdPartyDomains"] = strings.Fields("a.example b.example c.example d.example e.example f.example g.example h.example i.example j.example k.example")
		},
		"unknownNotZero":  func(v, c map[string]any) { c["cited"] = false },
		"missingNotFalse": func(v, c map[string]any) { c["status"] = "missing"; c["mentioned"] = false },
		"missingHash":     func(v, c map[string]any) { c["answerHash"] = nil },
		"duplicateCell":   func(v, c map[string]any) { v["cells"] = []any{c, c} },
		"oversized":       func(v, c map[string]any) { v["unknown"] = strings.Repeat("a", 513<<10) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := geoFixture(t)
			s := v["snapshot"].(map[string]any)
			c := s["cells"].([]any)[0].(map[string]any)
			mutate(s, c)
			_, err := geoDecodeFixture(t, v)
			require.Error(t, err)
		})
	}
	validURL := "https://fixture.example/" + strings.Repeat("a", 2048-len("https://fixture.example/"))
	require.True(t, geoURL(validURL, "fixture.example"))
	require.False(t, geoURL(validURL+"a", "fixture.example"))
	for _, base := range []string{"", "http://evidence.example", "https://user@evidence.example", "https://evidence.example?token=fixture", "https://host.internal", "https://127.0.0.1"} {
		require.Nil(t, geoObservationURL(base, "obs-1"))
	}
	require.Equal(t, "https://evidence.example/project/observations/obs-1", *geoObservationURL("https://evidence.example/project", "obs-1"))
	require.Nil(t, geoObservationURL("https://evidence.example", "../../secret"))
}
func TestSEOGeoUnknownDenominatorsAndComparability(t *testing.T) {
	r, err := geoDecodeFixture(t, geoFixture(t))
	require.NoError(t, err)
	counts := geoAggregate(r.Snapshot)
	require.Equal(t, 1, *counts.Answered)
	require.Zero(t, counts.Cited.Known)
	require.Zero(t, counts.Mentioned.Known)
	yes, no := true, false
	r.Snapshot.Cells[0].Cited = &yes
	r.Snapshot.Cells[0].Mentioned = &no
	counts = geoAggregate(r.Snapshot)
	require.Zero(t, counts.Cited.Known, "partial extraction has no rate denominator")
	require.Equal(t, 1, counts.Mentioned.Known)
	r.Snapshot.Cells[0].CitationExtraction = "complete"
	counts = geoAggregate(r.Snapshot)
	require.Equal(t, geoMetric{Positive: 1, Known: 1}, counts.Cited)
	original := geoComparison(r.Snapshot)
	r.Snapshot.Context.Market = "JP"
	require.NotEqual(t, original, geoComparison(r.Snapshot))
	r.Snapshot.Context.Market = "unknown"
	r.Snapshot.Engines[0].Surface = "consumer_ui"
	require.NotEqual(t, original, geoComparison(r.Snapshot))
	r.Snapshot.Engines[0].Surface = "provider_api"
	r.Snapshot.Cells[0].Cited = nil
	require.Equal(t, original, geoComparison(r.Snapshot), "outcomes cannot change the comparison group")
}
