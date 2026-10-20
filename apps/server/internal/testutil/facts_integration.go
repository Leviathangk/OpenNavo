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
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewFactsStore adds Redis to the isolated PostgreSQL helper. Explicit localhost ports support
// Docker Desktop versions which do not allocate port zero. Every container is explicitly cleaned up.
func NewFactsStore(t *testing.T) *store.Store {
	t.Helper()
	st := NewSEOStore(t)
	req := testcontainers.ContainerRequest{Image: "public.ecr.aws/docker/library/redis:8", ExposedPorts: []string{"6379/tcp"}, WaitingFor: wait.ForListeningPort("6379/tcp")}
	if os.Getenv("OPENNAVO_TEST_EXPLICIT_PORTS") == "1" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
		require.NoError(t, listener.Close())
		req.HostConfigModifier = func(c *container.HostConfig) {
			p, err := network.ParsePort("6379/tcp")
			require.NoError(t, err)
			c.PortBindings = network.PortMap{p: []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: port}}}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if r != nil {
		t.Cleanup(func() { require.NoError(t, r.Terminate(context.Background())) })
	}
	require.NoError(t, err)
	host, err := r.Host(ctx)
	require.NoError(t, err)
	port, err := r.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	st.Redis = redis.NewClient(&redis.Options{Addr: net.JoinHostPort(host, port.Port())})
	t.Cleanup(func() { require.NoError(t, st.Redis.Close()) })
	require.NoError(t, st.Redis.Ping(ctx).Err())
	return st
}
