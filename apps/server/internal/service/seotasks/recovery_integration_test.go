//go:build integration

package seotasks_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func enqueueFixture(t *testing.T, svc *seotasks.Service, entity string) seotasks.Task {
	t.Helper()
	task, err := svc.Enqueue(context.Background(), seotasks.NewTask{Kind: "fixture", EntityKey: entity, DedupeKey: "recovery:" + entity, RuleVersion: "fixture", Evidence: map[string]any{"v": 1}})
	require.NoError(t, err)
	return task
}

func claimOne(t *testing.T, svc *seotasks.Service, owner string, id int64) seotasks.Task {
	t.Helper()
	claimed, err := svc.ClaimWith(context.Background(), owner, seotasks.ClaimFilter{Limit: 1, IDs: []int64{id}})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	return claimed[0]
}

func writePartial(t *testing.T, db *gorm.DB, task seotasks.Task, owner string, result map[string]any) {
	t.Helper()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		b, err := seotasks.Begin(context.Background(), tx, seotasks.Write{TaskID: task.ID, Fencing: task.Fencing, Owner: owner, IdempotencyKey: "partial-" + strconv.FormatInt(task.ID, 10), Operation: "WriteTranslations", Payload: result})
		if err != nil {
			return err
		}
		_, err = seotasks.CommitPartial(context.Background(), tx, b, seotasks.Mutation{Mode: "live", Object: "translation", ObjectID: task.EntityKey, Result: result, RuleVersion: "fixture", PlaybookVersion: "fixture"})
		return err
	}))
}

// The reason an agent gives for skipping or quarantining survives a partial
// write made earlier in the same lease; a finished task still reports its write.
func TestSettledTasksKeepTheirReason(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	written := map[string]any{"locale": "fr-FR", "status": "written", "reason": "validated"}

	quarantine := claimOne(t, svc, "translator", enqueueFixture(t, svc, "pkg:801").ID)
	writePartial(t, st.DB, quarantine, "translator", written)
	task, err := svc.Complete(ctx, quarantine.ID, quarantine.Fencing, "translator", "quarantined", "de-DE changed_literal after 3 corrections")
	require.NoError(t, err)
	require.Equal(t, "quarantined", task.State)
	var result map[string]any
	require.NoError(t, json.Unmarshal(task.Result, &result))
	require.Equal(t, "quarantined", result["outcome"])
	require.Equal(t, "de-DE changed_literal after 3 corrections", result["reason"])
	require.Equal(t, "validated", result["lastAction"].(map[string]any)["reason"])

	done := claimOne(t, svc, "translator", enqueueFixture(t, svc, "pkg:802").ID)
	writePartial(t, st.DB, done, "translator", written)
	task, err = svc.Complete(ctx, done.ID, done.Fencing, "translator", "done", "")
	require.NoError(t, err)
	require.JSONEq(t, `{"locale":"fr-FR","status":"written","reason":"validated"}`, string(task.Result))
}

func TestReopenWithoutCooldownOrStatePrerequisites(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	id := enqueueFixture(t, svc, "pkg:811").ID
	for round, state := range []string{"open", "deferred", "skipped", "quarantined", "done", "expired"} {
		require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET state=?,expires_at=clock_timestamp()-interval '1 minute',next_retry_at=clock_timestamp()+interval '1 day' WHERE id=?", state, id).Error)
		task, err := svc.Reopen(ctx, id, "agent:1:2", "retry now", false)
		require.NoError(t, err)
		require.Equal(t, "open", task.State)
		require.Nil(t, task.ExpiresAt)
		require.Nil(t, task.NextRetryAt)
		require.Equal(t, round+1, task.ReopenCount)
	}
	leased := claimOne(t, svc, "agent:1:2", id)
	reopened, err := svc.Reopen(ctx, id, "agent:1:2", "restart leased work", false)
	require.NoError(t, err)
	require.Greater(t, reopened.Fencing, leased.Fencing)
	_, err = svc.Complete(ctx, id, leased.Fencing, "agent:1:2", "done", "")
	require.Error(t, err, "old worker must not complete a reopened task")
	again := claimOne(t, svc, "agent:1:2", id)
	require.Greater(t, again.Fencing, reopened.Fencing)
}

func TestClaimTargetsRequestedTasks(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	enqueueFixture(t, svc, "pkg:821")
	wanted := enqueueFixture(t, svc, "pkg:822")
	claimed, err := svc.ClaimWith(ctx, "owner", seotasks.ClaimFilter{Limit: 5, IDs: []int64{wanted.ID}})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, wanted.ID, claimed[0].ID)
	_, err = svc.ClaimWith(ctx, "owner", seotasks.ClaimFilter{Limit: 1, IDs: []int64{0}})
	require.ErrorIs(t, err, seotasks.ErrInvalid)
}

func TestReopenAfterEntityLeaseTakeover(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	old := claimOne(t, svc, "old-worker", enqueueFixture(t, svc, "pkg:841").ID)
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET lease_until=clock_timestamp()-interval '1 minute' WHERE id=?", old.ID).Error)
	require.NoError(t, st.DB.Exec("UPDATE seo_entity_leases SET lease_until=clock_timestamp()-interval '1 minute' WHERE task_id=?", old.ID).Error)
	next, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "fixture", EntityKey: "package:841", DedupeKey: "takeover", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	held := claimOne(t, svc, "new-worker", next.ID)
	reopened, err := svc.Reopen(ctx, old.ID, "operator", "retry abandoned task", false)
	require.NoError(t, err)
	require.Equal(t, "open", reopened.State)
	require.Nil(t, reopened.LeaseOwner)
	require.Nil(t, reopened.LeaseUntil)
	require.Greater(t, reopened.Fencing, old.Fencing)
	require.Equal(t, 1, reopened.ReopenCount)
	_, err = svc.Complete(ctx, old.ID, old.Fencing, "old-worker", "done", "")
	require.ErrorIs(t, err, seotasks.ErrLease)
	claimed, err := svc.ClaimWith(ctx, "retry-worker", seotasks.ClaimFilter{Limit: 1, IDs: []int64{old.ID}})
	require.NoError(t, err)
	require.Empty(t, claimed, "the replacement task still owns the shared entity")
	_, err = svc.Complete(ctx, held.ID, held.Fencing, "new-worker", "done", "")
	require.NoError(t, err, "reopening the old task must preserve the new lease")
	claimOne(t, svc, "retry-worker", old.ID)
}

// Evidence that arrived during a lease replaces the task's evidence when the
// lease ends, whether the owner defers it or abandons it.
func TestPendingEvidenceTakesOverWhenTheLeaseEnds(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	stash := func(id int64) {
		require.NoError(t, st.DB.Exec(`UPDATE seo_tasks SET pending_evidence='{"v":2}',pending_evidence_hash='hash-v2' WHERE id=?`, id).Error)
	}

	deferred := claimOne(t, svc, "owner", enqueueFixture(t, svc, "pkg:831").ID)
	stash(deferred.ID)
	require.NoError(t, svc.Defer(ctx, deferred.ID, deferred.Fencing, "owner", "source down", time.Now().Add(time.Hour)))
	task, err := svc.Get(ctx, deferred.ID)
	require.NoError(t, err)
	require.Equal(t, "open", task.State)
	require.Nil(t, task.NextRetryAt)
	require.JSONEq(t, `{"v":2}`, string(task.Evidence))
	require.Equal(t, "hash-v2", task.EvidenceHash)
	require.Nil(t, task.PendingEvidence)

	abandoned := claimOne(t, svc, "gone", enqueueFixture(t, svc, "pkg:832").ID)
	stash(abandoned.ID)
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET lease_until=clock_timestamp()-interval '1 minute' WHERE id=?", abandoned.ID).Error)
	require.NoError(t, st.DB.Exec("UPDATE seo_entity_leases SET lease_until=clock_timestamp()-interval '1 minute' WHERE task_id=?", abandoned.ID).Error)
	again := claimOne(t, svc, "next", abandoned.ID)
	require.JSONEq(t, `{"v":2}`, string(again.Evidence))
	require.Nil(t, again.PendingEvidence)
}
