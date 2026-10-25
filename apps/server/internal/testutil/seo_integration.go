//go:build integration

package testutil

import (
	"context"
	"net"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewSEOStore starts only PostgreSQL: SEO ledgers do not depend on Redis.
// Explicit-port mode works around Docker Desktop versions that do not allocate HostPort="0".
// The caller may disable Ryuk for that mode; cleanup is always registered here.
func NewSEOStore(t *testing.T) *store.Store {
	t.Helper()
	return NewSEOStoreAtVersion(t, 0)
}

// NewSEOStoreAtVersion builds a historical schema directly, without rolling back
// later irreversible migrations. Zero selects the latest schema.
func NewSEOStoreAtVersion(t *testing.T, version int64) *store.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := []testcontainers.ContainerCustomizer{pgcontainer.WithDatabase("seo_test"), pgcontainer.WithUsername("seo_test"), pgcontainer.WithPassword("isolated-test-only"), pgcontainer.BasicWaitStrategies(), PostgresSettings()}
	if os.Getenv("OPENNAVO_TEST_EXPLICIT_PORTS") == "1" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
		require.NoError(t, listener.Close())
		opts = append(opts, testcontainers.WithHostConfigModifier(func(c *container.HostConfig) {
			p, err := network.ParsePort("5432/tcp")
			require.NoError(t, err)
			c.PortBindings = network.PortMap{p: []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: port}}}
		}))
	}
	pg, err := pgcontainer.Run(ctx, "postgres:18", opts...)
	if pg != nil {
		t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	}
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(5)
	t.Cleanup(func() { require.NoError(t, sql.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	if version == 0 {
		require.NoError(t, goose.UpContext(ctx, sql, "."))
	} else {
		require.NoError(t, goose.UpToContext(ctx, sql, ".", version))
	}
	return &store.Store{DB: db}
}
