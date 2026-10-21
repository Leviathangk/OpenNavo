//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestVersionHistoryMigrationRereadsHistories(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("version_history_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresSettings(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 26))
	_, err = db.ExecContext(ctx, `INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (1,'cask','fetched','fetched','homebrew/cask','Fetched','1','1','{}','a'),
 (2,'cask','pending','pending','homebrew/cask','Pending','1','1','{}','b'),
 (3,'cask','swiftvm','swiftvm-app/tap/swiftvm','swiftvm-app/tap','SwiftVM','1.3.0','1.3.0','{}','c');
 INSERT INTO changelog_sources(package_id,type,resolved_by,etag,last_modified,last_fetched_at,last_status,next_fetch_at) VALUES
 (1,'homebrew_commits','auto','"v1"','Mon, 12 Oct 2026 00:00:00 GMT','2026-10-14T00:00:00Z','ok','2026-10-16T00:00:00Z'),
 (2,'homebrew_commits','auto',NULL,NULL,NULL,'never','2026-10-15T00:00:00Z');
 INSERT INTO tap_casks(package_id,tap,source_path,head_sha) VALUES(3,'swiftvm-app/tap','Casks/swiftvm.rb','abc');`)
	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 27))

	var etag, lastModified, fetchedAt sql.NullString
	var status string
	var next time.Time
	require.NoError(t, db.QueryRowContext(ctx, "SELECT etag,last_modified,last_fetched_at::text,last_status,next_fetch_at FROM changelog_sources WHERE package_id=1").Scan(&etag, &lastModified, &fetchedAt, &status, &next))
	require.False(t, etag.Valid || lastModified.Valid || fetchedAt.Valid, "the incremental cursor is dropped")
	require.Equal(t, "ok", status)
	require.True(t, next.Equal(time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)), "the read keeps its schedule")
	require.NoError(t, db.QueryRowContext(ctx, "SELECT last_status FROM changelog_sources WHERE package_id=2").Scan(&status))
	require.Equal(t, "never", status)
	var history sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, "SELECT history_sha FROM tap_casks WHERE package_id=3").Scan(&history))
	require.False(t, history.Valid, "existing tap casks read their history on the next sync")

	require.NoError(t, goose.DownToContext(ctx, db, ".", 26))
	require.NoError(t, goose.UpContext(ctx, db, "."))
}
