//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSiteLocaleMigrationAndLosslessRollback(t *testing.T) {
	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	hostPort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	pg, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("site_locales_test"), postgres.WithUsername("test"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), testutil.PostgresSettings(), testcontainers.WithHostConfigModifier(func(h *container.HostConfig) {
		h.PortBindings = network.PortMap{network.MustParsePort("5432/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: hostPort}}}
	}))
	if pg != nil {
		t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	}
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 32))

	// Inspect every existing locale constraint, including nullable revision locales and article
	// translations. Test the actual PostgreSQL expression, not a text copy of the migration.
	baseline := map[string]bool{}
	check := func(site bool, capture bool) {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT r.relname,a.attname,pg_get_expr(c.conbin,c.conrelid)
FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid
JOIN pg_namespace n ON n.oid=r.relnamespace
JOIN pg_attribute a ON a.attrelid=r.oid AND a.attnum=c.conkey[1]
WHERE n.nspname='public' AND c.contype='c' AND cardinality(c.conkey)=1
AND a.attname IN ('locale','source_locale') ORDER BY r.relname,a.attname`)
		require.NoError(t, err)
		defer func() { require.NoError(t, rows.Close()) }()
		type constraint struct{ table, column, expr string }
		var constraints []constraint
		for rows.Next() {
			var c constraint
			require.NoError(t, rows.Scan(&c.table, &c.column, &c.expr))
			if capture {
				baseline[c.table+"."+c.column] = true
			}
			if baseline[c.table+"."+c.column] {
				constraints = append(constraints, c)
			}
		}
		require.NoError(t, rows.Err())
		require.Len(t, constraints, 35)
		for _, c := range constraints {
			for _, locale := range []string{"en-US", "zh-CN", "ja-JP", "es-ES", "pt-BR", "ru-RU", "de-DE", "fr-FR", "it-IT", "zh-Hant", "invalid"} {
				var accepted bool
				require.NoError(t, db.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM (SELECT $1::text AS %s) input`, c.expr, c.column), locale).Scan(&accepted))
				newLocale := locale == "de-DE" || locale == "fr-FR" || locale == "it-IT" || locale == "zh-Hant"
				want := locale != "invalid" && (!newLocale || site && !strings.HasPrefix(c.table, "desktop_release"))
				require.Equal(t, want, accepted, "%s.%s/%s", c.table, c.column, locale)
			}
		}
	}
	check(false, true)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 50))
	check(true, false)
	_, err = db.ExecContext(ctx, `INSERT INTO categories(id,slug,icon,source_locale) VALUES(987,'locale-fixture','lucide:box','de-DE')`)
	require.NoError(t, err)
	require.Error(t, goose.DownContext(ctx, db, "."), "rollback must not silently delete new-language content")
	var source string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT source_locale FROM categories WHERE id=987`).Scan(&source))
	require.Equal(t, "de-DE", source)
	check(true, false) // A failed Down leaves all widened constraints intact.
	_, err = db.ExecContext(ctx, `DELETE FROM categories WHERE id=987`)
	require.NoError(t, err)
	require.NoError(t, goose.DownContext(ctx, db, "."))
	check(false, false)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 50))
	check(true, false)
}
