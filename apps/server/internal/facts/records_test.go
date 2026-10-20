package facts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPriceOffersRequireUsageEvidenceAndExpire(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	amount, currency := "0", "USD"
	p := PriceFact{PriceObservation: PriceObservation{Region: "US", Edition: "App Store", Period: "unknown", Usage: "unknown", DownloadFree: true, Amount: &amount, Currency: &currency, PurchaseURL: "https://apps.apple.com/us/app/example/id1", Channel: "app_store"}, Evidence: NewProof("https://apps.apple.com/us/app/example/id1", now, time.Hour, "verified", now)}
	require.NoError(t, p.Validate())
	require.False(t, p.OfferReady(now), "free download must not assert free usage")
	p.Period, p.Usage = "free", "free"
	require.True(t, p.OfferReady(now))
	require.False(t, p.OfferReady(now.Add(time.Hour)))
	p.Evidence.Status = "needs_review"
	require.False(t, p.OfferReady(now))
	p.Usage = "paid"
	p.Period = "monthly"
	require.ErrorIs(t, p.Validate(), ErrInvalid)
}

func TestRecordRejectsUnknownFieldsAndFutureEvidence(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	r := Record{Kind: "price", Key: "us-pro", Value: json.RawMessage(`{"region":"US","edition":"Pro","period":"one_time","usage":"paid","downloadFree":false,"amount":"19.99","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor","invented":true}`), Evidence: NewProof("https://vendor.example/pricing", now, time.Hour, "verified", now)}
	require.ErrorIs(t, ValidateRecord(r, now), ErrInvalid)
	r.Value = json.RawMessage(`{"region":"US","edition":"Pro","period":"one_time","usage":"paid","downloadFree":false,"amount":"19.99","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor"}`)
	require.NoError(t, ValidateRecord(r, now))
	future := now.Add(time.Hour)
	r.Evidence.CheckedAt = &future
	require.ErrorIs(t, ValidateRecord(r, now), ErrInvalid)
}
