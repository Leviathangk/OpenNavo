//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestKeywordRevisionDetectsHumanWriteAndTaskTraceExposesRevision(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "research", EntityKey: "site:keywords", DedupeKey: "keyword-revision-fixture", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, nil)
	require.NoError(t, err)
	require.Len(t, leased, 1)
	body := map[string]any{"records": []any{map[string]any{"keyword": "example mac", "source": "gsc", "engine": "google", "country": "US", "language": "en-US", "device": "all", "period": "2026-10", "volumeState": "unknown", "observedAt": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}}, "targets": []any{}}
	_, err = svc.Execute(domain.WithOperation(ctx, &domain.Operation{Permissions: []string{"*"}}), "UpsertSeoKeywords", Input{Body: body})
	require.NoError(t, err)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "upsertSeoKeywords", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"seo:keyword:edit", "seo:task:handle"}, RequiredPermissions: []string{"seo:keyword:edit"}, Execution: &domain.AgentExecution{TaskID: leased[0].ID, Fencing: leased[0].Fencing, IdempotencyKey: uuid.NewString(), ExpectedRevisionSet: true}}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpsertSeoKeywords", Input{Body: body})
	require.Error(t, err)
	trace, err := svc.Execute(domain.WithOperation(ctx, op), "GetSeoTask", Input{ID: leased[0].ID})
	require.NoError(t, err)
	revision := trace.(map[string]any)["currentRevision"].(int64)
	require.Positive(t, revision)
	op.Execution.ExpectedRevision = revision
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpsertSeoKeywords", Input{Body: body})
	require.NoError(t, err)
}

func TestTaskTraceForPackageTaskListsPackageRevisions(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	tasks := &seotasks.Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(834,'cask','trace-app','trace-app','homebrew/cask','Trace App','1','1','{}','hash');
 INSERT INTO content_revision_requests(request_id) VALUES('fixture-trace');
 INSERT INTO content_revisions(entity,object_key,version,locale,action,operation_id,required_permissions,actor_type,request_id,before_data,after_data)
 VALUES('package','834',1,'en-US','update','UpdatePackage',ARRAY['catalog:package:edit'],'agent','fixture-trace','{}','{}')`).Error)
	var revision int64
	require.NoError(t, st.DB.Raw(`SELECT max(id) FROM content_revisions WHERE entity='package' AND object_key='834'`).Scan(&revision).Error)
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "maintenance", EntityKey: "pkg:834", DedupeKey: "catalog:pkg:834", RuleVersion: "fixture", Evidence: map[string]any{"token": "trace-app"}})
	require.NoError(t, err)
	leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, nil)
	require.NoError(t, err)
	require.Len(t, leased, 1)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "getSeoTask", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"seo:task:handle"}, RequiredPermissions: []string{"seo:task:handle"}}
	trace, err := svc.Execute(domain.WithOperation(ctx, op), "GetSeoTask", Input{ID: leased[0].ID})
	require.NoError(t, err)
	require.Equal(t, revision, trace.(map[string]any)["currentRevision"])
	require.Equal(t, map[string]int64{"package:834": revision}, trace.(map[string]any)["revisions"])
}
