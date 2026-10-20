package api_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

// Keep the shared evidence payloads valid when composed schemas and dry-run envelopes change.
func TestFactContractsExamples(t *testing.T) {
	for _, kind := range []string{"public", "admin"} {
		spec, err := openapi3.NewLoader().LoadFromFile(kind + ".openapi.yaml")
		require.NoError(t, err)
		for _, name := range []string{"FactEvidence", "FactRecordEvidence", "FactRecord", "FactRecordsUpdate", "FactRecordsResult", "BackgroundContent", "BackgroundContentResponse", "BackgroundItem", "BackgroundItemResponse", "BackgroundItemsResponse", "CollectionCriteria", "CollectionCriteriaResponse", "CollectionSetup", "PriceFact", "SecurityIdentityFact", "ComparisonAttributeFact", "VersionCompatibility", "MigrationGuide", "KnownIssue", "KnownIssueFact", "AppIntegration", "AppIntegrationFact", "AppFacts"} {
			if ref := spec.Components.Schemas[name]; ref != nil {
				t.Run(kind+"/"+name, func(t *testing.T) {
					require.NotNil(t, ref.Value.Example)
					require.NoError(t, ref.Value.VisitJSON(ref.Value.Example))
				})
			}
		}
		for _, path := range []string{"/background-items", "/background-items/{label}", "/packages/{id}/fact-records", "/collections/{id}/setup-criteria"} {
			item := spec.Paths.Find(path)
			if item == nil {
				continue
			}
			for method, op := range item.Operations() {
				t.Run(kind+"/"+method+path, func(t *testing.T) {
					media := op.Responses.Status(200).Value.Content.Get("application/json")
					for _, locale := range localeCodes(t) {
						example := media.Examples[locale]
						require.NotNil(t, example, locale)
						require.NoError(t, media.Schema.Value.VisitJSON(example.Value.Value), locale)
					}
				})
			}
		}
	}
}
