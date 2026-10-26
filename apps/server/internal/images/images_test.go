package images

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func picture(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
func TestNormalizationAndDominantColor(t *testing.T) {
	data := picture(t, 512, 512, color.NRGBA{R: 240, G: 60, B: 20, A: 255})
	out, accent, err := Process(data, "icon")
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, "#F03C14", *accent)
	require.Equal(t, 256, out[0].Width)
	require.Equal(t, 512, out[1].Width)
	require.Len(t, out[0].SHA256, 64)
	again, c, err := Process(data, "icon")
	require.NoError(t, err)
	require.Equal(t, out, again)
	require.Equal(t, accent, c)
	out, _, err = Process(picture(t, 200, 100, color.NRGBA{A: 255}), "screenshot")
	require.NoError(t, err)
	require.Equal(t, 640, out[0].Height)
	require.Equal(t, 320, out[1].Height)
	_, accent, err = Process(picture(t, 128, 128, color.NRGBA{R: 90, G: 90, B: 90, A: 255}), "icon")
	require.NoError(t, err)
	require.Nil(t, accent)
	_, accent, err = Process(picture(t, 128, 128, color.NRGBA{R: 255, A: 50}), "icon")
	require.NoError(t, err)
	require.Nil(t, accent)
}
func TestRejectInvalidOrUnsafeImages(t *testing.T) {
	for _, data := range [][]byte{[]byte(`<svg/>`), make([]byte, 5*1024*1024+1), picture(t, 127, 127, color.NRGBA{}), picture(t, 128, 200, color.NRGBA{})} {
		_, _, err := Process(data, "icon")
		require.Error(t, err)
	}
	_, _, err := Process(picture(t, 1, 8192, color.NRGBA{}), "screenshot")
	require.Error(t, err)
}

func TestNormalizedPNGStillEnforcesLimits(t *testing.T) {
	_, _, err := ProcessNormalizedPNG(make([]byte, MaxNormalizedBytes+1), "screenshot")
	require.EqualError(t, err, "image too large")
	_, _, err = ProcessNormalizedPNG(picture(t, 1, 8193, color.NRGBA{}), "screenshot")
	require.EqualError(t, err, "image dimensions exceed limits")
	_, _, err = ProcessNormalizedPNG(picture(t, 1, 8192, color.NRGBA{}), "screenshot")
	require.EqualError(t, err, "normalized image dimensions exceed limits")
	var jpegData bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegData, image.NewNRGBA(image.Rect(0, 0, 128, 128)), nil))
	_, _, err = ProcessNormalizedPNG(jpegData.Bytes(), "screenshot")
	require.EqualError(t, err, "unsupported image")
}
