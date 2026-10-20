package facts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBrewfileContainsOnlySupportedCaskTokens(t *testing.T) {
	require.Equal(t, "cask \"example\"\ncask \"example@2\"\n", Brewfile([]string{"example", "example", "example@2", "x\"; system('echo')", "owner/tap/app"}))
	require.False(t, ClientCaskToken("link", "example"))
	require.False(t, ClientCaskToken("formula", "example"))
}
func TestCollectionPriceTotalsNeverChooseEditionsOrMixCurrencies(t *testing.T) {
	now := time.Now()
	proof := NewProof("https://vendor.example/pricing", now, time.Hour, "verified", now)
	price := func(amount, currency, period string) PriceFact {
		usage := "paid"
		if period != "one_time" {
			usage = "subscription"
		}
		return PriceFact{PriceObservation: PriceObservation{Region: "US", Edition: "Pro", Usage: usage, Period: period, Amount: &amount, Currency: &currency, PurchaseURL: "https://vendor.example/buy", Channel: "vendor"}, Evidence: proof}
	}
	totals, unknown := PriceTotals([][]PriceFact{{price("0.1", "USD", "one_time")}, {price("0.2", "USD", "one_time")}, {price("10", "EUR", "one_time")}, {price("3", "USD", "monthly")}, {price("5", "USD", "one_time"), price("9", "USD", "one_time")}, {}}, now)
	require.Len(t, totals, 3)
	require.Equal(t, 2, unknown)
	for _, v := range totals {
		if v.Currency == "USD" && v.Period == "one_time" {
			require.Equal(t, "0.3", v.Amount)
			require.Equal(t, 2, v.CoveredApps)
		}
	}
	totals, unknown = PriceTotals([][]PriceFact{{price("9", "USD", "one_time")}}, now.Add(time.Hour))
	require.Empty(t, totals)
	require.Equal(t, 1, unknown)
}
