package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMissingAssetRenditions(t *testing.T) {
	complete := []AssetFile{}
	for _, spec := range RequiredAssetRenditions("screenshot") {
		complete = append(complete, AssetFile{Role: spec.Role, Type: spec.Type, Width: spec.Width, Height: spec.Height})
	}
	for _, tc := range []struct {
		name, kind string
		files      []AssetFile
		widths     []int
	}{
		{"empty icon", "icon", nil, []int{64, 128, 256}},
		{"empty screenshot", "screenshot", nil, []int{64, 128, 256, 318, 636}},
		{"cover excluded", "cover", nil, []int{}},
		{"complete", "screenshot", complete, []int{}},
		{"thumbnail required", "screenshot", complete[:3], []int{318, 636}},
		{"partial", "screenshot", complete[1:4], []int{64, 636}},
		{"png is not webp", "icon", []AssetFile{{Role: "responsive", Type: "image/png", Width: 64}}, []int{64, 128, 256}},
		{"normalized is not responsive", "icon", []AssetFile{{Role: "normalized", Type: "image/webp", Width: 64}}, []int{64, 128, 256}},
		{"thumbnail dimensions", "screenshot", append(append([]AssetFile{}, complete[:4]...), AssetFile{Role: "thumbnail", Type: "image/webp", Width: 636, Height: 399}), []int{636}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			widths := []int{}
			for _, spec := range MissingAssetRenditions(tc.kind, tc.files) {
				widths = append(widths, spec.Width)
			}
			require.Equal(t, tc.widths, widths)
		})
	}
}
