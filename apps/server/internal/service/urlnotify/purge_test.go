package urlnotify

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPurgeRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	require.Equal(t, now.Add(90*time.Second), purgeRetryAt("90", now))
	require.Equal(t, now.Add(2*time.Minute), purgeRetryAt(now.Add(2*time.Minute).Format(http.TimeFormat), now))
	for _, header := range []string{"", "invalid", "-1", "0", "9999999999999999999999", now.Add(-time.Minute).Format(http.TimeFormat)} {
		require.Equal(t, now.Add(time.Minute), purgeRetryAt(header, now))
	}
}
