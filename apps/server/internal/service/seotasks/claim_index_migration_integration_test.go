//go:build integration

package seotasks_test

import (
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClaimAgingIndexUpgradePreservesTaskData(t *testing.T) {
	st := testutil.NewSEOStore(t)
	// A small fixture represents accumulated terminal imports plus a scheduled retry.
	// ANALYZE records index membership; this is not a timing or load test.
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_tasks(kind,entity_key,dedupe_key,rule_version,state,next_retry_at)
 SELECT 'hermes_import','category:'||i,'index-fixture-'||i,'fixture','done',
 CASE WHEN i=1 THEN now()+interval '1 hour' END FROM generate_series(1,64) i`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_tasks(kind,entity_key,dedupe_key,rule_version,state)
 VALUES('fill_fact','category:65','index-fixture-65','fixture','open'),
 ('fill_fact','category:66','index-fixture-66','fixture','done')`).Error)
	rowState := func() string {
		t.Helper()
		var state string
		require.NoError(t, st.DB.Raw(`SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM seo_tasks t`).Scan(&state).Error)
		return state
	}
	before := rowState()
	indexRows := func() int {
		t.Helper()
		require.NoError(t, st.DB.Exec("ANALYZE seo_tasks").Error)
		var count int
		require.NoError(t, st.DB.Raw("SELECT reltuples::integer FROM pg_class WHERE oid='seo_tasks_claim_age'::regclass").Scan(&count).Error)
		return count
	}
	raw, err := migrations.Files.ReadFile("00083_seo_claim_retry_index.sql")
	require.NoError(t, err)
	parts := strings.Split(string(raw), "-- +goose Down")
	require.Len(t, parts, 2)
	apply := func(sql string) {
		t.Helper()
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return tx.Exec(sql).Error }))
		require.JSONEq(t, before, rowState(), "index migration must preserve task history and retry state")
	}
	require.Equal(t, 2, indexRows(), "fresh migration includes only open work and recurring imports")
	apply(parts[1]) // Original 00080 shape, representing an already migrated database.
	require.Equal(t, 65, indexRows(), "old index also retained terminal imports")
	apply(parts[0])
	require.Equal(t, 2, indexRows(), "forward migration removes terminal imports from index membership")
	apply(parts[1])
	apply(parts[0])
	require.Equal(t, 2, indexRows())
}
