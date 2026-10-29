//go:build integration

package management

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

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
