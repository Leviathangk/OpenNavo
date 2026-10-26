//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
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
	text := "Version 2.0.2 fixes scrolling."
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "upsertReleaseNotes", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"changelog:release:edit"}, RequiredPermissions: []string{"changelog:release:edit"}, DryRun: true, Execution: &domain.AgentExecution{IdempotencyKey: uuid.NewString()}}
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	preview, err := svc.Execute(domain.WithOperation(ctx, op), "UpsertReleaseNotes", Input{ID: 910, Version: "2.0.2", Body: map[string]any{"sourceLocale": "en-US", "summary": text, "sections": []any{}}})
	require.NoError(t, err)
	require.Equal(t, "passed", preview.(map[string]any)["validation"])
	raw, err := json.Marshal(preview)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Notes of another app", "the preview must not show another app's release")
	require.Contains(t, string(raw), text)
}
