//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOExecutionPermissionsDatabaseDefaultsAndTunerIsolation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st}
	client, err := s.Execute(ctx, "CreateAgentClient", Input{Body: map[string]any{"name": "SEO fixture client"}})
	require.NoError(t, err)
	encoded, err := json.Marshal(client)
	require.NoError(t, err)
	var created struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(encoded, &created))
	id := created.ID
	result, err := s.Execute(ctx, "CreateAgentToken", Input{ID: id, Body: map[string]any{"name": "Fixture executor"}})
	require.NoError(t, err)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var view struct {
		Token struct {
			Permissions []string `json:"permissions"`
		} `json:"token"`
	}
	require.NoError(t, json.Unmarshal(raw, &view))
	require.Len(t, view.Token.Permissions, len(agent.Grantable)-1)
	require.NotContains(t, view.Token.Permissions, "seo:automation:tune")
	for _, permission := range []string{"seo:insight:view", "seo:keyword:edit", "seo:snippet:propose", "seo:task:handle"} {
		require.Contains(t, view.Token.Permissions, permission)
		_, err := s.Execute(ctx, "CreateAgentToken", Input{ID: id, Body: map[string]any{"name": "Fixture scoped", "permissions": []any{permission}}})
		require.NoError(t, err, permission)
	}
	_, err = s.Execute(ctx, "CreateAgentToken", Input{ID: id, Body: map[string]any{"name": "Fixture tuner", "permissions": []any{"seo:automation:tune", "seo:insight:view"}}})
	require.NoError(t, err)
	_, err = s.Execute(ctx, "CreateAgentToken", Input{ID: id, Body: map[string]any{"name": "Mixed denied", "permissions": []any{"seo:automation:tune", "seo:keyword:edit"}}})
	require.Error(t, err)
	require.Error(t, st.DB.Exec("UPDATE agent_tokens SET permissions=ARRAY['seo:automation:tune','seo:keyword:edit'] WHERE client_id=?", id).Error, "database must also refuse mixed credentials")
}
