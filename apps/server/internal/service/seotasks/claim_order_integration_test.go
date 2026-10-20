//go:build integration

package seotasks_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type claimQueryCapture struct {
	logger.Interface
	mu  sync.Mutex
	sql string
}

func (l *claimQueryCapture) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	if strings.HasPrefix(sql, "SELECT ") && strings.Contains(sql, `FROM "seo_tasks"`) && strings.Contains(sql, "SKIP LOCKED") {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.sql = sql
	}
}

func TestClaimAgingRetrySetAndIndexPlan(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	// 1,024 terminal rows are a small plan fixture, not a throughput benchmark.
	// Completed imports without another retry must not accumulate in the index.
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_tasks(kind,entity_key,dedupe_key,rule_version,state,priority)
 SELECT CASE WHEN n%2=0 THEN 'hermes_import' ELSE 'fixture' END,'history:'||n,'history:'||n,'fixture','done',10000 FROM generate_series(1,1024)n`).Error)
	base := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	insert := func(key, kind, state string, priority float64, age time.Duration, retry, lease, expiry *time.Time) int64 {
		t.Helper()
		var id int64
		require.NoError(t, st.DB.Raw(`INSERT INTO seo_tasks(kind,entity_key,dedupe_key,rule_version,state,priority,created_at,next_retry_at,lease_until,expires_at,evidence)
 VALUES(?,?,?,'fixture',?,?,?,?,?,?, '{"field":"price"}') RETURNING id`, kind, key, key, state, priority, base.Add(-age), retry, lease, expiry).Scan(&id).Error)
		return id
	}
	day := 24 * time.Hour
	eligible := []int64{
		insert("aged", "fixture", "open", 0, 10*day, nil, nil, nil),
		insert("recent", "fixture", "open", 5, 0, nil, nil, nil),
		insert("tie-first", "fixture", "open", 2, 2*day, nil, nil, nil),
		insert("tie-second", "fixture", "open", 3, day, nil, nil, nil),
		insert("deferred-due", "fixture", "deferred", 1, 2*day, &past, nil, nil),
		insert("lease-expired", "fixture", "leased", 2, 0, nil, &past, nil),
		insert("periodic-due", "hermes_import", "done", 1.125, 0, &past, nil, nil),
		insert("low", "fixture", "open", -0.5, 0, nil, nil, nil),
	}
	insert("periodic-later", "hermes_import", "done", 10000, 0, &future, nil, nil)
	insert("deferred-later", "fixture", "deferred", 10000, 0, &future, nil, nil)
	insert("lease-active", "fixture", "leased", 10000, 0, nil, &future, nil)
	insert("open-expired", "fixture", "open", 10000, 0, nil, nil, &past)
	for _, state := range []string{"skipped", "quarantined", "expired"} {
		insert("terminal-"+state, "hermes_import", state, 10000, 0, &past, nil, nil)
	}
	blocked := insert("entity-busy", "fixture", "open", 10000, 0, nil, nil, nil)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_entity_leases(entity_key,task_id,owner,fencing,lease_until) VALUES('entity-busy',?,'existing-owner',7,?)`, blocked, future).Error)
	require.NoError(t, st.DB.Exec("ANALYZE seo_tasks").Error)
	require.NoError(t, st.DB.Exec("ANALYZE seo_entity_leases").Error)

	var predicate string
	require.NoError(t, st.DB.Raw(`SELECT pg_get_expr(indpred,indrelid) FROM pg_index WHERE indexrelid='seo_tasks_claim_age'::regclass`).Scan(&predicate).Error)
	var indexed int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM seo_tasks WHERE "+predicate).Scan(&indexed).Error)
	require.EqualValues(t, len(eligible)+5, indexed, "only open states and scheduled recurring imports belong in the partial index")

	rollback := errors.New("preview rollback")
	for _, zone := range []string{"UTC", "America/Los_Angeles"} {
		for _, kinds := range [][]string{nil, {"fixture"}, {"hermes_import"}} {
			t.Run(zone+"/"+strings.Join(kinds, ","), func(t *testing.T) {
				capture := &claimQueryCapture{Interface: logger.Default.LogMode(logger.Silent)}
				err := st.DB.Session(&gorm.Session{Logger: capture}).Transaction(func(tx *gorm.DB) error {
					require.NoError(t, tx.Exec("SELECT set_config('TimeZone',?,true)", zone).Error)
					// Compare the actual Claim with the former dynamic aging expression.
					var expected []int64
					q := tx.Table("seo_tasks").Select("id").Where("id IN ?", eligible)
					if len(kinds) > 0 {
						q = q.Where("kind IN ?", kinds)
					}
					require.NoError(t, q.Order("priority + extract(epoch FROM (statement_timestamp()-created_at))/86400 DESC,id").Scan(&expected).Error)
					tasks, err := (&seotasks.Service{DB: tx}).Claim(ctx, "fixture-claim-owner", 10, kinds)
					require.NoError(t, err)
					got := make([]int64, len(tasks))
					for i, task := range tasks {
						got[i] = task.ID
						require.Equal(t, "leased", task.State)
						require.Positive(t, task.Fencing)
						if task.Kind == "hermes_import" {
							require.NotNil(t, task.NextRetryAt)
							require.True(t, task.NextRetryAt.After(now.Add(29*day)))
						}
					}
					require.Equal(t, expected, got)
					if len(kinds) == 0 {
						require.Equal(t, eligible, got, "old low-priority task wins and ties keep id order")
					}
					return rollback
				})
				require.ErrorIs(t, err, rollback)
				capture.mu.Lock()
				query := capture.sql
				capture.mu.Unlock()
				require.NotEmpty(t, query)
				var plan []string
				require.NoError(t, st.DB.Raw("EXPLAIN (COSTS OFF) "+query).Scan(&plan).Error)
				explain := strings.Join(plan, "\n")
				t.Log("Small-fixture default plan:\n" + explain)
				require.Contains(t, explain, "seo_tasks_claim_age", "the candidate query excludes completed history through the partial index")
				require.NotContains(t, explain, "Seq Scan on seo_tasks")
				// For just 13 indexed rows PostgreSQL may choose a bitmap scan
				// followed by a tiny sort. Separately prove that the actual SQL can
				// use index order; do not assert timings or a production cost model.
				require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
					require.NoError(t, tx.Exec("SET LOCAL enable_bitmapscan=off").Error)
					require.NoError(t, tx.Exec("SET LOCAL enable_seqscan=off").Error)
					var ordered []string
					require.NoError(t, tx.Raw("EXPLAIN (COSTS OFF) "+query).Scan(&ordered).Error)
					plan := strings.Join(ordered, "\n")
					require.Contains(t, plan, "Index Scan using seo_tasks_claim_age")
					require.NotContains(t, plan, "Sort", "the static score can be served directly in index order")
					return nil
				}))
			})
		}
	}
}
