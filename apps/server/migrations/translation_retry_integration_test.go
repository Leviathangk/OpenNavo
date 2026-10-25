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

func TestRetryRejectedTranslationsReopensOnlyFalsePositives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("translation_retry_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresSettings(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 92))
	_, err = db.ExecContext(ctx, `INSERT INTO seo_tasks(id,kind,entity_key,locale,dedupe_key,rule_version,state,result) VALUES
 (1,'translate','package:1','de-DE','t1','translation-v1','skipped','{"outcome":"skipped","reason":"de-DE validation_failed:changed_literal twice"}'),
 (2,'translate','package:2','zh-Hant','t2','translation-v1','skipped','{"actionId":9}'),
 (3,'translate','package:3','fr-FR','t3','translation-v1','skipped','{"outcome":"skipped","reason":"handled by release process"}'),
 (4,'translate','package:4','it-IT','t4','translation-v1','done','{"outcome":"done","reason":"changed_literal fixed"}'),
 (5,'maintenance','pkg:5','en-US','t5','opportunities-v1','skipped','{"outcome":"skipped","reason":"changed_literal"}');
 INSERT INTO seo_gate_events(request_id,task_id,owner,operation,rule_version,result,reason) VALUES
 ('r2',2,'agent:1:2','WriteTranslations','fixture','rejected','zh-Hant:test_artifact_required'),
 ('r3',3,'agent:1:2','WriteTranslations','fixture','rejected','fr-FR:validation_failed:wrong_language')`)
	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 93))
	states := map[int]string{}
	rows, err := db.QueryContext(ctx, "SELECT id,state FROM seo_tasks")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	for rows.Next() {
		var id int
		var state string
		require.NoError(t, rows.Scan(&id, &state))
		states[id] = state
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[int]string{1: "open", 2: "open", 3: "skipped", 4: "done", 5: "skipped"}, states)
}
