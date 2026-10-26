package observability

import (
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type fakeQueueInspector struct {
	calls   atomic.Int32
	blocked <-chan struct{}
	err     error
}

func (f *fakeQueueInspector) Queues() ([]string, error) {
	f.calls.Add(1)
	if f.blocked != nil {
		<-f.blocked
	}
	return []string{"critical"}, f.err
}
func (f *fakeQueueInspector) GetQueueInfo(name string) (*asynq.QueueInfo, error) {
	return &asynq.QueueInfo{Queue: name, Pending: 7, Scheduled: 11, Retry: 3, Archived: 4, Active: 1, Aggregating: 2, Latency: 6 * time.Second}, nil
}
func testQueueCollector(t *testing.T, f queueInspector) *QueueMetrics {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "unused"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	m := NewQueueMetrics(client)
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	m.inspector = f
	return m
}

func TestQueueMetricsExposeStatesLatencyAndCache(t *testing.T) {
	f := &fakeQueueInspector{}
	m := testQueueCollector(t, f)
	expected := `# HELP asynq_queue_size Tasks in each Asynq queue by state
# TYPE asynq_queue_size gauge
asynq_queue_size{queue="critical",state="active"} 1
asynq_queue_size{queue="critical",state="aggregating"} 2
asynq_queue_size{queue="critical",state="archived"} 4
asynq_queue_size{queue="critical",state="pending"} 7
asynq_queue_size{queue="critical",state="retry"} 3
asynq_queue_size{queue="critical",state="scheduled"} 11
# HELP asynq_queue_latency_seconds Age of the oldest pending task
# TYPE asynq_queue_latency_seconds gauge
asynq_queue_latency_seconds{queue="critical"} 6
# HELP opennavo_queue_metrics_collection_success Whether the last queue inspection succeeded
# TYPE opennavo_queue_metrics_collection_success gauge
opennavo_queue_metrics_collection_success 1
`
	registry := prometheus.NewRegistry()
	registry.MustRegister(m)
	for range 2 {
		w := httptest.NewRecorder()
		promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
		require.Equal(t, 200, w.Code)
		for _, line := range strings.Split(strings.TrimSpace(expected), "\n") {
			require.Contains(t, w.Body.String(), line+"\n")
		}
	}
	require.Equal(t, int32(1), f.calls.Load())
}

func TestQueueMetricsOutageIsBoundedAndDoesNotSpawnRepeatedInspections(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	f := &fakeQueueInspector{blocked: blocked}
	m := testQueueCollector(t, f)
	m.timeout, m.ttl = 20*time.Millisecond, 0
	registry := prometheus.NewRegistry()
	registry.MustRegister(m)
	for range 3 {
		start := time.Now()
		families, err := registry.Gather()
		require.NoError(t, err)
		require.Less(t, time.Since(start), time.Second)
		require.Len(t, families, 1)
		require.Equal(t, "opennavo_queue_metrics_collection_success", families[0].GetName())
		require.Zero(t, families[0].Metric[0].Gauge.GetValue())
	}
	require.Equal(t, int32(1), f.calls.Load())
}

func TestQueueMetricsRedisErrorDoesNotReportZeroBacklog(t *testing.T) {
	m := testQueueCollector(t, &fakeQueueInspector{err: errors.New("unavailable")})
	registry := prometheus.NewRegistry()
	registry.MustRegister(m)
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)
	require.Equal(t, "opennavo_queue_metrics_collection_success", families[0].GetName())
	require.Zero(t, families[0].Metric[0].Gauge.GetValue())
}
