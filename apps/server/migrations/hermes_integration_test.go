//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"

	"github.com/getkin/kin-openapi/openapi3"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestHermesMigrationsPreserveDataAndEnforceCredentialBounds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("hermes_migration_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresSettings(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 7))
	_, err = db.ExecContext(ctx, `INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'existing-admin','not-a-password-hash');
 INSERT INTO admin_roles(role_code,role_name) VALUES('R_ADMIN','Admin');
 INSERT INTO admin_permissions(code,name,group_name) VALUES('changelog:source:edit','old','changelog'),('changelog:fetch','old','changelog');
 INSERT INTO admin_role_permissions(role_id,permission_code) SELECT id,'changelog:source:edit' FROM admin_roles WHERE role_code='R_ADMIN';
 INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(1,'cask','keep','keep','homebrew/cask','Keep','1','1','{}','hash');
 INSERT INTO releases(package_id,source,source_key,version,body_markdown) VALUES(1,'manual','keep','1','old notes');
 UPDATE i18n_glossary SET translations='{"zh-CN":"保留译法"}' WHERE term='OpenNavo';`)
	require.NoError(t, err)
	require.NoError(t, goose.UpContext(ctx, db, "."))
	require.NoError(t, goose.UpContext(ctx, db, "."), "appending migrations remains idempotent")
	version, err := goose.GetDBVersionContext(ctx, db)
	require.NoError(t, err)
	available, err := goose.CollectMigrations(".", 0, goose.MaxVersion)
	require.NoError(t, err)
	latest, err := available.Last()
	require.NoError(t, err)
	require.Equal(t, latest.Version, version, "all embedded migrations must be applied")
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM admin_permissions WHERE code IN ('changelog:source:edit','changelog:fetch')").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM admin_role_permissions WHERE permission_code IN ('changelog:source:edit','changelog:fetch')").Scan(&count))
	require.Zero(t, count)
	var note, glossary string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT body_markdown FROM releases WHERE source_key='keep'").Scan(&note))
	require.Equal(t, "old notes", note)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT translations->>'zh-CN' FROM i18n_glossary WHERE term='OpenNavo'").Scan(&glossary))
	require.Equal(t, "保留译法", glossary)
	var machine bool
	require.NoError(t, db.QueryRowContext(ctx, "SELECT is_machine FROM admin_users WHERE id=1").Scan(&machine))
	require.False(t, machine)
	var permissionJSON string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT json_agg(permission_code ORDER BY permission_code)::text FROM admin_role_permissions WHERE role_id=(SELECT id FROM admin_roles WHERE role_code='R_AGENT')").Scan(&permissionJSON))
	spec, err := openapi3.NewLoader().LoadFromFile("../api/admin.openapi.yaml")
	require.NoError(t, err)
	var expected []string
	for _, value := range spec.Components.Schemas["AgentPermission"].Value.Enum {
		expected = append(expected, value.(string))
	}
	require.Len(t, expected, 24)
	slices.Sort(expected)
	require.JSONEq(t, mustJSON(t, expected), permissionJSON)
	for _, denied := range []string{"agent:manage", "i18n:settings", "system:config:edit", "system:user:edit", "system:role:edit", "system:audit:view", "release:desktop:edit", "release:desktop:publish"} {
		require.NotContains(t, expected, denied)
	}
	var calls, writes, llm, batch int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT calls_per_minute,writes_per_day,llm_ops_per_day,batch_limit FROM agent_settings").Scan(&calls, &writes, &llm, &batch))
	require.Equal(t, []int{120, 3000, 1000, 100}, []int{calls, writes, llm, batch})
	_, err = db.ExecContext(ctx, "INSERT INTO admin_users(id,user_name,password_hash,is_machine) VALUES(2,'agent-test','unusable',true); INSERT INTO agent_clients(user_id,name) VALUES(2,'Hermes')")
	require.NoError(t, err)
	var tokenID int64
	var expires, created time.Time
	var allowDelete bool
	require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO agent_tokens(client_id,name,token_hash,token_prefix) VALUES(1,'test',$1,'onv_demo') RETURNING id,expires_at,created_at,allow_delete", strings.Repeat("a", 64)).Scan(&tokenID, &expires, &created, &allowDelete))
	require.Equal(t, 90*24*time.Hour, expires.Sub(created))
	require.True(t, allowDelete)
	for _, query := range []string{
		"UPDATE agent_tokens SET permissions=ARRAY['system:config:edit'] WHERE id=1",
		"UPDATE agent_tokens SET token_hash='not-a-hash' WHERE id=1",
		"UPDATE agent_tokens SET token_prefix='too-long-to-store-a-token' WHERE id=1",
		"UPDATE agent_tokens SET ip_allowlist=ARRAY['not-an-ip']::cidr[] WHERE id=1",
		"UPDATE agent_settings SET batch_limit=101",
	} {
		_, err = db.ExecContext(ctx, query)
		require.Error(t, err, query)
	}
	_, err = db.ExecContext(ctx, "UPDATE agent_tokens SET permissions='{}',expires_at=NULL,ip_allowlist=ARRAY['192.0.2.0/24','2001:db8::/32']::cidr[] WHERE id=1")
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_name='agent_tokens' AND column_name IN ('token','plaintext','secret')").Scan(&count))
	require.Zero(t, count)
	// Trash snapshots and asset references are independent of revisions; revision cleanup must not shorten retention.
	_, err = db.ExecContext(ctx, `INSERT INTO content_trash(entity,object_key,snapshot,snapshot_hash,asset_ids,actor_type,request_id) VALUES('collection','9','{"id":9,"i18n":{"zh-CN":{"title":"保留"}}}',repeat('a',64),ARRAY[7::bigint],'admin','delete-1');
 INSERT INTO content_revision_requests(request_id,revision_count) VALUES('delete-1',1);
 INSERT INTO content_revisions(entity,object_key,version,action,operation_id,required_permissions,actor_type,request_id,before_data,after_data) VALUES('collection','9',1,'delete','deleteCollection',ARRAY['content:collection:edit'],'admin','delete-1','{"id":9}',NULL);
 DELETE FROM content_revisions WHERE object_key='9';`)
	require.NoError(t, err)
	var retention float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT extract(epoch FROM expires_at-deleted_at) FROM content_trash WHERE object_key='9'").Scan(&retention))
	require.Equal(t, 30*24*time.Hour.Seconds(), retention)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM catalog_changes WHERE event_type IS NOT NULL").Scan(&count))
	require.Zero(t, count, "migration must not fabricate historical new-version events")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
