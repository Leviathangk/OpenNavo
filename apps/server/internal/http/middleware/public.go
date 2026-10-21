package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/redis/go-redis/v9"
)

// GCRA uses Redis time and atomic scripts so all API instances share one quota.
var rateScript = redis.NewScript(`local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000000+tonumber(clock[2])
local interval=tonumber(ARGV[1])
local burst=tonumber(ARGV[2])
local tat=tonumber(redis.call('GET',KEYS[1])) or now
local allowAt=tat-(burst-1)*interval
if now<allowAt then return {0,math.ceil((allowAt-now)/1000000),0} end
local next=math.max(tat,now)+interval
redis.call('SET',KEYS[1],string.format('%.0f',next),'PX',math.ceil((next-now)/1000))
return {1,0,math.max(0,burst-math.ceil((next-now)/interval))}`)

type RateLimiter struct{ Client *redis.Client }
type RateDecision struct {
	Allowed               bool
	RetryAfter, Remaining int
}

func (r RateLimiter) Allow(ctx context.Context, key string, count int, window time.Duration) (bool, int, error) {
	decision, err := r.Check(ctx, key, count, window)
	return decision.Allowed, decision.RetryAfter, err
}
func (r RateLimiter) Check(ctx context.Context, key string, count int, window time.Duration) (RateDecision, error) {
	if count <= 0 || window.Microseconds() < int64(count) {
		return RateDecision{}, errors.New("invalid rate limit configuration")
	}
	values, err := rateScript.Run(ctx, r.Client, []string{"rate:" + key}, window.Microseconds()/int64(count), count).Int64Slice()
	if err != nil {
		return RateDecision{}, err
	}
	if len(values) != 3 {
		return RateDecision{}, errors.New("invalid rate limit result")
	}
	return RateDecision{Allowed: values[0] == 1, RetryAfter: int(values[1]), Remaining: int(values[2])}, nil
}
func RateLimit(limiter RateLimiter, loggers ...*slog.Logger) gin.HandlerFunc {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return func(c *gin.Context) {
		count, window, scope := 300, time.Minute, "public"
		switch c.FullPath() {
		case "/api/v1/search":
			count, scope = 60, "search"
		case "/api/v1/search/suggest":
			count, scope = 120, "suggest"
		case "/api/v1/catalog/changes":
			count, scope = 60, "changes"
		case "/api/v1/packages/lookup":
			count, scope = 30, "lookup"
		case "/api/v1/usage/activity":
			count, scope = 60, "usage"
		case "/api/v1/feedback":
			count, window, scope = 5, time.Hour, "feedback"
		}
		key := scope + ":" + c.ClientIP()
		decision, err := limiter.Check(c.Request.Context(), key, count, window)
		if err != nil {
			logger.ErrorContext(c.Request.Context(), "public rate limiter unavailable", "requestId", c.GetString("requestId"), "route", c.FullPath(), "scope", scope, "error", err)
			c.Header("Cache-Control", "no-store")
			c.Header("Retry-After", "5")
			respond.Error(c, &domain.AppError{Code: domain.CodeInternal, HTTPStatus: http.StatusServiceUnavailable, Cause: err}, false)
			return
		}
		c.Header("X-RateLimit-Limit", strconv.Itoa(count))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(decision.Remaining))
		if !decision.Allowed {
			c.Header("Retry-After", strconv.Itoa(max(1, decision.RetryAfter)))
			respond.Error(c, &domain.AppError{Code: domain.CodeRateLimited, HTTPStatus: 429}, false)
			return
		}
		c.Next()
	}
}

type bufferedWriter struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (w *bufferedWriter) WriteHeader(status int) { w.status = status }
func (w *bufferedWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = 200
	}
}
func (w *bufferedWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	return w.body.Write(data)
}
func (w *bufferedWriter) WriteString(data string) (int, error) {
	w.WriteHeaderNow()
	return w.body.WriteString(data)
}
func (w *bufferedWriter) Status() int {
	if w.status == 0 {
		return 200
	}
	return w.status
}
func (w *bufferedWriter) Size() int     { return w.body.Len() }
func (w *bufferedWriter) Written() bool { return w.status != 0 }
func cachePolicy(path string) string {
	switch path {
	case "/api/v1/search", "/api/v1/search/suggest":
		return "public, max-age=30"
	case "/api/v1/catalog/snapshot", "/api/v1/catalog/changes":
		return "public, max-age=60"
	case "/api/v1/catalog/links":
		// The body carries its own etag. Compressing at the edge makes Caddy suffix the ETag header and Cloudflare weaken
		// it, so the header stops matching the body; `no-transform` keeps both layers from touching this response.
		return "public, max-age=60, s-maxage=300, stale-while-revalidate=600, no-transform"
	case "/api/v1/config/client":
		return "public, max-age=300"
	case "/api/v1/site/settings":
		return "public, max-age=60"
	case "/api/v1/site/indexnow-keys/:key":
		return "public, max-age=300"
	case "/api/v1/articles", "/api/v1/articles/:slug", "/api/v1/authors/:slug", "/api/v1/sitemap/articles":
		// Withdrawals must leave caches quickly.
		return "public, max-age=60"
	default:
		return "public, max-age=60, s-maxage=300, stale-while-revalidate=600"
	}
}
func appendVary(header http.Header, value string) {
	for _, item := range strings.Split(header.Get("Vary"), ",") {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return
		}
	}
	if previous := header.Get("Vary"); previous != "" {
		value = previous + ", " + value
	}
	header.Set("Vary", value)
}
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}
func PublicCacheHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Next()
			return
		}
		writer := &bufferedWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		c.Writer = writer.ResponseWriter
		status := writer.Status()
		if status == 200 {
			hash := sha256.Sum256(writer.body.Bytes())
			etag := c.Writer.Header().Get("ETag")
			if etag == "" {
				etag = `W/"` + hex.EncodeToString(hash[:16]) + `"`
			}
			c.Header("ETag", etag)
			policy := c.GetString("publicCachePolicy")
			if policy == "" {
				policy = cachePolicy(c.FullPath())
			}
			c.Header("Cache-Control", policy)
			appendVary(c.Writer.Header(), "Accept-Language")
			if etagMatches(c.GetHeader("If-None-Match"), etag) {
				c.Header("Content-Length", "")
				c.Writer.WriteHeader(http.StatusNotModified)
				return
			}
		} else {
			c.Header("Cache-Control", "no-store")
		}
		c.Writer.WriteHeader(status)
		_, _ = c.Writer.Write(writer.body.Bytes())
	}
}
