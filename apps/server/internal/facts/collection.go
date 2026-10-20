package facts

import (
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
)

var clientTokenPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+._@-]{0,127}$`)

func ClientCaskToken(kind, token string) bool {
	return kind == "cask" && clientTokenPattern.MatchString(token)
}
func Brewfile(tokens []string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, token := range tokens {
		if ClientCaskToken("cask", token) && !seen[token] {
			seen[token] = true
			b.WriteString("cask \"")
			b.WriteString(token)
			b.WriteString("\"\n")
		}
	}
	return b.String()
}

type PriceTotal struct {
	Region      string `json:"region"`
	Currency    string `json:"currency"`
	Period      string `json:"period"`
	Amount      string `json:"amount"`
	CoveredApps int    `json:"coveredApps"`
}

// PriceTotals never selects between competing editions or mixes currencies and billing periods.
func PriceTotals(packages [][]PriceFact, now time.Time) ([]PriceTotal, int) {
	type group struct {
		PriceTotal
		sum *big.Rat
	}
	groups := map[string]*group{}
	unknown := 0
	for _, prices := range packages {
		candidates := map[string][]PriceFact{}
		for _, p := range prices {
			if !p.OfferReady(now) {
				continue
			}
			key := p.Region + "/" + *p.Currency + "/" + p.Period
			candidates[key] = append(candidates[key], p)
		}
		covered := false
		for key, items := range candidates {
			if len(items) != 1 {
				continue
			}
			p := items[0]
			amount, ok := new(big.Rat).SetString(*p.Amount)
			if !ok {
				continue
			}
			g := groups[key]
			if g == nil {
				g = &group{PriceTotal: PriceTotal{Region: p.Region, Currency: *p.Currency, Period: p.Period}, sum: new(big.Rat)}
				groups[key] = g
			}
			g.sum.Add(g.sum, amount)
			g.CoveredApps++
			covered = true
		}
		if !covered {
			unknown++
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []PriceTotal{}
	for _, key := range keys {
		g := groups[key]
		g.Amount = strings.TrimRight(strings.TrimRight(g.sum.FloatString(6), "0"), ".")
		out = append(out, g.PriceTotal)
	}
	return out, unknown
}
