package management

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestHermesPackageWriteRejections(t *testing.T) {
	svc := &Service{Now: func() time.Time { return time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC) }}
	for _, tc := range []struct {
		op     string
		body   map[string]any
		reason string
	}{
		{"UpdatePackageMeta", map[string]any{"downloadSize": int64(-1)}, "download_size_invalid:downloadSize"},
		{"UpdatePackageMeta", map[string]any{"accentColor": "private-value"}, "accent_color_invalid:accentColor"},
		{"SetPackagePrice", map[string]any{"type": "private-value"}, "price_invalid"},
		{"SetPackageMacosSupport", map[string]any{"ranges": []any{map[string]any{"arch": "private-value"}}}, "macos_support_invalid"},
		{"SetPackageAlternatives", map[string]any{}, "alternative_items_invalid:items"},
		{"SetPackageAlternatives", map[string]any{"items": []any{map[string]any{"packageId": float64(42)}}}, "alternatives_invalid"},
	} {
		t.Run(tc.op+"/"+tc.reason, func(t *testing.T) {
			in := Input{ID: 42, Body: tc.body}
			var err error
			if tc.op == "UpdatePackageMeta" {
				_, err = svc.packageWrite(context.Background(), tc.op, in)
			} else {
				_, err = svc.packageFacts(context.Background(), tc.op, in)
			}
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, "rejected: "+tc.reason, app.Msg)
			require.NotContains(t, app.Msg, "private-value")
		})
	}
}

func TestFactRejectionKeepsSpecificReason(t *testing.T) {
	err := domain.Rejected("unknown_field")
	require.Same(t, err, validationError(err, "price_input_invalid"))
}
