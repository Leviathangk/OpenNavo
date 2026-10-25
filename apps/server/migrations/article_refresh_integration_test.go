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

func TestRetryArticleRefreshReopensOnlyAbandonedRefreshes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("refresh_migration_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresSettings(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 91))
	_, err = db.ExecContext(ctx, `INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,rule_version,state) VALUES
 ('article_refresh','article:1','zh-CN','article-refresh:1:1','opportunities-v1','skipped'),
 ('article_refresh','article:2','zh-CN','article-refresh:2:2','opportunities-v1','quarantined'),
 ('article_refresh','article:3','zh-CN','article-refresh:3:3','opportunities-v1','done'),
 ('translate','article:4','de-DE','translate:article:4:de-DE:x','translation-v1','skipped')`)
	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 92))
	states := map[string]string{}
	rows, err := db.QueryContext(ctx, "SELECT dedupe_key,state FROM seo_tasks")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	for rows.Next() {
		var key, state string
		require.NoError(t, rows.Scan(&key, &state))
		states[key] = state
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[string]string{"article-refresh:1:1": "open", "article-refresh:2:2": "open", "article-refresh:3:3": "done", "translate:article:4:de-DE:x": "skipped"}, states)
}
