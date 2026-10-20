package observability

import (
	"context"
	"sync"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

type BusinessMetrics struct {
	Store  *store.Store
	mu     sync.Mutex
	loaded time.Time
	state  store.MetricState
	err    error
	desc   map[string]*prometheus.Desc
}

func NewBusinessMetrics(st *store.Store) *BusinessMetrics {
	m := &BusinessMetrics{Store: st, desc: map[string]*prometheus.Desc{}}
	for name, labels := range map[string][]string{
		"content_translation_requests_total": {"entity", "status"}, "content_translation_tokens_total": {"entity", "status"},
		"sync_runs_total": {"job", "status"}, "catalog_packages": {"kind"}, "changelog_fetch_total": {"source", "status"}, "github_ratelimit_remaining": {},
		"catalog_last_success_timestamp_seconds": {}, "opennavo_metrics_collection_success": {},
	} {
		m.desc[name] = prometheus.NewDesc(name, "OpenNavo "+name, labels, nil)
	}
	return m
}
func (m *BusinessMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.desc {
		ch <- d
	}
}
func (m *BusinessMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if now.Sub(m.loaded) > 15*time.Second {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		m.state, m.err = m.Store.MetricState(ctx, now)
		cancel()
		m.loaded = now
	}
	emit := func(name string, kind prometheus.ValueType, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(m.desc[name], kind, value, labels...)
	}
	if m.err != nil {
		emit("opennavo_metrics_collection_success", prometheus.GaugeValue, 0)
		return
	}
	emit("opennavo_metrics_collection_success", prometheus.GaugeValue, 1)
	for _, row := range m.state.Translations {
		emit("content_translation_requests_total", prometheus.CounterValue, float64(row.Requests), row.Entity, row.Status)
		emit("content_translation_tokens_total", prometheus.CounterValue, float64(row.Tokens), row.Entity, row.Status)
	}
	jobs := map[string]int64{}
	fetches := map[string]int64{}
	lastSuccess := float64(0)
	for _, r := range m.state.Jobs {
		jobs[r.JobType+"\x00"+r.Status] += r.Runs
		if r.JobType == "changelog:fetch" {
			fetches[r.Source+"\x00"+r.Status] += r.Runs
		}
		if r.JobType == "catalog:sync" && r.Status == "succeeded" {
			lastSuccess = max(lastSuccess, float64(r.LastFinishedAt.Unix()))
		}
	}
	if len(jobs) == 0 {
		jobs["catalog:sync\x00succeeded"] = 0
	}
	if len(fetches) == 0 {
		fetches["unknown\x00succeeded"] = 0
	}
	for key, value := range jobs {
		a, b := metricLabels(key)
		emit("sync_runs_total", prometheus.CounterValue, float64(value), a, b)
	}
	for key, value := range fetches {
		a, b := metricLabels(key)
		emit("changelog_fetch_total", prometheus.CounterValue, float64(value), a, b)
	}
	packages := map[string]int64{"cask": 0, "formula": 0}
	for _, r := range m.state.Packages {
		packages[r.Kind] = r.Count
	}
	for kind, value := range packages {
		emit("catalog_packages", prometheus.GaugeValue, float64(value), kind)
	}
	emit("github_ratelimit_remaining", prometheus.GaugeValue, float64(m.state.GitHubRemaining))
	emit("catalog_last_success_timestamp_seconds", prometheus.GaugeValue, lastSuccess)

}
func metricLabels(key string) (string, string) {
	for index, c := range key {
		if c == 0 {
			return key[:index], key[index+1:]
		}
	}
	return key, ""
}
