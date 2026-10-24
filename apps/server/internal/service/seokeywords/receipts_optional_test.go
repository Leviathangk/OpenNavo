package seokeywords

import (
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestOpenSEOOptionalReceiptValidation(t *testing.T) {
	ptr := func(s string) *string { return &s }
	for _, tc := range []struct {
		name       string
		task, cost *string
		reason     string
	}{
		{"absent", nil, nil, ""}, {"provided", ptr("fixture-task"), ptr("0.0500"), ""},
		{"zero_cost", ptr("fixture-free"), ptr("0.0000"), ""},
		{"task_only", ptr("fixture-task"), nil, "vendor_receipt_required"},
		{"cost_only", nil, ptr("0.1"), "vendor_receipt_required"},
		{"empty_task", ptr(" "), ptr("0.1"), "vendor_receipt_required"},
		{"long_task", ptr(strings.Repeat("x", 201)), ptr("0.1"), "vendor_receipt_required"},
		{"bad_cost", ptr("fixture-task"), ptr("-1"), "volume_or_cost_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			volume := int64(100)
			o := Observation{Keyword: "Fixture mac utility", Source: "openseo:dataforseo", Engine: "google", Country: "US", Language: "en-US", Device: "desktop", Period: "2026-10", VolumeState: "known", SearchVolume: &volume, ObservedAt: now, VendorTaskID: tc.task, CostUSD: tc.cost}
			err := validate(&o, now)
			if tc.reason == "" {
				require.NoError(t, err)
			} else {
				var app *domain.AppError
				require.ErrorAs(t, err, &app)
				require.Equal(t, "rejected: "+tc.reason, app.Msg)
			}
		})
	}
}
