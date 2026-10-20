package seoingest

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/stretchr/testify/require"
)

type versionQueryCase struct {
	Query                    string
	Notes, Original, Noindex bool
	Canonical                string
	Status                   int
	Robots                   string
}

func versionQueryCases(t *testing.T) []versionQueryCase {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/seo/version-query.json")
	require.NoError(t, err)
	var cases []versionQueryCase
	require.NoError(t, json.Unmarshal(raw, &cases))
	return cases
}

func TestVersionQueryMatchesWebContract(t *testing.T) {
	for _, tc := range versionQueryCases(t) {
		t.Run(tc.Query, func(t *testing.T) {
			query, err := url.ParseQuery(tc.Query)
			require.NoError(t, err)
			require.Equal(t, tc.Notes, auditDataFlag(query, "notes"))
			require.Equal(t, tc.Original, auditDataFlag(query, "original"))
			require.Equal(t, tc.Noindex, auditQueryNoindex(query))
			canonical := "/apps/app/versions"
			if tc.Canonical != "" {
				canonical += "?" + tc.Canonical
			}
			require.Equal(t, canonical, packagePagePath(seo.Facts{Token: "app"}, "en-US", "versions", query))
		})
	}
	for _, path := range []string{"/apps/ORDINARY-APP", "/APPS/ORDINARY-APP/versions", "/JA/apps/ORDINARY-APP", "/EN/apps/ORDINARY-APP"} {
		route := parseAuditRoute(path)
		require.True(t, route.Package && route.Valid)
		require.Equal(t, "ordinary-app", route.Token, path)
	}
}

type packagePathCase struct {
	Path, Token, Kind, Locale string
	Valid                     bool
}

func packagePathCases(t *testing.T) []packagePathCase {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/seo/package-path.json")
	require.NoError(t, err)
	var cases []packagePathCase
	require.NoError(t, json.Unmarshal(raw, &cases))
	return cases
}

func TestPackagePathsMatchWebNormalization(t *testing.T) {
	for _, tc := range packagePathCases(t) {
		t.Run(tc.Path, func(t *testing.T) {
			parsed, err := url.Parse("https://fixture.example" + tc.Path)
			require.NoError(t, err)
			route := parseAuditRoute(parsed.EscapedPath())
			require.True(t, route.Package)
			require.Equal(t, tc.Valid, route.Valid)
			require.Equal(t, tc.Token, route.Token)
			require.Equal(t, tc.Kind, route.Kind)
			require.Equal(t, tc.Locale, route.Locale)
		})
	}
}
