//go:build integration

package testutil

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func NewStore(t *testing.T) *store.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := []testcontainers.ContainerCustomizer{postgres.WithDatabase("opennavo_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies(), PostgresSettings()}
	if os.Getenv("OPENNAVO_TEST_EXPLICIT_PORTS") == "1" {
		opts = append(opts, testcontainers.WithHostConfigModifier(explicitTestPort(t, "5432/tcp")))
	}
	pg, err := postgres.Run(ctx, "postgres:18", opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	redisRequest := testcontainers.ContainerRequest{Image: "redis:8", ExposedPorts: []string{"6379/tcp"}, WaitingFor: wait.ForAll(wait.ForLog("Ready to accept connections"), wait.ForListeningPort("6379/tcp"))}
	if os.Getenv("OPENNAVO_TEST_EXPLICIT_PORTS") == "1" {
		redisRequest.HostConfigModifier = explicitTestPort(t, "6379/tcp")
	}
	rdb, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: redisRequest, Started: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rdb.Terminate(context.Background())) })
	host, err := rdb.Host(ctx)
	require.NoError(t, err)
	port, err := rdb.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	st, err := store.Open(ctx, config.Config{DatabaseURL: dsn, RedisURL: fmt.Sprintf("redis://%s:%s/0", host, port.Port()), DBMaxOpenConns: 5})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	sqlDB, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(ctx, sqlDB, "."))
	return st
}

// Opt-in workaround for Docker Desktop versions that leave ephemeral host ports
// unassigned. CI retains the normal testcontainers allocation behavior.
func explicitTestPort(t *testing.T, port string) func(*container.HostConfig) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	hostPort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	containerPort, err := network.ParsePort(port)
	require.NoError(t, err)
	return func(c *container.HostConfig) {
		c.PortBindings = network.PortMap{containerPort: []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: hostPort}}}
	}
}

// PostgresSettings runs test servers with production's JIT setting (off since
// 2026-10-10). With JIT on, the planner's estimate for the background
// declaration trigger crosses the JIT threshold, and every packages insert or
// artifact update spends about 110 ms compiling a query that runs in microseconds.
func PostgresSettings() testcontainers.ContainerCustomizer {
	return testcontainers.WithCmdArgs("-c", "jit=off")
}

// PostgresTestPorts applies the same optional Docker Desktop workaround to
// migration tests, which need to control how far migrations are applied.
func PostgresTestPorts(t *testing.T) testcontainers.ContainerCustomizer {
	if os.Getenv("OPENNAVO_TEST_EXPLICIT_PORTS") == "1" {
		return testcontainers.WithHostConfigModifier(explicitTestPort(t, "5432/tcp"))
	}
	return testcontainers.WithHostConfigModifier(func(*container.HostConfig) {})
}
