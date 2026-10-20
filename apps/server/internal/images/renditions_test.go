package images

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/image/webp"
)

func TestWebPRenditionsRetainOriginalAndCrop(t *testing.T) {
	// Local synthetic fixture: red outer stripes and a blue middle prove crop geometry.
	source := image.NewNRGBA(image.Rect(0, 0, 640, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 640; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= 160 && x < 480 {
				c = color.NRGBA{B: 255, A: 255}
			}
			source.SetNRGBA(x, y, c)
		}
	}
	var input bytes.Buffer
	require.NoError(t, png.Encode(&input, source))
	variants, _, err := Process(input.Bytes(), "screenshot")
	require.NoError(t, err)
	files := variants[0].Files
	require.Len(t, files, 6)
	require.Equal(t, input.Bytes(), files[0].Data)
	require.Equal(t, "original", files[0].Role)
	for i, width := range []int{64, 128, 256, 318, 636} {
		file := files[i+1]
		require.Equal(t, width, file.Width)
		require.Equal(t, "image/webp", file.MIME)
		decoded, err := webp.Decode(bytes.NewReader(file.Data))
		require.NoError(t, err)
		require.Equal(t, image.Rect(0, 0, file.Width, file.Height), decoded.Bounds())
		if width >= 318 {
			require.Equal(t, width*200/318, file.Height)
			require.Equal(t, "thumbnail", file.Role)
			for _, x := range []int{0, width / 2, width - 1} {
				require.Equal(t, color.NRGBA{B: 255, A: 255}, color.NRGBAModel.Convert(decoded.At(x, file.Height/2)))
			}
		}
	}
	icon, _, err := Process(picture(t, 256, 256, color.NRGBA{G: 255, A: 128}), "icon")
	require.NoError(t, err)
	require.Len(t, icon[0].Files, 4)
	for _, f := range icon[0].Files[1:] {
		img, err := webp.Decode(bytes.NewReader(f.Data))
		require.NoError(t, err)
		_, _, _, alpha := img.At(0, 0).RGBA()
		require.EqualValues(t, 128*257, alpha)
	}
}

func TestScreenshotThumbnailsUpscaleSmallAndPortraitSources(t *testing.T) {
	for _, size := range []image.Point{{X: 80, Y: 50}, {X: 160, Y: 400}} {
		t.Run(size.String(), func(t *testing.T) {
			source := image.NewNRGBA(image.Rect(0, 0, size.X, size.Y))
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					c := color.NRGBA{B: 255, A: 255}
					// The portrait's red top/bottom must disappear at both densities.
					if size.Y == 400 && (y < 150 || y >= 250) {
						c = color.NRGBA{R: 255, A: 255}
					}
					source.SetNRGBA(x, y, c)
				}
			}
			var input bytes.Buffer
			require.NoError(t, png.Encode(&input, source))
			variants, _, err := Process(input.Bytes(), "screenshot")
			require.NoError(t, err)
			var thumbnails []Rendition
			for _, file := range variants[0].Files {
				if file.Role == "thumbnail" {
					thumbnails = append(thumbnails, file)
				}
			}
			require.Len(t, thumbnails, 2)
			for i, file := range thumbnails {
				require.Equal(t, 318*(i+1), file.Width)
				require.Equal(t, 200*(i+1), file.Height)
				decoded, err := webp.Decode(bytes.NewReader(file.Data))
				require.NoError(t, err)
				require.Equal(t, image.Rect(0, 0, file.Width, file.Height), decoded.Bounds())
				for _, y := range []int{0, file.Height / 2, file.Height - 1} {
					require.Equal(t, color.NRGBA{B: 255, A: 255}, color.NRGBAModel.Convert(decoded.At(file.Width/2, y)))
				}
			}
		})
	}
}
