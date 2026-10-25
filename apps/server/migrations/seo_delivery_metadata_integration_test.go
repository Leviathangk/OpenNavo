//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestSEODeliveryRecoveryPreservesNewerWork(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 102)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES('cask','cached-fixture','cached-fixture','homebrew/cask','Fixture','1','1','{}','fixture')`).Error)
	insert := func(path, state, problem string) int64 {
		var id int64
		require.NoError(t, st.DB.Raw(`INSERT INTO url_notify_outbox(url,path,locale,reason,state,attempts,last_error,lease_token,lease_until)
 VALUES(?,?,'en-US','updated',?,12,?,'fixture-lease',now()+interval '1 hour') RETURNING id`, "https://site.example"+path, path, state, problem).Scan(&id).Error)
		return id
	}
	quota := "edge purge incomplete: status 429"
	policy := "public canonical or robots differs from page decision"
	retry := []int64{insert("/apps/throttled", "dead", quota), insert("/fr/apps/empty/dependencies", "dead", policy)}
	keepDead := []int64{insert("/apps/credentials", "dead", "edge purge incomplete: status 401"), insert("/apps/other", "dead", policy)}
	keepDead = append(keepDead, insert("/apps/active", "dead", quota))
	active := insert("/apps/active", "probed", "")
	keepDead = append(keepDead, insert("/apps/delivered", "dead", quota))
	delivered := insert("/apps/delivered", "announced", "")
	require.NoError(t, goose.UpToContext(ctx, db, ".", 103))
	var invalidation string
	require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE unique_key='cache:invalidate:seo-delivery-metadata'").Scan(&invalidation).Error)
	require.JSONEq(t, `["c:pkg:*","c:dep:*"]`, invalidation)
	for _, id := range retry {
		var row struct {
			State      string
			Attempts   int
			LeaseToken *string
		}
		require.NoError(t, st.DB.Table("url_notify_outbox").Where("id=?", id).Take(&row).Error)
		require.Equal(t, "pending", row.State)
		require.Zero(t, row.Attempts)
		require.Nil(t, row.LeaseToken)
	}
	for _, id := range keepDead {
		var state string
		require.NoError(t, st.DB.Raw("SELECT state FROM url_notify_outbox WHERE id=?", id).Scan(&state).Error)
		require.Equal(t, "dead", state)
	}
	for id, want := range map[int64]string{active: "probed", delivered: "announced"} {
		var row struct {
			State, LeaseToken string
			Attempts          int
		}
		require.NoError(t, st.DB.Table("url_notify_outbox").Where("id=?", id).Take(&row).Error)
		require.Equal(t, want, row.State)
		require.Equal(t, "fixture-lease", row.LeaseToken)
		require.Equal(t, 12, row.Attempts)
	}
}
