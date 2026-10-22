package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageCountryTrustAndUnknown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, peer, header, expected string
		trusted                      []string
	}{
		{"trusted edge", "192.0.2.10:4000", "US", "US", []string{"192.0.2.0/24"}},
		{"IPv6 ingress", "[2001:db8::1]:4000", "jp", "JP", []string{"2001:db8::1"}},
		{"forged direct", "198.51.100.1:4000", "US", "ZZ", []string{"192.0.2.0/24"}},
		{"no trusted ingress", "192.0.2.10:4000", "US", "ZZ", nil},
		{"missing", "192.0.2.10:4000", "", "ZZ", []string{"192.0.2.10"}},
		{"Cloudflare unknown", "192.0.2.10:4000", "XX", "ZZ", []string{"192.0.2.10"}},
		{"Tor", "192.0.2.10:4000", "T1", "ZZ", []string{"192.0.2.10"}},
		{"invalid", "192.0.2.10:4000", "US, CN", "ZZ", []string{"192.0.2.10"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/api/v1/usage/activity", nil)
			c.Request.RemoteAddr = tc.peer
			c.Request.Header.Set("CF-IPCountry", tc.header)
			c.Request.Header.Set("X-Forwarded-For", "192.0.2.10")
			UsageCountry(tc.trusted)(c)
			require.Equal(t, tc.expected, c.GetString("usageCountry"))
		})
	}
}
