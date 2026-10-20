//go:build integration

package seotasks_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTaskControlsReplayClaimAndRejectStaleCompletion(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	_, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "tech_alert", EntityKey: "pkg:701", DedupeKey: "control-fixture", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	claim := func(s *seotasks.Service) (any, error) { return s.Claim(ctx, "control-owner", 1, nil) }
	_, err = svc.Control(ctx, "control-owner", "claim", "fixture-claim", map[string]any{"limit": 1}, true, claim)
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("seo_entity_leases").Count(&count).Error)
	require.Zero(t, count)
	first, err := svc.Control(ctx, "control-owner", "claim", "fixture-claim", map[string]any{"limit": 1}, false, claim)
	require.NoError(t, err)
	replay, err := svc.Control(ctx, "control-owner", "claim", "fixture-claim", map[string]any{"limit": 1}, false, claim)
	require.NoError(t, err)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(replay)
	require.JSONEq(t, string(a), string(b))
	_, err = svc.Control(ctx, "control-owner", "claim", "fixture-claim", map[string]any{"limit": 2}, false, claim)
	require.ErrorIs(t, err, seotasks.ErrConflict)
	tasks := first.([]seotasks.Task)
	require.Len(t, tasks, 1)
	task := tasks[0]
	_, err = svc.Complete(ctx, task.ID, task.Fencing, "someone-else", "done", "")
	require.ErrorIs(t, err, seotasks.ErrLease)
	require.NoError(t, svc.Defer(ctx, task.ID, task.Fencing, "control-owner", "retry official source", time.Now().Add(time.Hour)))
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET next_retry_at=now()-interval '1 minute' WHERE id=?", task.ID).Error)
	again, err := svc.Claim(ctx, "new-owner", 1, nil)
	require.NoError(t, err)
	require.Len(t, again, 1)
	require.Greater(t, again[0].Fencing, task.Fencing)
	_, err = svc.Complete(ctx, task.ID, task.Fencing, "control-owner", "done", "")
	require.ErrorIs(t, err, seotasks.ErrLease)
	result, err := svc.Complete(ctx, task.ID, again[0].Fencing, "new-owner", "done", "")
	require.NoError(t, err)
	require.Equal(t, "done", result.State)
	require.Contains(t, string(result.Result), "outcome")
}

func TestPackageAndReleaseShareLeaseAndNewObjectRebind(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','lease-editor','lease-editor','homebrew/cask','Lease Editor','1.0','1.0','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(id,package_id,source,source_key,version,source_locale) VALUES(801,701,'editorial','lease-fixture','1.0','en-US')`).Error)
	for _, key := range []string{"package:701", "pkg:701", "release:801"} {
		_, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "fixture", EntityKey: key, DedupeKey: key, RuleVersion: "fixture", Evidence: map[string]any{}})
		require.NoError(t, err)
	}
	first, err := svc.Claim(ctx, "owner-one", 10, nil)
	require.NoError(t, err)
	require.Len(t, first, 1)
	second, err := svc.Claim(ctx, "owner-two", 10, nil)
	require.NoError(t, err)
	require.Empty(t, second)
	_, err = svc.Enqueue(ctx, seotasks.NewTask{Kind: "create", EntityKey: "article:new:fixture", DedupeKey: "new-article", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	create, err := svc.Claim(ctx, "owner-create", 1, []string{"create"})
	require.NoError(t, err)
	require.Len(t, create, 1)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return seotasks.Rebind(ctx, tx, create[0], "owner-create", "article:902") }))
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		task, err := seotasks.Verify(ctx, tx, create[0].ID, create[0].Fencing, "owner-create")
		require.Equal(t, "article:902", task.EntityKey)
		return err
	}))
}
