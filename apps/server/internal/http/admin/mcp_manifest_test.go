package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/stretchr/testify/require"
)

func TestMCPGeneratedOperationsMatchAgentPolicies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "mcp", "src", "opennavo_mcp", "gen", "operations.json"))
	require.NoError(t, err)
	var generated map[string]struct {
		Method, Path string
		Policy       AgentPolicy
	}
	require.NoError(t, json.Unmarshal(data, &generated))
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	policies := AgentPolicies(spec)
	require.Len(t, generated, 93)
	require.Len(t, policies, len(generated))
	parameters := regexp.MustCompile(`\{([^}]+)\}`)
	for operation, item := range generated {
		route := item.Method + " " + parameters.ReplaceAllString(item.Path, ":$1")
		actual, ok := policies[route]
		require.True(t, ok, "route missing: %s", route)
		require.Equal(t, operation, actual.Operation)
		require.ElementsMatch(t, item.Policy.Permissions, actual.Permissions)
		require.Equal(t, item.Policy.DeleteAction, actual.DeleteAction)
		require.Equal(t, item.Policy.Introspection, actual.Introspection)
	}
}
