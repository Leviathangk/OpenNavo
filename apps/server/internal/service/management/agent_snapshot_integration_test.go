//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestReleaseNotesPreviewNeverSnapshotsReleaseWithPackageID(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	// Release 910 belongs to another app; package 910 has no editorial notes for 2.0.2 yet.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,homepage) VALUES
  (910,'cask','notes-target','notes-target','homebrew/cask','Notes Target','2.0.2','2.0.2','{}','hash','https://vendor.example.test'),
  (911,'cask','notes-other','notes-other','homebrew/cask','Notes Other','1.0','1.0','{}','hash','https://other.example.test');
 INSERT INTO package_versions(package_id,version,version_base) VALUES(910,'2.0.2','2.0.2');
 INSERT INTO releases(id,package_id,source,source_key,version,title,body_markdown,source_locale) VALUES(910,911,'editorial','1.0','1.0','Notes of another app','Notes of another app','en-US')`).Error)
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "maintenance", EntityKey: "pkg:910", DedupeKey: "catalog:pkg:910", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	claimed, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, nil)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	text := "Version 2.0.2 fixes scrolling."
	evidence := agentgate.Evidence{EntityKey: "pkg:910", SourceURL: "https://vendor.example.test/notes", FinalURL: "https://vendor.example.test/notes", BodyText: text, Excerpt: text, BodyHash: seotasks.Hash([]byte(text)), FetchedAt: time.Now(), ValidUntil: time.Now().Add(time.Hour), Authority: "official", FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: seotasks.Hash([]byte("fixture"))}
	require.NoError(t, st.DB.Create(&evidence).Error)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "upsertReleaseNotes", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"changelog:release:edit"}, RequiredPermissions: []string{"changelog:release:edit"}, DryRun: true, Execution: &domain.AgentExecution{TaskID: claimed[0].ID, Fencing: claimed[0].Fencing, IdempotencyKey: uuid.NewString(), EvidenceIDs: []int64{evidence.ID}}}
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	preview, err := svc.Execute(domain.WithOperation(ctx, op), "UpsertReleaseNotes", Input{ID: 910, Version: "2.0.2", Body: map[string]any{"sourceLocale": "en-US", "summary": text, "sections": []any{}}})
	require.NoError(t, err)
	require.Equal(t, "passed", preview.(map[string]any)["validation"])
	raw, err := json.Marshal(preview)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Notes of another app", "the preview must not show another app's release")
	require.Contains(t, string(raw), text)
}

func TestEvidenceRejectionNamesTheRuleAndIsTraced(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,homepage) VALUES(912,'cask','evidence-app','evidence-app','homebrew/cask','Evidence App','1','1','{}','hash','https://vendor.example.test')`).Error)
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "maintenance", EntityKey: "pkg:912", DedupeKey: "catalog:pkg:912", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	claimed, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, nil)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "createSeoEvidence", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"seo:task:handle"}, RequiredPermissions: []string{"seo:task:handle"}}
	svc := &Service{Store: st, Evidence: agentgate.NewEvidenceService(st.DB, "fixture")}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "CreateSeoEvidence", Input{Body: map[string]any{"taskId": claimed[0].ID, "fencing": claimed[0].Fencing, "idempotencyKey": uuid.NewString(), "entityKey": "pkg:912", "url": "https://mirror.example.test/notes", "excerpt": "Evidence App"}})
	var appErr *domain.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, domain.CodeValidation, appErr.Code)
	require.Equal(t, "rejected: source_not_official", appErr.Msg)
	var reason string
	require.NoError(t, st.DB.Raw("SELECT reason FROM seo_gate_events WHERE task_id=? AND operation='CreateSeoEvidence' AND result='rejected'", claimed[0].ID).Scan(&reason).Error)
	require.Equal(t, "source_not_official", reason)
}
