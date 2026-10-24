//go:build integration

package store_test

import (
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTraditionalMigrationCanUpgradeAfterDown(t *testing.T) {
	st := testutil.NewSEOStore(t) // Fresh Up has already run.
	require.NoError(t, st.DB.Exec(`INSERT INTO derived_traditional(entity,object_key,simplified_hash,converter,before_data,after_data)
 VALUES('category','migration-fixture','hash','fixture','{"name":"original"}','{"name":"derived"}')`).Error)
	raw, err := migrations.Files.ReadFile("00102_derived_traditional.sql")
	require.NoError(t, err)
	parts := strings.Split(string(raw), "-- +goose Down")
	require.Len(t, parts, 2)
	var before string
	require.NoError(t, st.DB.Raw("SELECT to_jsonb(d)::text FROM derived_traditional d WHERE object_key='migration-fixture'").Scan(&before).Error)
	for i := 0; i < 2; i++ {
		for _, sql := range []string{parts[1], parts[0]} {
			require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return tx.Exec(sql).Error }))
			var after string
			require.NoError(t, st.DB.Raw("SELECT to_jsonb(d)::text FROM derived_traditional d WHERE object_key='migration-fixture'").Scan(&after).Error)
			require.JSONEq(t, before, after, "down/up must preserve the original and derived snapshots")
		}
	}
}
