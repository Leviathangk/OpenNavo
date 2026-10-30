//go:build integration

package management

import (
	"context"
	"fmt"
	"path"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// MCP's enabled switch sends the same hidden patch as the admin UI. Exercise
// that shared mutation through the agent boundary, including its side effects.
func TestMCPPackageVisibilityUsesAdminWritePath(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(701,'visibility-fixture','isolated-test-only');
 INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,installs_30d,rank_30d,disabled,stale_disabled) VALUES
 (701,'cask','pearcleaner','pearcleaner','homebrew/cask','Pearcleaner','1','1','{}','fixture','A useful cleanup application',50,1,false,false),
 (702,'link','visibility-link','visibility-link','','Link fixture','1','1','{}','fixture','A useful link application',50,1,false,false),
 (703,'cask','upstream-disabled','upstream-disabled','homebrew/cask','Disabled fixture','1','1','{}','fixture','An unavailable application',50,1,true,false),
 (704,'cask','stale-disabled','stale-disabled','homebrew/cask','Stale fixture','1','1','{}','fixture','A stale application',50,1,false,true);
 INSERT INTO package_meta(package_id,hidden,developer,notes) SELECT id,true,'Fixture developer','Keep this note' FROM packages`).Error)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://visibility.example"}}
	public := &catalog.PublicService{Store: st}
	actorID := int64(701)
	counts := func() map[string]int64 {
		t.Helper()
		out := map[string]int64{}
		for _, table := range []string{"audit_logs", "content_revisions", "catalog_changes", "job_outbox", "url_notify_outbox", "agent_write_events", "agent_write_controls"} {
			var count int64
			require.NoError(t, st.DB.Table(table).Count(&count).Error)
			out[table] = count
		}
		return out
	}
	listed := func(token string) bool {
		t.Helper()
		sitemap, err := public.Sitemap(ctx, "apps", 1, 100)
		require.NoError(t, err)
		for _, record := range sitemap.Records {
			if record.Token == token {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		id    int64
		token string
	}{{701, "pearcleaner"}, {702, "visibility-link"}} {
		t.Run(tc.token, func(t *testing.T) {
			for _, hidden := range []bool{false, true} {
				op := &domain.Operation{RequestID: uuid.NewString(), Name: "updatePackageMeta", Tool: "package_set_enabled", ActorID: &actorID, AgentClientID: 1, AgentTokenID: 2,
					Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, DryRun: true, Execution: &domain.AgentExecution{IdempotencyKey: uuid.NewString()}}
				in := Input{ID: tc.id, Body: map[string]any{"hidden": hidden, "visibilityReason": "Visibility fixture"}, Actor: store.AuditActor{ID: &actorID}}
				before := counts()
				preview, err := svc.Execute(domain.WithOperation(ctx, op), "UpdatePackageMeta", in)
				require.NoError(t, err)
				view := preview.(map[string]any)
				require.Equal(t, true, view["dryRun"])
				changes := view["changes"].([]any)
				require.Len(t, changes, 1)
				change := changes[0].(map[string]any)
				require.Equal(t, !hidden, change["before"].(store.HistoryState)["package_meta"][0]["hidden"])
				require.Equal(t, hidden, change["after"].(store.HistoryState)["package_meta"][0]["hidden"])
				require.Equal(t, before, counts(), "preview must not persist logs, content or invalidations")
				require.Equal(t, hidden, listed(tc.token), "preview must not change sitemap membership")
				var stored bool
				require.NoError(t, st.DB.Raw("SELECT hidden FROM package_meta WHERE package_id=?", tc.id).Scan(&stored).Error)
				require.Equal(t, !hidden, stored)

				// Reuse the preview's key and revision for the live write.
				op.DryRun = false
				op.Execution.ExpectedRevisionSet = true
				op.Execution.ExpectedRevision = toInt64(view["expectedRevision"])
				_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdatePackageMeta", in)
				require.NoError(t, err)
				require.Equal(t, !hidden, listed(tc.token))
				var audit struct {
					ActorID int64
					After   string
				}
				require.NoError(t, st.DB.Raw("SELECT actor_id,after::text FROM audit_logs WHERE request_id=? AND action='UpdatePackageMeta'", op.RequestID).Scan(&audit).Error)
				require.Equal(t, actorID, audit.ActorID)
				require.JSONEq(t, fmt.Sprintf(`{"hidden":%t,"visibilityReason":"Visibility fixture"}`, hidden), audit.After)
				var revisions int64
				require.NoError(t, st.DB.Table("content_revisions").Where("request_id=? AND actor_type='agent' AND operation_id='updatePackageMeta'", op.RequestID).Count(&revisions).Error)
				require.EqualValues(t, 1, revisions)
				var patterns []string
				require.NoError(t, st.DB.Raw("SELECT jsonb_array_elements_text(payload) FROM job_outbox WHERE unique_key=?", "cache:invalidate:agent-write:"+op.RequestID).Scan(&patterns).Error)
				for _, key := range []string{"c:pkg:sitemap:apps:1:100", "c:home:en-US", "c:pkg:detail:cask:pearcleaner:en-US"} {
					covered := false
					for _, pattern := range patterns {
						matches, err := path.Match(pattern, key)
						require.NoError(t, err)
						covered = covered || matches
					}
					require.True(t, covered, "invalidation must cover %s", key)
				}
				var changesCount int64
				require.NoError(t, st.DB.Table("catalog_changes").Where("package_id=? AND reason='content' AND locales IS NULL", tc.id).Count(&changesCount).Error)
				require.Positive(t, changesCount, "the normal catalog notification feed must receive the change")

				after := counts()
				_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdatePackageMeta", in)
				require.NoError(t, err)
				require.Equal(t, after, counts(), "an identical retry must not write twice")
			}
		})
	}
	for _, id := range []int64{703, 704} {
		_, err := svc.Execute(ctx, "UpdatePackageMeta", Input{ID: id, Body: map[string]any{"hidden": false}})
		require.NoError(t, err)
		detail, err := st.AdminPackage(ctx, id)
		require.NoError(t, err)
		view := hermesMap(detail)
		require.Equal(t, false, view["hidden"])
		require.Equal(t, true, view["disabled"], "visibility must not override upstream/stale disabling")
	}
	_, total, err := st.PublicPackages(ctx, store.PublicFilter{Locale: "en-US", Current: 1, Size: 100})
	require.NoError(t, err)
	require.Zero(t, total, "default catalog listings still exclude upstream/stale disabled apps")
	var preserved int64
	require.NoError(t, st.DB.Table("package_meta").Where("developer='Fixture developer' AND notes='Keep this note'").Count(&preserved).Error)
	require.EqualValues(t, 4, preserved, "visibility patches preserve unrelated metadata")
}
