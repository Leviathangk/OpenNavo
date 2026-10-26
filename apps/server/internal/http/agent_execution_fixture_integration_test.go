//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

const executionFixtureText = "Mac application guide. Follow these steps on your Mac. Original introduction."
const executionFixtureURL = "https://vendor.example.test/guide"

type httpExecutionFixture struct {
	t  *testing.T
	st *store.Store
}

// evidence stores a captured source excerpt for the entity, as an editor's capture would.
func (f httpExecutionFixture) evidence(entity string) agentgate.Evidence {
	f.t.Helper()
	var proof agentgate.Evidence
	require.NoError(f.t, f.st.DB.Where("entity_key=? AND valid_until>now() AND revoked_at IS NULL", entity).Order("id DESC").FirstOrInit(&proof).Error)
	if proof.ID == 0 {
		proof = agentgate.Evidence{EntityKey: entity, SourceURL: executionFixtureURL, FinalURL: executionFixtureURL, BodyText: executionFixtureText, Excerpt: executionFixtureText, BodyHash: agentgate.Hash([]byte(executionFixtureText)), FetchedAt: time.Now().UTC(), ValidUntil: time.Now().Add(time.Hour), Authority: "reference", FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: agentgate.Hash([]byte(entity))}
		require.NoError(f.t, f.st.DB.Create(&proof).Error)
	}
	return proof
}

// attach sends the current revision of the edited entity and a fresh retry key.
func (f httpExecutionFixture) attach(r *http.Request, entity string) {
	f.t.Helper()
	kind, key, _ := strings.Cut(entity, ":")
	var revision int64
	if kind == "article" && !strings.HasPrefix(key, "new:") {
		require.NoError(f.t, f.st.DB.Raw("SELECT revision FROM articles WHERE id=?", key).Scan(&revision).Error)
	} else if !strings.HasPrefix(key, "new:") && kind != "asset" {
		if kind == "pkg" {
			kind = "package"
		}
		require.NoError(f.t, f.st.DB.Raw("SELECT coalesce(max(id),0) FROM content_revisions WHERE entity=? AND object_key=?", kind, key).Scan(&revision).Error)
	}
	r.Header.Set("X-Expected-Revision", strconv.FormatInt(revision, 10))
	r.Header.Set("Idempotency-Key", uuid.NewString())
}

// Scope only the entities deliberately exercised by the legacy HTTP lifecycle fixture.
func (f httpExecutionFixture) scope(path, body string) string {
	f.t.Helper()
	path = strings.Split(path, "?")[0]
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var fields map[string]any
	if body != "" {
		require.NoError(f.t, json.Unmarshal([]byte(body), &fields))
	}
	if path == "/translations/fix" {
		ref := fields["ref"].(map[string]any)
		kind := ref["entity"].(string)
		if kind == "package" {
			kind = "pkg"
		}
		return fmt.Sprintf("%s:%.0f", kind, ref["id"].(float64))
	}
	if len(parts) >= 3 && parts[0] == "content" {
		var row struct{ Entity, ObjectKey string }
		table, where, arg := "content_revisions", "id=?", parts[2]
		if parts[1] == "requests" {
			where = "request_id=?"
		}
		require.NoError(f.t, f.st.DB.Table(table).Select("entity,object_key").Where(where, arg).First(&row).Error)
		if row.Entity == "package" {
			row.Entity = "pkg"
		}
		return row.Entity + ":" + row.ObjectKey
	}
	kind := map[string]string{"glossary": "glossary", "articles": "article", "article-authors": "article_author", "packages": "pkg", "collections": "collection"}[parts[0]]
	require.NotEmpty(f.t, kind, path)
	if len(parts) > 1 {
		return kind + ":" + parts[1]
	}
	key := fields["slug"]
	if parts[0] == "glossary" {
		key = fields["term"]
	}
	require.NotNil(f.t, key, path)
	return kind + ":new:" + key.(string)
}
