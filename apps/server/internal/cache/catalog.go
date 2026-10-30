package cache

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Backend interface {
	Load(context.Context, string) ([]byte, error)
	Save(context.Context, string, []byte, time.Duration) error
}

func ReadThrough[T any](ctx context.Context, backend Backend, key string, ttl time.Duration, load func(context.Context) (T, error)) (T, error) {
	backend = Scope(ctx, backend)
	var value T
	if backend != nil {
		if data, err := backend.Load(ctx, key); err == nil && json.Unmarshal(data, &value) == nil {
			return value, nil
		}
	}
	value, err := load(ctx)
	if err != nil {
		return value, err
	}
	if backend != nil {
		if data, encodeErr := json.Marshal(value); encodeErr == nil {
			_ = backend.Save(ctx, key, data, ttl)
		}
	}
	return value, nil
}

// ReadThroughCompressed is ReadThrough for values above a backend's entry limit (Redis keeps at most 512 KiB):
// entries hold gzip-compressed JSON, which shrinks repetitive payloads such as sitemap pages by over 10×.
func ReadThroughCompressed[T any](ctx context.Context, backend Backend, key string, ttl time.Duration, load func(context.Context) (T, error)) (T, error) {
	backend = Scope(ctx, backend)
	if backend != nil {
		if data, err := backend.Load(ctx, key); err == nil {
			if reader, err := gzip.NewReader(bytes.NewReader(data)); err == nil {
				var cached T
				if json.NewDecoder(reader).Decode(&cached) == nil {
					return cached, nil
				}
			}
		}
	}
	value, err := load(ctx)
	if err != nil {
		return value, err
	}
	if backend != nil {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if json.NewEncoder(writer).Encode(value) == nil && writer.Close() == nil {
			_ = backend.Save(ctx, key, buffer.Bytes(), ttl)
		}
	}
	return value, nil
}
func PublishInvalidation(ctx context.Context, client *redis.Client, patterns ...string) error {
	for _, pattern := range patterns {
		if !validPattern(pattern) {
			return errors.New("invalid cache pattern")
		}
	}
	data, err := json.Marshal(struct {
		Patterns []string `json:"patterns"`
	}{patterns})
	if err != nil {
		return err
	}
	return client.Publish(ctx, invalidationChannel(client), data).Err()
}

func invalidationChannel(client *redis.Client) string {
	// Redis pub/sub ignores the SELECT database number; non-default databases need separate channels.
	if client.Options().DB == 0 {
		return "cache:invalidate"
	}
	return fmt.Sprintf("cache:invalidate:db:%d", client.Options().DB)
}
func validPattern(pattern string) bool {
	return strings.HasPrefix(pattern, "c:") && len(pattern) > 2 && len(pattern) <= 256 && !strings.ContainsAny(pattern, "\r\n")
}

// PatternLiteral escapes Redis glob metacharacters so a key segment matches only itself.
func PatternLiteral(value string) string {
	var out strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`*?[]\`, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}
func Invalidate(ctx context.Context, client *redis.Client, patterns []string) error {
	for _, pattern := range patterns {
		if !validPattern(pattern) {
			return errors.New("invalid cache pattern")
		}
		var cursor uint64
		for {
			// Large batches keep a full pass over a busy keyspace to a few round trips.
			keys, next, err := client.Scan(ctx, cursor, pattern, 1000).Result()
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := client.Unlink(ctx, keys...).Err(); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return nil
}

// invalidationInterval bounds how often invalidation bursts rescan Redis: bulk writes
// otherwise publish several times per second and keep every cache permanently cold.
var invalidationInterval = 10 * time.Second

// coalescer merges invalidation bursts. The first batch after a quiet interval runs at
// once; later ones wait until the interval since the previous pass has elapsed.
type coalescer struct {
	interval time.Duration
	last     time.Time
	pending  map[string]struct{}
}

// add records valid patterns and reports when the pending batch is due.
func (c *coalescer) add(now time.Time, patterns []string) (time.Time, bool) {
	for _, pattern := range patterns {
		if !validPattern(pattern) {
			continue
		}
		if c.pending == nil {
			c.pending = map[string]struct{}{}
		}
		c.pending[pattern] = struct{}{}
	}
	if len(c.pending) == 0 {
		return time.Time{}, false
	}
	return later(now, c.last.Add(c.interval)), true
}

// take returns the pending batch; c:* supersedes every narrower pattern.
func (c *coalescer) take(now time.Time) []string {
	if len(c.pending) == 0 {
		return nil
	}
	patterns := []string{"c:*"}
	if _, all := c.pending["c:*"]; !all {
		patterns = slices.Sorted(maps.Keys(c.pending))
	}
	c.pending, c.last = nil, now
	return patterns
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func Subscribe(ctx context.Context, client *redis.Client, logger *slog.Logger, targets ...*redis.Client) (func(), error) {
	child, cancel := context.WithCancel(ctx)
	sub := client.Subscribe(child, invalidationChannel(client))
	if _, err := sub.Receive(child); err != nil {
		cancel()
		_ = sub.Close()
		return nil, err
	}
	// Keep business-local authorization caches invalidated too. Publishers
	// stay on the business connection; disposable cache storage may differ.
	clients := []*redis.Client{}
	for _, target := range append([]*redis.Client{client}, targets...) {
		if target != nil && !slices.Contains(clients, target) {
			clients = append(clients, target)
		}
	}
	apply := func(ctx context.Context, patterns []string) {
		for _, target := range clients {
			if err := Invalidate(ctx, target, patterns); err != nil && ctx.Err() == nil {
				logger.WarnContext(ctx, "cache invalidation failed")
			}
		}
	}
	received := make(chan []string, 64)
	go func() {
		defer close(received)
		for {
			message, err := sub.ReceiveMessage(child)
			if err != nil {
				if child.Err() != nil {
					return
				}
				logger.WarnContext(child, "cache subscription unavailable")
				continue
			}
			var payload struct {
				Patterns []string `json:"patterns"`
			}
			if json.Unmarshal([]byte(message.Payload), &payload) != nil {
				continue
			}
			select {
			case received <- payload.Patterns:
			case <-child.Done():
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		batch := coalescer{interval: invalidationInterval}
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		for {
			select {
			case patterns, open := <-received:
				if !open {
					// Apply a pending burst on shutdown so a restart cannot strand stale entries.
					if pending := batch.take(time.Now()); len(pending) > 0 {
						flush, stop := context.WithTimeout(context.WithoutCancel(child), 3*time.Second)
						apply(flush, pending)
						stop()
					}
					return
				}
				if due, ok := batch.add(time.Now(), patterns); ok {
					timer.Reset(time.Until(due))
				}
			case <-timer.C:
				apply(child, batch.take(time.Now()))
			}
		}
	}()
	return func() { cancel(); _ = sub.Close(); <-done }, nil
}
