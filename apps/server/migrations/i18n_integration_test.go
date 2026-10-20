//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSixLanguageMigrationPreservesLegacyText(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("opennavo_migration_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 4))
	_, err = db.ExecContext(ctx, `INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(1,'cask','sample','sample','homebrew/cask','Sample','1','1','{}','hash'),(2,'formula','keep','keep','homebrew/core','Keep','1','1','{}','hash');
 INSERT INTO categories(id,slug,icon) VALUES(1,'tools','lucide:box');
 INSERT INTO collections(id,slug) VALUES(1,'sample');
 INSERT INTO features(id,placement,target_type,url) VALUES(1,'home_hero','url','https://example.com');
 INSERT INTO releases(id,package_id,source,source_key,version) VALUES(1,1,'manual','one','1');
 INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(1,'screenshot','test-image','https://example.com/a.png',100,100,1,'hash','image/png');
 INSERT INTO package_screenshots(id,package_id,asset_id,caption_zh,caption_en) VALUES(1,1,1,'说明','Caption');
 INSERT INTO collection_items(collection_id,package_id,note_zh,note_en) VALUES(1,1,NULL,'Note');
 INSERT INTO desktop_releases(id,version,artifacts,notes_zh,notes_en) VALUES(1,'1','[]','说明','Notes');
 INSERT INTO mirrors(id,key,name_zh,name_en,probe_url) VALUES(1,'test','镜像','Mirror','https://example.com');
 INSERT INTO package_i18n(package_id,locale,summary,status,source) VALUES(1,'zh-CN','摘要','reviewed','human'),(1,'en-US','Summary','machine','llm');
 INSERT INTO release_i18n(release_id,locale,summary,status) VALUES(1,'zh-CN','摘要','needs_review'),(1,'en-US','Summary','reviewed');
 INSERT INTO category_i18n(category_id,locale,name) VALUES(1,'zh-CN','工具'),(1,'en-US','Tools');
 INSERT INTO collection_i18n(collection_id,locale,title) VALUES(1,'zh-CN','合集'),(1,'en-US','Collection');
 INSERT INTO feature_i18n(feature_id,locale,title) VALUES(1,'zh-CN','精选'),(1,'en-US','Feature');`)
	require.NoError(t, err)
	tables := []struct{ table, key, field string }{{"package_i18n", "package_id", "summary"}, {"release_i18n", "release_id", "summary"}, {"category_i18n", "category_id", "name"}, {"collection_i18n", "collection_id", "title"}, {"feature_i18n", "feature_id", "title"}}
	before := map[string]string{}
	for _, v := range tables {
		require.NoError(t, db.QueryRowContext(ctx, fmt.Sprintf("SELECT jsonb_agg(jsonb_build_array(%s,locale,%s) ORDER BY locale)::text FROM %s", v.key, v.field, v.table)).Scan(&beforeValue{target: before, key: v.table}))
	}
	require.NoError(t, goose.UpToContext(ctx, db, ".", 5))
	version, err := goose.GetDBVersionContext(ctx, db)
	require.NoError(t, err)
	require.EqualValues(t, 5, version)
	for _, v := range tables {
		var after string
		require.NoError(t, db.QueryRowContext(ctx, fmt.Sprintf("SELECT jsonb_agg(jsonb_build_array(%s,locale,%s) ORDER BY locale)::text FROM %s", v.key, v.field, v.table)).Scan(&after))
		require.JSONEq(t, before[v.table], after)
		t.Logf("%s: before=2 after=2, text identical", v.table)
	}
	for _, v := range []struct{ table, field, expected string }{{"screenshot_i18n", "caption", `["Caption","说明"]`}, {"collection_item_i18n", "note", `["Note",null]`}, {"desktop_release_i18n", "notes", `["Notes","说明"]`}, {"mirror_i18n", "name", `["Mirror","镜像"]`}} {
		var got string
		require.NoError(t, db.QueryRowContext(ctx, fmt.Sprintf("SELECT jsonb_agg(%s ORDER BY locale)::text FROM %s", v.field, v.table)).Scan(&got))
		require.JSONEq(t, v.expected, got)
		t.Logf("%s: 1 bilingual row -> 2 locale rows, text/null identical", v.table)
	}
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM packages WHERE kind='formula'").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND column_name IN ('caption_zh','caption_en','note_zh','note_en','notes_zh','notes_en','name_zh','name_en')").Scan(&count))
	require.Zero(t, count)
	_, err = db.ExecContext(ctx, "INSERT INTO category_i18n(category_id,locale,name) VALUES(1,'ja-JP','道具'),(1,'es-ES','Herramientas'),(1,'pt-BR','Ferramentas'),(1,'ru-RU','Инструменты'); INSERT INTO releases(id,package_id,source,source_key,version) VALUES(2,1,'editorial','edited','1')")
	require.NoError(t, err)
	t.Log("migration up version 5; six languages and editorial accepted; formula retained; old columns removed")
}

type beforeValue struct {
	target map[string]string
	key    string
}

func (v *beforeValue) Scan(value any) error {
	switch value := value.(type) {
	case string:
		v.target[v.key] = value
	case []byte:
		v.target[v.key] = string(value)
	default:
		return fmt.Errorf("unexpected text type %T", value)
	}
	return nil
}
