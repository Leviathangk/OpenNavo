//go:build integration

package management

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestRevertRequestNormalEditThenClearDisabled(t *testing.T) {
	for _, tc := range []struct {
		name          string
		disabled      bool
		staleDisabled bool
		repeatClear   bool
	}{
		{name: "upstream_disabled", disabled: true},
		{name: "stale_disabled", staleDisabled: true},
		{name: "earliest_clear_snapshot", disabled: true, staleDisabled: true, repeatClear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewSEOStore(t)
			actorID := int64(804)
			require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(804,'merged-history-fixture','isolated-test-only');
INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,download_size,disable_date,disable_reason,disable_replacement)
VALUES(804,'cask','merged-history','merged-history','homebrew/cask','Merged history','1','1','{}','fixture',100,'2026-01-01','unsafe','cask:replacement');
INSERT INTO package_meta(package_id) VALUES(804)`).Error)
			require.NoError(t, st.DB.Exec("UPDATE packages SET disabled=?,stale_disabled=? WHERE id=804", tc.disabled, tc.staleDisabled).Error)
			ref := content.Ref{Entity: "package", ID: 804}
			before, err := st.HistorySnapshotWithPackageLifecycle(ctx, ref)
			require.NoError(t, err)
			svc := &Service{Store: st}
			requestID := uuid.NewString()
			op := &domain.Operation{RequestID: requestID, Name: "updatePackageMeta", ActorID: &actorID, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, AllowDelete: true}
			requestCtx := domain.WithOperation(ctx, op)
			write := func(body map[string]any) {
				t.Helper()
				_, err := svc.Execute(requestCtx, "UpdatePackageMeta", Input{ID: 804, Body: body, Actor: store.AuditActor{ID: &actorID}})
				require.NoError(t, err)
			}

			// Both real service writes retain the same operation request ID.
			// The normal edit deliberately captures no lifecycle fields.
			write(map[string]any{"hidden": true, "downloadSize": int64(200)})
			write(map[string]any{"clearDisabled": true})
			if tc.repeatClear {
				// A later clear captures the already-cleared lifecycle. Revert
				// must use the first clear's before, not the latest clear's before.
				write(map[string]any{"clearDisabled": true, "downloadSize": int64(300)})
			}

			var revisions []revisionRow
			require.NoError(t, st.DB.Raw("SELECT before_data,after_data FROM content_revisions WHERE request_id=? AND entity='package' ORDER BY id", requestID).Scan(&revisions).Error)
			count := 2
			if tc.repeatClear {
				count++
			}
			require.Len(t, revisions, count)
			normalBefore, err := revisionState(revisions[0].BeforeData)
			require.NoError(t, err)
			require.False(t, store.HistoryStateIncludesPackageLifecycle(normalBefore))
			clearBefore, err := revisionState(revisions[1].BeforeData)
			require.NoError(t, err)
			require.True(t, store.HistoryStateIncludesPackageLifecycle(clearBefore))
			require.Equal(t, tc.disabled, clearBefore["packages"][0]["disabled"])
			cleared, err := st.HistorySnapshotWithPackageLifecycle(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, false, cleared["packages"][0]["disabled"])
			require.NotNil(t, cleared["packages"][0]["disabled_override_at"])
			t.Logf("shared request has %d revisions; normal before omits lifecycle, first clear before disabled=%v", count, tc.disabled)

			_, err = svc.Execute(ctx, "RevertRequest", Input{RequestID: requestID, Body: map[string]any{}})
			require.NoError(t, err)
			after, err := st.HistorySnapshotWithPackageLifecycle(ctx, ref)
			require.NoError(t, err)
			var hidden bool
			require.NoError(t, st.DB.Raw("SELECT hidden FROM package_meta WHERE package_id=804").Scan(&hidden).Error)
			require.False(t, hidden, "the normal edit must also be reverted")
			require.Equal(t, before["packages"], after["packages"], "restore lifecycle booleans, the complete override group, and the original download size")
		})
	}
}

func TestClearDisabledRevisionRestoresPackageLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	actorID := int64(801)
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(801,'history-lifecycle-fixture','isolated-test-only');
INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,disabled,disable_date,disable_reason,disable_replacement)
VALUES(801,'cask','history-lifecycle','history-lifecycle','homebrew/cask','History lifecycle','1','1','{}','fixture',true,'2026-01-01','unsafe','cask:replacement');
INSERT INTO package_meta(package_id) VALUES(801)`).Error)
	svc := &Service{Store: st}
	requestID := uuid.NewString()
	op := &domain.Operation{RequestID: requestID, Name: "updatePackageMeta", ActorID: &actorID, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, AllowDelete: true}
	in := Input{ID: 801, Body: map[string]any{"hidden": true, "clearDisabled": true}, Actor: store.AuditActor{ID: &actorID}}
	_, err := svc.Execute(domain.WithOperation(ctx, op), "UpdatePackageMeta", in)
	require.NoError(t, err)

	var revisionID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM content_revisions WHERE request_id=? AND entity='package' ORDER BY id DESC LIMIT 1", requestID).Scan(&revisionID).Error)
	require.Positive(t, revisionID)
	var version sql.NullString
	require.NoError(t, st.DB.Raw("SELECT disabled_override_version FROM packages WHERE id=801").Scan(&version).Error)
	require.Equal(t, "1", version.String)

	// Simulate later lifecycle data for fields changed by the explicit clear.
	// Fields the clear left untouched remain at their captured values so this
	// test focuses on restoring the changed portion of the snapshot.
	require.NoError(t, st.DB.Exec(`UPDATE packages SET disabled=true,disabled_override_at=NULL,disabled_override_date=NULL,disabled_override_reason=NULL,disabled_override_replacement=NULL,disabled_override_stale=false,disabled_override_version=NULL WHERE id=801`).Error)
	_, err = svc.Execute(ctx, "RestoreRevision", Input{ID: revisionID, Body: map[string]any{}})
	require.NoError(t, err)

	var lifecycle struct {
		Disabled            bool
		StaleDisabled       bool
		DisableDate         *string
		DisableReason       *string
		DisableReplacement  *string
		OverrideAt          sql.NullTime   `gorm:"column:disabled_override_at"`
		OverrideDate        sql.NullString `gorm:"column:disabled_override_date"`
		OverrideReason      sql.NullString `gorm:"column:disabled_override_reason"`
		OverrideReplacement sql.NullString `gorm:"column:disabled_override_replacement"`
		OverrideStale       bool           `gorm:"column:disabled_override_stale"`
		OverrideVersion     sql.NullString `gorm:"column:disabled_override_version"`
	}
	require.NoError(t, st.DB.Raw(`SELECT disabled,stale_disabled,disable_date::text,disable_reason,disable_replacement,disabled_override_at,disabled_override_date::text,disabled_override_reason,disabled_override_replacement,disabled_override_stale,disabled_override_version FROM packages WHERE id=801`).Scan(&lifecycle).Error)
	require.False(t, lifecycle.Disabled)
	require.False(t, lifecycle.StaleDisabled)
	require.Equal(t, "2026-01-01", *lifecycle.DisableDate)
	require.Equal(t, "unsafe", *lifecycle.DisableReason)
	require.Equal(t, "cask:replacement", *lifecycle.DisableReplacement)
	require.True(t, lifecycle.OverrideAt.Valid)
	require.Equal(t, "2026-01-01", lifecycle.OverrideDate.String)
	require.Equal(t, "unsafe", lifecycle.OverrideReason.String)
	require.Equal(t, "cask:replacement", lifecycle.OverrideReplacement.String)
	require.False(t, lifecycle.OverrideStale)
	require.Equal(t, "1", lifecycle.OverrideVersion.String)

	// Undoing the same request restores the disabled upstream state and removes
	// the manual override as well.
	_, err = svc.Execute(ctx, "RevertRequest", Input{RequestID: requestID, Body: map[string]any{}})
	require.NoError(t, err)
	require.NoError(t, st.DB.Raw(`SELECT disabled,stale_disabled,disable_date::text,disable_reason,disable_replacement,disabled_override_at,disabled_override_version FROM packages WHERE id=801`).Scan(&lifecycle).Error)
	require.True(t, lifecycle.Disabled)
	require.False(t, lifecycle.StaleDisabled)
	require.Equal(t, "2026-01-01", *lifecycle.DisableDate)
	require.Equal(t, "unsafe", *lifecycle.DisableReason)
	require.Equal(t, "cask:replacement", *lifecycle.DisableReplacement)
	require.False(t, lifecycle.OverrideAt.Valid)
	require.False(t, lifecycle.OverrideVersion.Valid)
}

func TestActivePackageUndoDoesNotRestoreUnchangedLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	actorID := int64(802)
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(802,'active-history-fixture','isolated-test-only');
INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,download_size,disabled,stale_disabled)
VALUES(802,'cask','active-history','active-history','homebrew/cask','Active history','1','1','{}','fixture',100,false,false);
INSERT INTO package_meta(package_id) VALUES(802)`).Error)
	svc := &Service{Store: st}
	requestID := uuid.NewString()
	op := &domain.Operation{RequestID: requestID, Name: "updatePackageMeta", ActorID: &actorID, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, AllowDelete: true}
	in := Input{ID: 802, Body: map[string]any{"downloadSize": int64(200), "clearDisabled": true}, Actor: store.AuditActor{ID: &actorID}}
	_, err := svc.Execute(domain.WithOperation(ctx, op), "UpdatePackageMeta", in)
	require.NoError(t, err)

	// The active package still has a full lifecycle snapshot because the clear
	// request asked for it, even though none of those fields changed.
	var disabled bool
	var downloadSize int64
	require.NoError(t, st.DB.Raw("SELECT disabled,download_size FROM packages WHERE id=802").Row().Scan(&disabled, &downloadSize))
	require.False(t, disabled)
	require.Equal(t, int64(200), downloadSize)

	// Simulate a later upstream disable, including the fields catalog sync would
	// clear when the upstream signature becomes authoritative again.
	require.NoError(t, st.DB.Exec(`UPDATE packages
SET disabled=true,stale_disabled=false,disable_date='2026-02-02',disable_reason='later',disable_replacement='cask:later',disabled_override_at=NULL,disabled_override_date=NULL,disabled_override_reason=NULL,disabled_override_replacement=NULL,disabled_override_stale=false,disabled_override_version=NULL
WHERE id=802`).Error)

	var revisionID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM content_revisions WHERE request_id=? AND entity='package' ORDER BY id DESC LIMIT 1", requestID).Scan(&revisionID).Error)
	require.Positive(t, revisionID)
	// Restoring the captured revision must leave lifecycle fields that the
	// revision did not change under the later upstream disable.
	_, err = svc.Execute(ctx, "RestoreRevision", Input{ID: revisionID, Body: map[string]any{}})
	require.NoError(t, err)
	require.NoError(t, st.DB.Raw("SELECT disabled,download_size FROM packages WHERE id=802").Row().Scan(&disabled, &downloadSize))
	require.True(t, disabled)
	require.Equal(t, int64(200), downloadSize)
}

func TestRestoreRevisionRestoresWholeDisabledOverrideGroup(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	actorID := int64(803)
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(803,'override-history-fixture','isolated-test-only');
INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,disabled,stale_disabled)
VALUES(803,'cask','override-history','override-history','homebrew/cask','Override history','1','1','{}','fixture',true,true);
INSERT INTO package_meta(package_id) VALUES(803)`).Error)
	svc := &Service{Store: st}
	permissions := []string{"catalog:package:edit"}
	clear := func(requestID string, hidden bool) {
		op := &domain.Operation{RequestID: requestID, Name: "updatePackageMeta", ActorID: &actorID, Permissions: permissions, RequiredPermissions: permissions, AllowDelete: true}
		in := Input{ID: 803, Body: map[string]any{"hidden": hidden, "clearDisabled": true}, Actor: store.AuditActor{ID: &actorID}}
		_, err := svc.Execute(domain.WithOperation(ctx, op), "UpdatePackageMeta", in)
		require.NoError(t, err)
	}

	requestID1 := uuid.NewString()
	clear(requestID1, true)
	var revisionID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM content_revisions WHERE request_id=? AND entity='package' ORDER BY id DESC LIMIT 1", requestID1).Scan(&revisionID).Error)
	require.Positive(t, revisionID)

	type overrideState struct {
		At          sql.NullTime   `gorm:"column:disabled_override_at"`
		Date        sql.NullString `gorm:"column:disabled_override_date"`
		Reason      sql.NullString `gorm:"column:disabled_override_reason"`
		Replacement sql.NullString `gorm:"column:disabled_override_replacement"`
		Stale       bool           `gorm:"column:disabled_override_stale"`
		Version     sql.NullString `gorm:"column:disabled_override_version"`
	}
	readOverride := func() overrideState {
		var state overrideState
		require.NoError(t, st.DB.Raw(`SELECT disabled_override_at,disabled_override_date::text,disabled_override_reason,disabled_override_replacement,disabled_override_stale,disabled_override_version FROM packages WHERE id=803`).Scan(&state).Error)
		return state
	}
	target := readOverride()
	require.True(t, target.At.Valid)
	require.False(t, target.Date.Valid)
	require.False(t, target.Reason.Valid)
	require.False(t, target.Replacement.Valid)
	require.True(t, target.Stale)
	require.Equal(t, "1", target.Version.String)

	// The second clear writes a new override payload while leaving revision #1's
	// target snapshot available for the restore below.
	require.NoError(t, st.DB.Exec(`UPDATE packages
SET disabled=true,stale_disabled=false,version='2',disable_date='2026-02-02',disable_reason='later',disable_replacement='cask:later'
WHERE id=803`).Error)
	clear(uuid.NewString(), false)
	later := readOverride()
	require.Equal(t, "2026-02-02", later.Date.String)
	require.Equal(t, "later", later.Reason.String)
	require.Equal(t, "cask:later", later.Replacement.String)
	require.False(t, later.Stale)
	require.Equal(t, "2", later.Version.String)

	_, err := svc.Execute(ctx, "RestoreRevision", Input{ID: revisionID, Body: map[string]any{}})
	require.NoError(t, err)
	restored := readOverride()
	require.True(t, restored.At.Valid)
	require.True(t, target.At.Time.Equal(restored.At.Time))
	require.Equal(t, target.Date, restored.Date)
	require.Equal(t, target.Reason, restored.Reason)
	require.Equal(t, target.Replacement, restored.Replacement)
	require.Equal(t, target.Stale, restored.Stale)
	require.Equal(t, target.Version, restored.Version)
	require.False(t, restored.Date.Valid)
	require.False(t, restored.Reason.Valid)
	require.False(t, restored.Replacement.Valid)
}
