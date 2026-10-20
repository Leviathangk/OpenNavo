//go:build integration

package seotasks_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTaskLeaseFencingAndAtomicActions(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	enqueue := func(entity string) seotasks.Task {
		task, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "fixture", EntityKey: entity, DedupeKey: uuid.NewString(), RuleVersion: "test-v1", Evidence: map[string]any{"source": "fixture"}})
		require.NoError(t, err)
		return task
	}
	owner := "agent:1:1"
	t.Run("expired task releases entity", func(t *testing.T) {
		first := enqueue("category:expired-fixture")
		leased, err := svc.Claim(ctx, owner, 1, []string{"fixture"})
		require.NoError(t, err)
		require.Len(t, leased, 1)
		require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET expires_at=now()-interval '1 second' WHERE id=?", first.ID).Error)
		next := enqueue("category:expired-fixture")
		leased, err = svc.Claim(ctx, owner, 1, []string{"fixture"})
		require.NoError(t, err)
		require.Len(t, leased, 1)
		require.Equal(t, next.ID, leased[0].ID)
		expired, err := svc.Get(ctx, first.ID)
		require.NoError(t, err)
		require.Equal(t, "expired", expired.State)
		_, err = svc.Complete(ctx, next.ID, leased[0].Fencing, owner, "skipped", "fixture complete")
		require.NoError(t, err)
	})
	t.Run("concurrent entity claims", func(t *testing.T) {
		for i := 0; i < 4; i++ {
			enqueue("package:one")
		}
		var claims atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := svc.Claim(ctx, uuid.NewString(), 2, []string{"fixture"})
				if err != nil {
					t.Error(err)
					return
				}
				claims.Add(int64(len(got)))
			}()
		}
		wg.Wait()
		require.EqualValues(t, 1, claims.Load())
		require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET state='expired' WHERE entity_key='package:one'").Error)
	})
	t.Run("expired owner cannot write", func(t *testing.T) {
		task := enqueue("package:two")
		first, err := svc.Claim(ctx, owner, 1, []string{"fixture"})
		require.NoError(t, err)
		require.Len(t, first, 1)
		require.Equal(t, task.ID, first[0].ID)
		require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET lease_until=now()-interval '1 second' WHERE id=?", task.ID).Error)
		require.NoError(t, st.DB.Exec("UPDATE seo_entity_leases SET lease_until=now()-interval '1 second' WHERE task_id=?", task.ID).Error)
		second, err := svc.Claim(ctx, "agent:1:2", 1, []string{"fixture"})
		require.NoError(t, err)
		require.Len(t, second, 1)
		require.Greater(t, second[0].Fencing, first[0].Fencing)
		called := false
		_, err = svc.Run(ctx, seotasks.Write{TaskID: task.ID, Fencing: first[0].Fencing, Owner: owner, IdempotencyKey: uuid.NewString(), Operation: "fixture"}, func(*gorm.DB, seotasks.Task) (seotasks.Mutation, error) {
			called = true
			return seotasks.Mutation{}, nil
		})
		require.ErrorIs(t, err, seotasks.ErrLease)
		require.False(t, called)
		require.ErrorIs(t, svc.Defer(ctx, task.ID, first[0].Fencing, owner, "upstream_timeout", time.Now().Add(time.Hour)), seotasks.ErrLease)
		require.NoError(t, svc.Defer(ctx, task.ID, second[0].Fencing, "agent:1:2", "upstream_timeout", time.Now().Add(time.Hour)))
		got, err := svc.Claim(ctx, owner, 1, []string{"fixture"})
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("business revision task outbox and response commit together", func(t *testing.T) {
		require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(701,'action-fixture','lucide:box','en-US')").Error)
		enqueue("category:701")
		claimed, err := svc.Claim(ctx, owner, 1, []string{"fixture"})
		require.NoError(t, err)
		require.Len(t, claimed, 1)
		w := seotasks.Write{TaskID: claimed[0].ID, Fencing: claimed[0].Fencing, Owner: owner, IdempotencyKey: uuid.NewString(), Operation: "updateCategory", Payload: map[string]any{"name": "File organizer"}}
		var calls atomic.Int64
		mutate := func(tx *gorm.DB, _ seotasks.Task) (seotasks.Mutation, error) {
			calls.Add(1)
			db := &store.Store{DB: tx}
			ref := content.Ref{Entity: "category", ID: 701}
			before, e := db.HistorySnapshot(ctx, ref)
			if e != nil {
				return seotasks.Mutation{}, e
			}
			if e = db.WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File organizer"}, true); e != nil {
				return seotasks.Mutation{}, e
			}
			after, e := db.HistorySnapshot(ctx, ref)
			if e != nil {
				return seotasks.Mutation{}, e
			}
			_, e = db.RecordRevision(ctx, ref, before, after, "update", &domain.Operation{RequestID: w.IdempotencyKey, Name: "updateCategory", AgentClientID: 1, RequiredPermissions: []string{"catalog:category:edit"}})
			if e != nil {
				return seotasks.Mutation{}, e
			}
			e = tx.Exec("INSERT INTO job_outbox(unique_key,job_type,payload) VALUES(?,'seo:audit','{}')", w.IdempotencyKey).Error
			return seotasks.Mutation{Mode: "live", Object: "category", ObjectID: "701", Fields: []string{"name"}, Before: before, After: after, Result: map[string]any{"saved": true}, RuleVersion: "test-v1", PlaybookVersion: "test-v1"}, e
		}
		injected := errors.New("failure after business writes")
		_, err = svc.Run(ctx, w, func(tx *gorm.DB, t seotasks.Task) (seotasks.Mutation, error) {
			m, e := mutate(tx, t)
			if e != nil {
				return m, e
			}
			return m, injected
		})
		require.ErrorIs(t, err, injected)
		count := func(table string) int64 {
			var n int64
			require.NoError(t, st.DB.Table(table).Count(&n).Error)
			return n
		}
		require.Zero(t, count("category_i18n"))
		require.Zero(t, count("content_revisions"))
		require.Zero(t, count("seo_actions"))
		require.Zero(t, count("job_outbox"))
		current, err := svc.Get(ctx, w.TaskID)
		require.NoError(t, err)
		require.Equal(t, "leased", current.State)
		// A timeout during the callback must roll back writes even while row locks are held.
		_, err = svc.Run(ctx, w, func(tx *gorm.DB, t seotasks.Task) (seotasks.Mutation, error) {
			m, e := mutate(tx, t)
			if e != nil {
				return m, e
			}
			e = tx.Exec("UPDATE seo_tasks SET lease_until=now()-interval '1 second' WHERE id=?", t.ID).Error
			return m, e
		})
		require.ErrorIs(t, err, seotasks.ErrLease)
		require.Zero(t, count("category_i18n"))
		require.Zero(t, count("seo_actions"))
		var wg sync.WaitGroup
		results := make(chan json.RawMessage, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, e := svc.Run(ctx, w, mutate)
				if e != nil {
					t.Error(e)
					return
				}
				results <- result
			}()
		}
		wg.Wait()
		close(results)
		for result := range results {
			require.JSONEq(t, `{"saved":true}`, string(result))
		}
		require.EqualValues(t, 3, calls.Load(), "two rolled back callbacks and one committed callback")
		require.EqualValues(t, 1, count("category_i18n"))
		require.EqualValues(t, 1, count("content_revisions"))
		require.EqualValues(t, 1, count("seo_actions"))
		require.EqualValues(t, 1, count("job_outbox"))
		current, err = svc.Get(ctx, w.TaskID)
		require.NoError(t, err)
		require.Equal(t, "done", current.State)
		w.Payload = map[string]any{"name": "Different content"}
		_, err = svc.Run(ctx, w, mutate)
		require.ErrorIs(t, err, seotasks.ErrConflict)
	})
}

func TestClaimRefillsPastCanonicalAliases(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_entity_leases(entity_key,owner,fencing,lease_until) VALUES('pkg:991','busy',1,now()+interval '1 hour')`).Error)
	for i := 0; i < 25; i++ {
		_, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "alias", EntityKey: "package:991", DedupeKey: uuid.NewString(), Priority: 100, RuleVersion: "test", Evidence: map[string]any{}})
		require.NoError(t, err)
	}
	want, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "alias", EntityKey: "pkg:992", DedupeKey: uuid.NewString(), RuleVersion: "test", Evidence: map[string]any{}})
	require.NoError(t, err)
	got, err := svc.Claim(ctx, "worker", 1, []string{"alias"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want.ID, got[0].ID)
}

func TestClaimRefillsAfterConcurrentEntityReservation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	tx := st.DB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	require.NoError(t, tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo-claim:pkg:993").Error)
	for i := 0; i < 25; i++ {
		_, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "contention", EntityKey: "package:993", DedupeKey: uuid.NewString(), Priority: 100, RuleVersion: "test", Evidence: map[string]any{}})
		require.NoError(t, err)
	}
	want, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "contention", EntityKey: "pkg:994", DedupeKey: uuid.NewString(), RuleVersion: "test", Evidence: map[string]any{}})
	require.NoError(t, err)
	got, err := svc.Claim(ctx, "worker", 1, []string{"contention"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want.ID, got[0].ID)
}
