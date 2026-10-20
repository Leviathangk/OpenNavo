package management

import (
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTranslationImpactUsesPageDecisions(t *testing.T) {
	rows := []store.IndexRow{{ID: 1, Kind: "cask", Token: "example", Name: "Example", Summary: "A useful editor", Description: "A full description", Version: "1.0", SourceLocale: "en-US", ReadyLocales: []string{"fr-FR"}}}
	impact := translationImpact(rows, seo.Policy{Mode: "shadow", TranslationMode: "shadow"}, 1, 2)
	require.Equal(t, 5, impact["total"])
	require.Len(t, impact["records"], 2)
	counts := impact["counts"].([]map[string]any)
	require.Equal(t, 0, counts[0]["losing"])
	require.Equal(t, 1, counts[1]["losing"])
	require.Equal(t, 0, counts[7]["losing"])
}
