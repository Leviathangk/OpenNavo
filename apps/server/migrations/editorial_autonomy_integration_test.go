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

func TestEditorialAutonomyRecoversOnlyCurrentValidatorFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("editorial_autonomy_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresSettings(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 96))
	_, err = db.ExecContext(ctx, `INSERT INTO content_translation_sources(entity,object_key,ref,source_locale,source_hash,fields) VALUES('package','1','{}','en-US','current','{}');
 INSERT INTO seo_tasks(id,kind,entity_key,locale,dedupe_key,rule_version,state,result,evidence_hash) VALUES
 (1,'translate','package:1','de-DE','t1','translation-v1','quarantined','{"reason":"validation_failed:changed_literal"}','current'),
 (2,'translate','package:1','fr-FR','t2','translation-v1','quarantined','{"reason":"manual review required"}','current'),
 (3,'translate','package:1','it-IT','t3','translation-v1','quarantined','{"reason":"changed_literal"}','obsolete'),
 (4,'translate','package:1','ja-JP','t4','translation-v1','done','{"reason":"changed_literal"}','current'),
 (5,'article','article:1','en-US','t5','fixture','quarantined','{"reason":"changed_literal"}','current'),
 (6,'translate','package:1','zh-Hant','t6','translation-v1','skipped','{"reason":"validation_failed:changed_brand (Arc)"}','current'),
 (7,'translate','package:1','es-ES','t7','translation-v1','quarantined','{"reason":"missing_field: summary"}','current')`)

	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 97))
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
	require.Equal(t, map[int]string{1: "open", 2: "quarantined", 3: "quarantined", 4: "done", 5: "quarantined", 6: "open", 7: "quarantined"}, states)
}
