//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

const executionFixtureText = "Mac application guide. Follow these steps on your Mac. Original introduction."
const executionFixtureURL = "https://vendor.example.test/guide"

type executionFixtureTransport struct{}

func (executionFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader(executionFixtureText)), Request: r}, nil
}

type httpExecutionFixture struct {
	t     *testing.T
	st    *store.Store
	owner string
}

func (f httpExecutionFixture) evidence(ctx context.Context, entity string) (seotasks.Task, agentgate.Evidence) {
	f.t.Helper()
	tasks := &seotasks.Service{DB: f.st.DB}
	var task seotasks.Task
	require.NoError(f.t, f.st.DB.Where("entity_key=? AND state='leased' AND lease_owner=? AND lease_until>now()", entity, f.owner).FirstOrInit(&task).Error)
	if task.ID == 0 {
		_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "fixture", EntityKey: entity, DedupeKey: uuid.NewString(), RuleVersion: "http-fixture-v1", Evidence: map[string]any{}})
		require.NoError(f.t, err)
		claimed, err := tasks.Claim(ctx, f.owner, 1, []string{"fixture"})
		require.NoError(f.t, err)
		require.Len(f.t, claimed, 1)
		require.Equal(f.t, entity, claimed[0].EntityKey)
		task = claimed[0]
	}
	var proof agentgate.Evidence
	require.NoError(f.t, f.st.DB.Where("entity_key=? AND created_by=? AND valid_until>now() AND revoked_at IS NULL", entity, f.owner).Order("id DESC").FirstOrInit(&proof).Error)
	if proof.ID == 0 {
		svc := &agentgate.EvidenceService{DB: f.st.DB, HTTP: &http.Client{Transport: executionFixtureTransport{}}}
		var err error
		proof, err = svc.Capture(ctx, agentgate.Capture{TaskID: task.ID, Fencing: task.Fencing, Owner: f.owner, IdempotencyKey: uuid.NewString(), EntityKey: entity, URL: executionFixtureURL, Excerpt: executionFixtureText})
		require.NoError(f.t, err)
	}
	return task, proof
}
func (f httpExecutionFixture) attach(r *http.Request, entity string) {
	f.t.Helper()
	task, proof := f.evidence(r.Context(), entity)
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
	r.Header.Set("X-SEO-Task-ID", strconv.FormatInt(task.ID, 10))
	r.Header.Set("X-SEO-Fencing", strconv.FormatInt(task.Fencing, 10))
	r.Header.Set("X-SEO-Expected-Revision", strconv.FormatInt(revision, 10))
	r.Header.Set("X-SEO-Evidence-IDs", strconv.FormatInt(proof.ID, 10))
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
