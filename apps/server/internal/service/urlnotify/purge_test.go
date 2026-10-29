package urlnotify

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPurgeRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	require.Equal(t, now.Add(90*time.Second), purgeRetryAt("90", now, 0))
	require.Equal(t, now.Add(2*time.Minute), purgeRetryAt(now.Add(2*time.Minute).Format(http.TimeFormat), now, 0))
	for _, header := range []string{"2147483647", "Fri, 31 Dec 9999 23:59:59 GMT"} {
		require.Equal(t, now.Add(15*time.Minute), purgeRetryAt(header, now, 0))
		require.Equal(t, now.Add(45*time.Second), purgeRetryAt(header, now, 45*time.Second))
		require.Equal(t, now.Add(24*time.Hour), purgeRetryAt(header, now, 48*time.Hour))
	}
	for _, header := range []string{"", "invalid", "-1", "0", "9999999999999999999999", now.Add(-time.Minute).Format(http.TimeFormat)} {
		require.Equal(t, now.Add(time.Minute), purgeRetryAt(header, now, 0))
	}
}
