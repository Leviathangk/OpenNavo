package facts

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLocalizedFactLimitsCountUnicodeCharacters(t *testing.T) {
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	proof := NewProof("https://vendor.example/docs", now, time.Hour, "verified", now)
	for _, tc := range []struct {
		name  string
		limit int
		check func(string) error
	}{
		{"background", 10000, func(text string) error {
			return (BackgroundContent{Locale: "zh-CN", Description: text, ItemType: "unknown", Evidence: proof}).Validate(now)
		}},
		{"issue title", 200, func(text string) error {
			return (Issue{Locale: "zh-CN", Title: text, Symptom: "Symptom", AppVersions: []string{"1.0"}, MacOSVersions: []string{"15"}, Steps: []string{"Retry"}}).Validate()
		}},
		{"issue steps", 4000, func(text string) error {
			return (Issue{Locale: "zh-CN", Title: "Issue", Symptom: "Symptom", AppVersions: []string{"1.0"}, MacOSVersions: []string{"15"}, Steps: []string{text}}).Validate()
		}},
		{"migration steps", 4000, func(text string) error {
			return (Migration{TargetPackageID: 1, Locale: "zh-CN", Steps: []MigrationStep{{Action: "backup", Text: text, Evidence: proof}}}).Validate(now)
		}},
		{"edition", 100, func(text string) error {
			return (PriceObservation{Region: "CN", Edition: text, Period: "free", Usage: "free", DownloadFree: true, PurchaseURL: "https://vendor.example/buy", Channel: "vendor"}).Validate()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.check(strings.Repeat("字", tc.limit)))
			require.Error(t, tc.check(strings.Repeat("字", tc.limit+1)))
		})
	}
}
