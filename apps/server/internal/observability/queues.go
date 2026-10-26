package observability

import (
	"context"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

type queueInspector interface {
	Queues() ([]string, error)
	GetQueueInfo(string) (*asynq.QueueInfo, error)
}

type queueSnapshot struct {
	queues []*asynq.QueueInfo
	err    error
}

// QueueMetrics uses a separate, short-timeout connection pool so a Redis outage
// cannot borrow the API's business pool or stall the metrics endpoint.
type QueueMetrics struct {
	client                 *redis.Client
	inspector              queueInspector
	mu                     sync.Mutex
	loaded                 time.Time
	snapshot               queueSnapshot
	pending                chan queueSnapshot
	timeout, ttl           time.Duration
	size, latency, success *prometheus.Desc
}

func NewQueueMetrics(business *redis.Client) *QueueMetrics {
	options := *business.Options()
	options.DialTimeout, options.ReadTimeout, options.WriteTimeout, options.PoolTimeout = 250*time.Millisecond, 250*time.Millisecond, 250*time.Millisecond, 250*time.Millisecond
	options.MaxRetries, options.PoolSize, options.MinIdleConns = -1, 1, 0
	client := redis.NewClient(&options)
	return &QueueMetrics{
		client: client, inspector: asynq.NewInspectorFromRedisClient(client), timeout: time.Second, ttl: 15 * time.Second,
		size:    prometheus.NewDesc("asynq_queue_size", "Tasks in each Asynq queue by state", []string{"queue", "state"}, nil),
		latency: prometheus.NewDesc("asynq_queue_latency_seconds", "Age of the oldest pending task", []string{"queue"}, nil),
		success: prometheus.NewDesc("opennavo_queue_metrics_collection_success", "Whether the last queue inspection succeeded", nil, nil),
	}
}

func (m *QueueMetrics) Close() error { return m.client.Close() }
func (m *QueueMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.size
	ch <- m.latency
	ch <- m.success
}

func (m *QueueMetrics) load(ctx context.Context) queueSnapshot {
	names, err := m.inspector.Queues()
	if err != nil {
		return queueSnapshot{err: err}
	}
	result := queueSnapshot{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return queueSnapshot{err: err}
		}
		info, err := m.inspector.GetQueueInfo(name)
		if err != nil {
			return queueSnapshot{err: err}
		}
		result.queues = append(result.queues, info)
	}
	result.err = ctx.Err()
	return result
}

func (m *QueueMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.loaded) >= m.ttl {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		// At most one inspection can be in flight, even if a driver ignores deadlines.
		if m.pending == nil {
			m.pending = make(chan queueSnapshot, 1)
			pending := m.pending
			go func() { pending <- m.load(ctx) }()
		}
		select {
		case m.snapshot = <-m.pending:
			m.pending = nil
		case <-ctx.Done():
			m.snapshot = queueSnapshot{err: ctx.Err()}
		}
		cancel()
		m.loaded = time.Now()
	}
	success := float64(1)
	if m.snapshot.err != nil {
		success = 0
	}
	ch <- prometheus.MustNewConstMetric(m.success, prometheus.GaugeValue, success)
	if success == 0 {
		return
	} // Missing observations must not look like empty queues.
	for _, q := range m.snapshot.queues {
		for state, value := range map[string]int{"pending": q.Pending, "scheduled": q.Scheduled, "retry": q.Retry, "archived": q.Archived, "active": q.Active, "aggregating": q.Aggregating} {
			ch <- prometheus.MustNewConstMetric(m.size, prometheus.GaugeValue, float64(value), q.Queue, state)
		}
		ch <- prometheus.MustNewConstMetric(m.latency, prometheus.GaugeValue, q.Latency.Seconds(), q.Queue)
	}
}
