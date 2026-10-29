package urlnotify

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const probeBudget = 10 * time.Second
const probeMaxAttempts = 3

func maxProbeTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

type probeBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *probeBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

func (n *Notifier) probePause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Retry-After is a lower bound. Long waits go back to the durable outbox instead of blocking a run.
func probeRetryDelay(header string, attempt int, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 32); err == nil && seconds >= 0 {
		return max(100*time.Millisecond, time.Duration(seconds)*time.Second)
	}
	if due, err := http.ParseTime(header); err == nil && due.After(now) {
		return due.Sub(now)
	}
	base := 500 * time.Millisecond * time.Duration(1<<min(attempt, 6))
	return base + time.Duration(rand.Int64N(int64(base))) // #nosec G404 -- Retry jitter is not a security decision.
}

func (n *Notifier) probeHTTP(ctx context.Context, rawURL string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, probeBudget)
	// The caller consumes the successful response body before its own request deadline. A separate
	// cancellation must travel with the body; otherwise returning here would cancel HTML parsing.
	deadline := n.Now().Add(probeBudget)
	interval := n.Config.IndexNowProbeInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if interval >= probeBudget {
		cancel()
		return nil, &probeWait{until: n.Now().Add(interval)}
	}
	pause := n.ProbeSleep
	if pause == nil {
		pause = n.probePause
		if n.Sleep != nil {
			pause = func(ctx context.Context, delay time.Duration) error { n.Sleep(delay); return ctx.Err() }
		}
	}
	if err := pause(ctx, interval); err != nil {
		cancel()
		return nil, err
	}
	client := *n.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for attempt := 0; attempt < probeMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("User-Agent", "OpenNavo-Probe/1.0 IndexNow")
		response, err := client.Do(req)
		if err != nil {
			cancel()
			return nil, errors.New("public page probe temporarily unavailable")
		}
		if response.StatusCode != http.StatusTooManyRequests && response.StatusCode != http.StatusServiceUnavailable {
			response.Body = &probeBody{ReadCloser: response.Body, cancel: cancel}
			return response, nil
		}
		delay := max(interval, probeRetryDelay(response.Header.Get("Retry-After"), attempt, n.Now()))
		_ = response.Body.Close()
		if attempt+1 == probeMaxAttempts || !n.Now().Add(delay).Before(deadline) || ctx.Err() != nil {
			cancel()
			return nil, &probeWait{until: n.Now().Add(delay)}
		}
		if err := pause(ctx, delay); err != nil {
			cancel()
			return nil, err
		}
	}
	panic("unreachable probe retry loop")
}
