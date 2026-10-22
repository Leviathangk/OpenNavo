package middleware

import (
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"
)

// UsageCountry accepts edge geolocation only from explicitly configured ingress
// peers. The ingress must overwrite CF-IPCountry and reject direct public traffic.
// Never infer location from language, accept a client-provided country, or store IPs.
func UsageCountry(trusted []string) gin.HandlerFunc {
	prefixes := make([]netip.Prefix, 0, len(trusted))
	for _, raw := range trusted {
		if ip, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()))
		} else if prefix, err := netip.ParsePrefix(strings.TrimSpace(raw)); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return func(c *gin.Context) {
		c.Set("usageCountry", "ZZ")
		peer, err := netip.ParseAddr(c.RemoteIP())
		if err != nil {
			return
		}
		for _, prefix := range prefixes {
			if !prefix.Contains(peer.Unmap()) {
				continue
			}
			country := strings.ToUpper(strings.TrimSpace(c.GetHeader("CF-IPCountry")))
			region, err := language.ParseRegion(country)
			if err == nil && len(country) == 2 && region.IsCountry() && country != "ZZ" {
				c.Set("usageCountry", country)
			}
			return
		}
	}
}
