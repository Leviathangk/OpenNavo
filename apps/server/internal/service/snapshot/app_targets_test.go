package snapshot

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestTapCatalogPreservesAppDestinations(t *testing.T) {
	raw, err := homebrew.ParseCaskSource(`cask "demo" do
version "1"
name "Demo"
sha256 :no_check
url "https://example.com/demo.zip"
app "Source.app", target: "Renamed.app"
app "folder/Other.app"
end`, homebrew.CaskSource{Tap: "example/tap", Token: "demo"}, time.Now())
	require.NoError(t, err)
	pkg, err := homebrew.Normalize("cask", raw)
	require.NoError(t, err)
	public := store.PublicPackage{Kind: "cask", Tap: "example/tap", Artifacts: pkg.Artifacts}
	result, err := item(public, public)
	require.NoError(t, err)
	require.Equal(t, []string{"Source.app", "folder/Other.app"}, result.Apps)
	require.NotNil(t, result.AppTargets)
	require.Equal(t, []string{"/Applications/Renamed.app", "/Applications/Other.app"}, *result.AppTargets)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"appTargets":["/Applications/Renamed.app","/Applications/Other.app"]`)

	public.Artifacts = json.RawMessage(`{"apps":["Source.app"]}`)
	result, err = item(public, public)
	require.NoError(t, err)
	require.Nil(t, result.AppTargets, "legacy data must not guess destinations")
}

func TestTapCatalogPreservesUpstreamToken(t *testing.T) {
	public := store.PublicPackage{Kind: "cask", Token: "stablyai-orca", FullToken: "stablyai/orca/orca", Tap: "stablyai/orca"} // #nosec G101 -- Public package names, not credentials.
	result, err := item(public, public)
	require.NoError(t, err)
	require.Equal(t, "orca", *result.BrewToken)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	var base publicapi.CatalogBaseItem
	require.NoError(t, json.Unmarshal(encoded, &base))
	require.Equal(t, "stablyai-orca", base.Token)
	require.Equal(t, "orca", *base.BrewToken)
	public.Token = "orca"
	result, err = item(public, public)
	require.NoError(t, err)
	require.Nil(t, result.BrewToken, "unchanged casks preserve their existing payload")
}
