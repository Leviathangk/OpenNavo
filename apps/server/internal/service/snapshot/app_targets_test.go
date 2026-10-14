package snapshot

import (
	"encoding/json"
	"testing"
	"time"

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
