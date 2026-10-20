//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestEnglishDefaultsPreserveExistingSourceLanguages(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("english_defaults"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresTestPorts(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 13))
	_, err = db.ExecContext(ctx, `INSERT INTO categories(slug,icon) VALUES('legacy','lucide:box');
 INSERT INTO app_config(key,value) VALUES('site.about','{"locales":{"zh-CN":[]}}'),('desktop.announcement','{"sourceLocale":"ja-JP","title":"日本語"}');`)
	require.NoError(t, err)
	require.NoError(t, goose.UpContext(ctx, db, "."))
	_, err = db.ExecContext(ctx, `INSERT INTO categories(slug,icon) VALUES('new','lucide:box');
 INSERT INTO categories(slug,icon,source_locale) VALUES('explicit','lucide:box','zh-CN');`)
	require.NoError(t, err)
	for slug, want := range map[string]string{"legacy": "zh-CN", "new": "en-US", "explicit": "zh-CN"} {
		var got string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT source_locale FROM categories WHERE slug=$1", slug).Scan(&got))
		require.Equal(t, want, got)
	}
	for key, want := range map[string]string{"site.about": "zh-CN", "desktop.announcement": "ja-JP"} {
		var got string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT value->>'sourceLocale' FROM app_config WHERE key=$1", key).Scan(&got))
		require.Equal(t, want, got)
	}
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND column_name='source_locale' AND column_default LIKE '%zh-CN%'`).Scan(&count))
	require.Zero(t, count)
	for _, test := range []struct {
		name, source, want string
	}{
		{"missing", "", "zh-CN"},
		{"null", `,"sourceLocale":null`, "zh-CN"},
		{"empty", `,"sourceLocale":""`, "zh-CN"},
		{"explicitEnglish", `,"sourceLocale":"en-US"`, "en-US"},
		{"explicitJapanese", `,"sourceLocale":"ja-JP"`, "ja-JP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, goose.DownToContext(ctx, db, ".", 13))
			for _, key := range []string{"site.about", "desktop.announcement"} {
				_, err := db.ExecContext(ctx, `UPDATE app_config SET value=$1::jsonb WHERE key=$2`, `{"title":"Legacy content"`+test.source+`}`, key)
				require.NoError(t, err)
			}
			require.NoError(t, goose.UpContext(ctx, db, "."))
			for _, key := range []string{"site.about", "desktop.announcement"} {
				var source, title string
				require.NoError(t, db.QueryRowContext(ctx, "SELECT value->>'sourceLocale',value->>'title' FROM app_config WHERE key=$1", key).Scan(&source, &title))
				require.Equal(t, test.want, source, key)
				require.Equal(t, "Legacy content", title, key)
			}
		})
	}

}
