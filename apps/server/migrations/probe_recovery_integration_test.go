//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestProbeFailureRecoveryRequeuesOnlyProbeFalseNegatives(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 110)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	const robots = "public canonical or robots differs from page decision"
	rows := []struct {
		url, state, lastError string
		attempts              int
		leased, announce      bool
	}{
		{"https://example.test/articles/dead", "dead", robots, 12, false, true},
		{"https://example.test/articles/retrying", "invalidated", robots, 3, false, true},
		{"https://example.test/apps/shed", "invalidated", "public page probe returned 503", 2, false, true},
		{"https://example.test/apps/leased", "invalidated", robots, 2, true, true},
		{"https://example.test/apps/broken", "dead", "public page probe returned 502", 12, false, true},
		{"https://example.test/apps/purge", "dead", "edge purge incomplete: status 500", 12, false, true},
		{"https://example.test/articles/superseded", "dead", robots, 12, false, true},
		{"https://example.test/articles/superseded", "announced", "", 0, false, true},
		{"https://example.test/articles/live-again", "dead", robots, 12, false, true},
		{"https://example.test/articles/live-again", "pending", "", 0, false, true},
	}
	for _, row := range rows {
		var token, until any
		if row.leased {
			token, until = "owner", time.Now().Add(time.Minute)
		}
		require.NoError(t, st.DB.Exec(`INSERT INTO url_notify_outbox(url,path,locale,reason,announce,robots,state,attempts,last_error,lease_token,lease_until,next_attempt_at)
 VALUES(?,?,'en-US','updated',?,'index, follow',?,?,?,?,?,now()+interval '1 day')`,
			row.url, strings.TrimPrefix(row.url, "https://example.test"), row.announce, row.state, row.attempts, row.lastError, token, until).Error)
	}
	require.NoError(t, goose.UpToContext(ctx, db, ".", 111))
	type receipt struct {
		URL, State, LastError string
		Attempts              int
		Due                   bool
	}
	var got []receipt
	require.NoError(t, st.DB.Raw(`SELECT url,state,last_error,attempts,next_attempt_at<=now() AS due FROM url_notify_outbox ORDER BY id`).Scan(&got).Error)
	require.Equal(t, []receipt{
		{"https://example.test/articles/dead", "pending", "", 0, true},
		{"https://example.test/articles/retrying", "invalidated", "", 0, true},
		{"https://example.test/apps/shed", "invalidated", "", 0, true},
		{"https://example.test/apps/leased", "invalidated", robots, 2, false},
		{"https://example.test/apps/broken", "dead", "public page probe returned 502", 12, false},
		{"https://example.test/apps/purge", "dead", "edge purge incomplete: status 500", 12, false},
		{"https://example.test/articles/superseded", "dead", robots, 12, false},
		{"https://example.test/articles/superseded", "announced", "", 0, false},
		{"https://example.test/articles/live-again", "dead", robots, 12, false},
		{"https://example.test/articles/live-again", "pending", "", 0, false},
	}, got)
	require.NoError(t, goose.DownToContext(ctx, db, ".", 110))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 111))
}
