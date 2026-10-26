//go:build integration

package observability

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"
)

func TestQueueMetricsReadRealAsynqQueues(t *testing.T) {
	st := testutil.NewStore(t)
	client := asynq.NewClientFromRedisClient(st.Redis)
	inspector := asynq.NewInspectorFromRedisClient(st.Redis)
	_, err := client.EnqueueContext(context.Background(), asynq.NewTask("fixture:pending", nil), asynq.Queue("critical"))
	require.NoError(t, err)
	_, err = client.EnqueueContext(context.Background(), asynq.NewTask("fixture:scheduled", nil), asynq.Queue("default"), asynq.ProcessIn(time.Hour))
	require.NoError(t, err)
	archived, err := client.EnqueueContext(context.Background(), asynq.NewTask("fixture:archived", nil), asynq.Queue("default"))
	require.NoError(t, err)
	require.NoError(t, inspector.ArchiveTask("default", archived.ID))
	m := NewQueueMetrics(st.Redis)
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	registry := prometheus.NewRegistry()
	registry.MustRegister(m)
	w := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, w.Code)
	for _, expected := range []string{
		`asynq_queue_size{queue="critical",state="pending"} 1`,
		`asynq_queue_size{queue="default",state="scheduled"} 1`,
		`asynq_queue_size{queue="default",state="archived"} 1`,
		`asynq_queue_size{queue="default",state="retry"} 0`,
		`asynq_queue_latency_seconds{queue="critical"}`,
		`opennavo_queue_metrics_collection_success 1`,
	} {
		require.Contains(t, w.Body.String(), expected)
	}
}
