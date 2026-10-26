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

func TestAgentTokensDefaultToGrantablePermissionsAndRefuseRetiredOnes(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st}
	client, err := s.Execute(ctx, "CreateAgentClient", Input{Body: map[string]any{"name": "Permission fixture client"}})
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
	require.ElementsMatch(t, agent.Grantable, view.Token.Permissions, "the database default grants every grantable permission")
	_, err = s.Execute(ctx, "CreateAgentToken", Input{ID: id, Body: map[string]any{"name": "Index reader", "permissions": []any{"site:index:view"}}})
	require.NoError(t, err)
	for _, retired := range []string{"seo:insight:view", "seo:keyword:edit", "seo:snippet:propose", "seo:task:handle", "seo:automation:tune"} {
		_, err := s.Execute(ctx, "CreateAgentToken", Input{ID: id, Body: map[string]any{"name": "Retired", "permissions": []any{retired}}})
		require.Error(t, err, retired)
	}
	// For one release the database still accepts the old codes, which the previous admin
	// writes while both releases run; it refuses anything else.
	require.NoError(t, st.DB.Exec("UPDATE agent_tokens SET permissions=ARRAY['seo:insight:view'] WHERE client_id=?", id).Error)
	require.Error(t, st.DB.Exec("UPDATE agent_tokens SET permissions=ARRAY['agent:manage'] WHERE client_id=?", id).Error)
}
