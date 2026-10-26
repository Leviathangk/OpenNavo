//go:build integration

package management

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

// evidenceFixture stores a captured source excerpt without fetching it.
func evidenceFixture(t *testing.T, st *store.Store, entity, body, link, authority string) agentgate.Evidence {
	t.Helper()
	e := agentgate.Evidence{EntityKey: entity, SourceURL: link, FinalURL: link, BodyText: body, Excerpt: body, BodyHash: agentgate.Hash([]byte(body)), FetchedAt: time.Now().UTC(), ValidUntil: time.Now().Add(time.Hour), Authority: authority, FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: agentgate.Hash([]byte("fixture"))}
	require.NoError(t, st.DB.Create(&e).Error)
	return e
}

// jsonBody turns a typed request into the decoded JSON body handlers receive.
func jsonBody(t *testing.T, in any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var b map[string]any
	require.NoError(t, json.Unmarshal(raw, &b))
	return b
}
