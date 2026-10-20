package sourcefacts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/opennavo/opennavo/server/internal/facts"
)

type NVD struct {
	Client    *http.Client
	Base      string
	UserAgent string
}

var cveIDPattern = regexp.MustCompile(`^CVE-[0-9]{4}-[0-9]{4,}$`)

// ExactMatches deliberately omits complex applicability expressions instead of treating a
// product name, wildcard version, or a single operand of AND as proof of vulnerability.
func (n *NVD) ExactMatches(ctx context.Context, cpe string) ([]facts.Vulnerability, error) {
	version, ok := facts.ExactCPEVersion(cpe)
	if !ok {
		return nil, facts.ErrInvalid
	}
	base := n.Base
	if base == "" {
		base = "https://services.nvd.nist.gov/rest/json/cves/2.0"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+url.Values{"cpeName": {cpe}, "isVulnerable": {""}, "resultsPerPage": {"2000"}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", n.UserAgent)
	req.Header.Set("Accept", "application/json")
	response, err := n.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("NVD unavailable: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == 429 || response.StatusCode == 403 {
		return nil, ErrRateLimited
	}
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("NVD status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 8<<20 {
		return nil, fmt.Errorf("NVD response too large")
	}
	var body struct {
		TotalResults    int `json:"totalResults"`
		Vulnerabilities []struct {
			CVE struct {
				ID             string `json:"id"`
				Status         string `json:"vulnStatus"`
				Configurations []struct {
					Operator string `json:"operator"`
					Negate   bool   `json:"negate"`
					Nodes    []struct {
						Operator string            `json:"operator"`
						Negate   bool              `json:"negate"`
						Children []json.RawMessage `json:"children"`
						Matches  []struct {
							Vulnerable     bool   `json:"vulnerable"`
							Criteria       string `json:"criteria"`
							StartIncluding string `json:"versionStartIncluding"`
							StartExcluding string `json:"versionStartExcluding"`
							EndIncluding   string `json:"versionEndIncluding"`
							EndExcluding   string `json:"versionEndExcluding"`
						} `json:"cpeMatch"`
					} `json:"nodes"`
				} `json:"configurations"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if body.TotalResults > len(body.Vulnerabilities) {
		return nil, fmt.Errorf("NVD response truncated")
	}
	out := []facts.Vulnerability{}
	seen := map[string]bool{}
	for _, item := range body.Vulnerabilities {
		c := item.CVE
		if c.Status == "Rejected" || !cveIDPattern.MatchString(c.ID) {
			continue
		}
		matched := false
		for _, config := range c.Configurations {
			if config.Negate || config.Operator != "" && config.Operator != "OR" {
				continue
			}
			for _, node := range config.Nodes {
				if node.Negate || node.Operator != "OR" || len(node.Children) > 0 {
					continue
				}
				for _, m := range node.Matches {
					if m.Vulnerable && m.Criteria == cpe && m.StartIncluding == "" && m.StartExcluding == "" && m.EndIncluding == "" && m.EndExcluding == "" {
						matched = true
					}
				}
			}
		}
		if matched && !seen[c.ID] {
			seen[c.ID] = true
			out = append(out, facts.Vulnerability{ID: c.ID, ProductCPE: cpe, AppVersion: version, SourceURL: "https://nvd.nist.gov/vuln/detail/" + c.ID})
		}
	}
	return out, nil
}
