//go:build integration

package seoopportunities

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func insertCask(t *testing.T, db *gorm.DB, id int64, token, version string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(?,'cask',?,?,'homebrew/cask','Fixture',?,?,'{}','fixture')`, id, token, token, version, version).Error)
}

func catalogEvent(t *testing.T, db *gorm.DB, id int64, token string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(?,'cask',?,'upsert','homebrew')`, id, token).Error)
}

func maintenanceTask(t *testing.T, db *gorm.DB, id int64) seotasks.Task {
	t.Helper()
	var task seotasks.Task
	require.NoError(t, db.Raw("SELECT * FROM seo_tasks WHERE dedupe_key=?", "catalog:pkg:"+itoa(id)).Scan(&task).Error)
	require.NotZero(t, task.ID)
	return task
}

// An owner that disappears no longer protects the task once its lease expires.
func TestExpiredLeaseNoLongerHoldsEvidence(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	insertCask(t, st.DB, 702, "abandoned-editor", "1.0")
	catalogEvent(t, st.DB, 702, "abandoned-editor")
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	tasks := &seotasks.Service{DB: st.DB}
	claimed, err := tasks.Claim(ctx, "gone-owner", 1, []string{"maintenance"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET lease_until=clock_timestamp()-interval '1 hour' WHERE id=?", claimed[0].ID).Error)
	require.NoError(t, st.DB.Exec("UPDATE seo_entity_leases SET lease_until=clock_timestamp()-interval '1 hour'").Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET version='2.0' WHERE id=702").Error)
	catalogEvent(t, st.DB, 702, "abandoned-editor")
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	task := maintenanceTask(t, st.DB, 702)
	require.Equal(t, "open", task.State)
	require.Contains(t, string(task.Evidence), `"2.0"`)
	require.Nil(t, task.LeaseUntil)
	require.Nil(t, task.PendingEvidence)
	_, err = tasks.Complete(ctx, claimed[0].ID, claimed[0].Fencing, "gone-owner", "skipped", "late")
	require.ErrorIs(t, err, seotasks.ErrLease)
}

// Bulk content invalidations are not maintenance work and must not use up the
// window that carries version updates.
func TestContentRowsDoNotHoldBackVersionUpdates(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	insertCask(t, st.DB, 703, "busy-editor", "3.1")
	require.NoError(t, st.DB.Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT 703,'cask','busy-editor','upsert','content' FROM generate_series(1,1200)`).Error)
	catalogEvent(t, st.DB, 703, "busy-editor")
	out, err := svc.Run(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, out["maintenance"])
	require.Contains(t, string(maintenanceTask(t, st.DB, 703).Evidence), `"3.1"`)
	var head, cursor int64
	require.NoError(t, st.DB.Raw("SELECT max(seq) FROM catalog_changes").Scan(&head).Error)
	require.NoError(t, st.DB.Raw("SELECT cursor FROM seo_opportunity_cursors WHERE source='catalog_changes'").Scan(&cursor).Error)
	require.Equal(t, head, cursor)
}

// Tasks whose version event was missed or arrived while held converge on the
// current package without any new event, with the same reopen rules as events.
func TestReconcileAlignsMaintenanceTasksWithTheirPackage(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	tasks := &seotasks.Service{DB: st.DB}
	ids := map[string]int64{"skipped": 711, "quarantined": 712, "leased": 713, "done": 714}
	for state, id := range ids {
		token := "stale-" + state
		insertCask(t, st.DB, id, token, "2.0")
		task, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "maintenance", EntityKey: "pkg:" + itoa(id), DedupeKey: "catalog:pkg:" + itoa(id), Locale: "en-US", Priority: 10, RuleVersion: RuleVersion,
			Evidence: map[string]any{"source": "catalog_changes", "version": "1.0", "token": token, "homepage": nil}})
		require.NoError(t, err)
		if state == "leased" {
			claimed, err := tasks.ClaimWith(ctx, "busy-owner", seotasks.ClaimFilter{Limit: 1, IDs: []int64{task.ID}})
			require.NoError(t, err)
			require.Len(t, claimed, 1)
		} else {
			require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET state=? WHERE id=?", state, task.ID).Error)
		}
	}
	out, err := svc.Run(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 3, out["reconciled"])
	for state, want := range map[string]string{"skipped": "open", "quarantined": "quarantined", "done": "open"} {
		task := maintenanceTask(t, st.DB, ids[state])
		require.Equal(t, want, task.State, state)
		require.Contains(t, string(task.Evidence), `"2.0"`, state)
	}
	held := maintenanceTask(t, st.DB, ids["leased"])
	require.Equal(t, "leased", held.State)
	require.Contains(t, string(held.Evidence), `"1.0"`)
	again, err := svc.Run(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, again["reconciled"])
}

// A signal that changes nothing leaves the row alone, so updated_at keeps
// meaning "last real change" (the weekly article-refresh retry depends on it).
func TestUnchangedSignalsLeaveTasksUntouched(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	insertCask(t, st.DB, 721, "steady-editor", "1.0")
	catalogEvent(t, st.DB, 721, "steady-editor")
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	first := maintenanceTask(t, st.DB, 721)
	catalogEvent(t, st.DB, 721, "steady-editor")
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.True(t, first.UpdatedAt.Equal(maintenanceTask(t, st.DB, 721).UpdatedAt))
}

// The cursor never passes a catalog write that is still committing.
func TestRunWaitsForACommittingCatalogWriter(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	insertCask(t, st.DB, 731, "committing-editor", "1.0")
	tx := st.DB.Begin()
	require.NoError(t, store.LockCatalogWrites(ctx, tx))
	catalogEvent(t, tx, 731, "committing-editor")
	type result struct {
		out map[string]any
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := svc.Run(ctx)
		done <- result{out, err}
	}()
	select {
	case <-done:
		t.Fatal("the run finished while a catalog write was uncommitted")
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, tx.Commit().Error)
	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.EqualValues(t, 1, r.out["maintenance"])
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not finish after the catalog write committed")
	}
	maintenanceTask(t, st.DB, 731)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
