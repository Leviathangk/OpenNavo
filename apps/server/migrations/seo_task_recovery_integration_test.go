//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The replay of history that was already skipped as a backlog ends; changes made
// after the opportunity job first ran are still delivered.
func TestSEOTaskRecoveryResumesCatalogCursorAtFirstRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("seo_task_recovery_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresSettings(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 93))
	_, err = db.ExecContext(ctx, `INSERT INTO catalog_changes(seq,package_id,kind,token,op,reason,created_at) OVERRIDING SYSTEM VALUE VALUES
 (100,1,'cask','old-a','upsert','homebrew',now()-interval '10 days'),
 (200,2,'cask','old-b','upsert','content',now()-interval '3 days'),
 (300,3,'cask','new-c','upsert','homebrew',now()-interval '1 hour');
 INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,rule_version,created_at) VALUES('maintenance','pkg:1','en-US','catalog:pkg:1','opportunities-v1',now()-interval '1 day');
 UPDATE seo_opportunity_cursors SET cursor=100 WHERE source='catalog_changes'`)
	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 94))
	var cursor int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT cursor FROM seo_opportunity_cursors WHERE source='catalog_changes'").Scan(&cursor))
	require.EqualValues(t, 200, cursor)
	var pending sql.NullString
	var reopens int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT pending_evidence::text,reopen_count FROM seo_tasks").Scan(&pending, &reopens))
	require.False(t, pending.Valid)
	require.Zero(t, reopens)
}
