//go:build integration

package management

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type disabledRequestHistoryFixture struct {
	st        *store.Store
	svc       *Service
	ref       content.Ref
	actorID   int64
	requestID string
	before    store.HistoryState
	steps     int
}

var disabledHistoryInterleavings = []struct {
	name  string
	clear []bool
}{
	{"edit", []bool{false}},
	{"clear", []bool{true}},
	{"edit_edit", []bool{false, false}},
	{"edit_clear", []bool{false, true}},
	{"clear_edit", []bool{true, false}},
	{"clear_clear", []bool{true, true}},
	{"edit_edit_edit", []bool{false, false, false}},
	{"edit_edit_clear", []bool{false, false, true}},
	{"edit_clear_edit", []bool{false, true, false}},
	{"edit_clear_clear", []bool{false, true, true}},
	{"clear_edit_edit", []bool{true, false, false}},
	{"clear_edit_clear", []bool{true, false, true}},
	{"clear_clear_edit", []bool{true, true, false}},
	{"clear_clear_clear", []bool{true, true, true}},
}

func newDisabledRequestHistoryFixture(t *testing.T, st *store.Store, id int64, requestID string) *disabledRequestHistoryFixture {
	t.Helper()
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(?,?,?)`, id, fmt.Sprintf("request-history-%d", id), "isolated-test-only").Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,download_size,disabled,disable_date,disable_reason,disable_replacement)
VALUES(?,'cask',?,?,'homebrew/cask','Request history','1','1','{}','fixture',100,true,'2026-01-01','unsafe','cask:replacement')`, id, fmt.Sprintf("request-history-%d", id), fmt.Sprintf("request-history-%d", id)).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id) VALUES(?)", id).Error)
	f := &disabledRequestHistoryFixture{st: st, svc: &Service{Store: st}, ref: content.Ref{Entity: "package", ID: id}, actorID: id, requestID: requestID}
	f.before = f.snapshot(t)
	return f
}

func (f *disabledRequestHistoryFixture) snapshot(t *testing.T) store.HistoryState {
	t.Helper()
	state, err := f.st.HistorySnapshotWithPackageLifecycle(context.Background(), f.ref)
	require.NoError(t, err)
	return state
}

func (f *disabledRequestHistoryFixture) write(t *testing.T, clear bool) {
	t.Helper()
	f.steps++
	body := map[string]any{"downloadSize": int64(100 + f.steps*100), "hidden": f.steps%2 == 1, "visibilityReason": "Visibility fixture"}
	if clear {
		body["clearDisabled"] = true
	}
	op := &domain.Operation{RequestID: f.requestID, Name: "updatePackageMeta", ActorID: &f.actorID, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, AllowDelete: true}
	_, err := f.svc.Execute(domain.WithOperation(context.Background(), op), "UpdatePackageMeta", Input{ID: f.ref.ID, Body: body, Actor: store.AuditActor{ID: &f.actorID}})
	require.NoError(t, err)
}

func (f *disabledRequestHistoryFixture) revisions(t *testing.T) []revisionRow {
	t.Helper()
	var revisions []revisionRow
	require.NoError(t, f.st.DB.Raw("SELECT id,before_data,after_data,after_hash FROM content_revisions WHERE request_id=? AND entity='package' AND object_key=? ORDER BY id", f.requestID, f.ref.Key()).Scan(&revisions).Error)
	require.Len(t, revisions, f.steps)
	return revisions
}

func (f *disabledRequestHistoryFixture) revert(t *testing.T) error {
	t.Helper()
	_, err := f.svc.Execute(context.Background(), "RevertRequest", Input{RequestID: f.requestID, Body: map[string]any{}})
	return err
}

func (f *disabledRequestHistoryFixture) requireRestored(t *testing.T) {
	t.Helper()
	after := f.snapshot(t)
	require.Equal(t, f.before["packages"], after["packages"], "restore the original ordinary fields and complete lifecycle, including explicit NULL overrides")
	require.Equal(t, f.before["package_meta"][0]["hidden"], after["package_meta"][0]["hidden"], "restore the metadata edit too")
}

func TestRevertRequestClearDisabledThenNormalEdit(t *testing.T) {
	st := testutil.NewSEOStore(t)
	f := newDisabledRequestHistoryFixture(t, st, 810, uuid.NewString())
	f.write(t, true)
	f.write(t, false)
	revisions := f.revisions(t)
	clearAfter, err := revisionState(revisions[0].AfterData)
	require.NoError(t, err)
	normalAfter, err := revisionState(revisions[1].AfterData)
	require.NoError(t, err)
	require.True(t, store.HistoryStateIncludesPackageLifecycle(clearAfter))
	require.False(t, store.HistoryStateIncludesPackageLifecycle(normalAfter))
	require.NoError(t, f.revert(t), "a normal edit after clearDisabled in the same request must be reversible")
	f.requireRestored(t)
}

func TestRevertRequestMixedPackageClearDisabledEdits(t *testing.T) {
	st := testutil.NewSEOStore(t)
	type step struct {
		pkg   int
		clear bool
	}
	for caseIndex, tc := range []struct {
		name  string
		steps []step
	}{
		{"clear_edit_then_other_packages", []step{{0, true}, {0, false}, {1, false}, {2, false}, {2, true}}},
		{"interleaved_packages", []step{{2, false}, {0, true}, {1, false}, {2, true}, {0, false}}},
		{"normal_package_last", []step{{2, false}, {2, true}, {0, true}, {0, false}, {1, false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestID := uuid.NewString()
			fixtures := make([]*disabledRequestHistoryFixture, 3)
			for i := range fixtures {
				fixtures[i] = newDisabledRequestHistoryFixture(t, st, int64(820+caseIndex*3+i), requestID)
			}
			for _, step := range tc.steps {
				fixtures[step.pkg].write(t, step.clear)
			}
			for _, f := range fixtures {
				f.revisions(t)
			}
			require.NoError(t, fixtures[0].revert(t), "the request must restore every package regardless of revision order")
			for _, f := range fixtures {
				f.requireRestored(t)
			}
		})
	}
}

const laterUpstreamDisabledSQL = `UPDATE packages SET disabled=true,stale_disabled=false,
disable_date='2026-02-02',disable_reason='later',disable_replacement='cask:later',
disabled_override_at=NULL,disabled_override_date=NULL,disabled_override_reason=NULL,
disabled_override_replacement=NULL,disabled_override_stale=false,disabled_override_version=NULL WHERE id=?`

func TestRevertRequestClearDisabledInterleavings(t *testing.T) {
	st := testutil.NewSEOStore(t)
	id := int64(9000)
	for _, sequence := range disabledHistoryInterleavings {
		t.Run(sequence.name, func(t *testing.T) {
			for _, scenario := range []struct {
				name, setup, upstream string
			}{
				{name: "disabled"},
				{name: "stale_disabled", setup: "UPDATE packages SET disabled=false,stale_disabled=true WHERE id=?"},
				{name: "active_null_lifecycle", setup: "UPDATE packages SET disabled=false,disable_date=NULL,disable_reason=NULL,disable_replacement=NULL WHERE id=?"},
				{name: "later_upstream_lifecycle", upstream: laterUpstreamDisabledSQL},
				{name: "active_later_upstream_lifecycle", setup: "UPDATE packages SET disabled=false WHERE id=?", upstream: laterUpstreamDisabledSQL},
				{name: "later_upstream_version", upstream: "UPDATE packages SET version='2',version_base='2' WHERE id=?"},
				{name: "later_normal_edit", upstream: "UPDATE packages SET download_size=999 WHERE id=?"},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					id++
					f := newDisabledRequestHistoryFixture(t, st, id, uuid.NewString())
					if scenario.setup != "" {
						require.NoError(t, st.DB.Exec(scenario.setup, id).Error)
						f.before = f.snapshot(t)
					}
					hasClear := false
					for _, clear := range sequence.clear {
						f.write(t, clear)
						hasClear = hasClear || clear
					}
					f.revisions(t)
					if scenario.upstream != "" {
						require.NoError(t, st.DB.Exec(scenario.upstream, id).Error)
					}
					current := f.snapshot(t)
					err := f.revert(t)
					lifecycleChanged := scenario.upstream == laterUpstreamDisabledSQL
					if hasClear && lifecycleChanged || scenario.name == "later_normal_edit" {
						var conflict *domain.AppError
						require.ErrorAs(t, err, &conflict)
						require.Equal(t, domain.CodeConflict, conflict.Code)
						require.Equal(t, current, f.snapshot(t), "a conflict must leave all content untouched")
						var reverts int64
						require.NoError(t, st.DB.Table("content_request_reverts").Where("request_id=?", f.requestID).Count(&reverts).Error)
						require.Zero(t, reverts)
						return
					}
					require.NoError(t, err)
					if lifecycleChanged {
						// Normal-only history never captured lifecycle, so an upstream
						// disable is preserved while the ordinary edits are reverted.
						current["packages"][0]["download_size"] = float64(100)
						require.Equal(t, current["packages"], f.snapshot(t)["packages"])
					} else {
						f.requireRestored(t)
					}
					require.Equal(t, false, f.snapshot(t)["package_meta"][0]["hidden"], "the metadata edit must be reverted even when upstream lifecycle is preserved")
					if scenario.name == "later_upstream_version" {
						var version string
						require.NoError(t, st.DB.Raw("SELECT version FROM packages WHERE id=?", id).Scan(&version).Error)
						require.Equal(t, "2", version, "uncaptured upstream fields must survive revert")
					}
				})
			}
		})
	}
}

func TestRevertRequestClearDisabledUsesLatestCapturedLifecycle(t *testing.T) {
	st := testutil.NewSEOStore(t)
	for i, tc := range []struct {
		name string
		date any
	}{
		{"latest_values", "2026-02-02"},
		{"latest_explicit_nulls", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDisabledRequestHistoryFixture(t, st, int64(840+i), uuid.NewString())
			f.write(t, true)
			// A sync between clears changes the lifecycle payload. The trailing
			// normal edits must fall back to the latest clear, including its NULLs.
			require.NoError(t, st.DB.Exec("UPDATE packages SET disabled=true,disable_date=?,disable_reason=NULL,disable_replacement=NULL WHERE id=?", tc.date, f.ref.ID).Error)
			f.write(t, true)
			f.write(t, false)
			f.write(t, false)
			revisions := f.revisions(t)
			latestClear, err := revisionState(revisions[1].AfterData)
			require.NoError(t, err)
			require.Contains(t, latestClear["packages"][0], "disabled_override_reason")
			require.Nil(t, latestClear["packages"][0]["disabled_override_reason"])
			if tc.date == nil {
				require.Contains(t, latestClear["packages"][0], "disabled_override_date")
				require.Nil(t, latestClear["packages"][0]["disabled_override_date"])
			}
			require.NoError(t, f.revert(t))
			f.requireRestored(t)
		})
	}
}

func TestRevertRequestMixedPackageClearDisabledConflictIsAtomic(t *testing.T) {
	st := testutil.NewSEOStore(t)
	requestID := uuid.NewString()
	conflicting := newDisabledRequestHistoryFixture(t, st, 850, requestID)
	normal := newDisabledRequestHistoryFixture(t, st, 851, requestID)
	cleared := newDisabledRequestHistoryFixture(t, st, 852, requestID)
	conflicting.write(t, true)
	conflicting.write(t, false)
	normal.write(t, false)
	cleared.write(t, false)
	cleared.write(t, true)
	require.NoError(t, st.DB.Exec(laterUpstreamDisabledSQL, conflicting.ref.ID).Error)
	fixtures := []*disabledRequestHistoryFixture{conflicting, normal, cleared}
	current := make([]store.HistoryState, len(fixtures))
	for i, f := range fixtures {
		current[i] = f.snapshot(t)
	}
	var revisionCount int64
	require.NoError(t, st.DB.Table("content_revisions").Count(&revisionCount).Error)
	var conflict *domain.AppError
	require.ErrorAs(t, conflicting.revert(t), &conflict)
	require.Equal(t, domain.CodeConflict, conflict.Code)
	// DESC revision order restores the other packages before reaching the
	// conflicting package. Both those writes and their history must roll back.
	for i, f := range fixtures {
		require.Equal(t, current[i], f.snapshot(t))
	}
	var afterCount, reverts int64
	require.NoError(t, st.DB.Table("content_revisions").Count(&afterCount).Error)
	require.Equal(t, revisionCount, afterCount)
	require.NoError(t, st.DB.Table("content_request_reverts").Where("request_id=?", requestID).Count(&reverts).Error)
	require.Zero(t, reverts)
}

func TestRestoreRevisionClearDisabledInterleavings(t *testing.T) {
	st := testutil.NewSEOStore(t)
	for i, sequence := range disabledHistoryInterleavings {
		t.Run(sequence.name, func(t *testing.T) {
			f := newDisabledRequestHistoryFixture(t, st, int64(10000+i), uuid.NewString())
			// NULL payload members must be restored as part of the override group,
			// even when those members were NULL on both sides of the original clear.
			require.NoError(t, st.DB.Exec("UPDATE packages SET disable_date=NULL,disable_reason=NULL,disable_replacement=NULL WHERE id=?", f.ref.ID).Error)
			firstClear := -1
			for step, clear := range sequence.clear {
				f.write(t, clear)
				if clear && firstClear < 0 {
					firstClear = step
				}
			}
			for step, revision := range f.revisions(t) {
				t.Run(fmt.Sprintf("revision_%d", step+1), func(t *testing.T) {
					require.NoError(t, st.DB.Exec(laterUpstreamDisabledSQL, f.ref.ID).Error)
					// Give every override field a distinct later value so restoring
					// a clear has to replace the whole payload, including explicit NULLs.
					require.NoError(t, st.DB.Exec(`UPDATE packages SET disabled_override_at='2026-03-03T00:00:00Z',disabled_override_date='2026-03-03',disabled_override_reason='later override',disabled_override_replacement='cask:override',disabled_override_stale=true,disabled_override_version='2' WHERE id=?`, f.ref.ID).Error)
					expected := f.snapshot(t)
					expected["packages"][0]["download_size"] = float64(200 + step*100)
					if step == firstClear {
						after, err := revisionState(revision.AfterData)
						require.NoError(t, err)
						expected["packages"][0]["disabled"] = false
						for _, field := range []string{"disabled_override_at", "disabled_override_date", "disabled_override_reason", "disabled_override_replacement", "disabled_override_stale", "disabled_override_version"} {
							expected["packages"][0][field] = after["packages"][0][field]
						}
					}
					_, err := f.svc.Execute(context.Background(), "RestoreRevision", Input{ID: revision.ID, Body: map[string]any{}})
					require.NoError(t, err)
					actual := f.snapshot(t)
					require.Equal(t, expected["packages"], actual["packages"], "normal and repeated-clear restores preserve later lifecycle; a state-changing clear restores its override group")
					require.Equal(t, step%2 == 0, actual["package_meta"][0]["hidden"])
				})
			}
		})
	}
}
