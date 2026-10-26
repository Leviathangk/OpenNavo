package images

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"math"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Rendition struct {
	Width, Height      int
	Role, MIME, SHA256 string
	Data               []byte
}

type Variant struct {
	Width, Height int
	PNG           []byte
	SHA256        string
	Files         []Rendition
}

const MaxUploadBytes = 5 * 1024 * 1024

// Normalization emits at most 1280x8192 RGBA pixels. Allow the uncompressed
// pixels plus PNG overhead; a small JPEG upload can produce a much larger PNG.
const MaxNormalizedBytes = 48 * 1024 * 1024

func Process(data []byte, kind string) ([]Variant, *string, error) {
	return process(data, kind, MaxUploadBytes, false)
}

// ProcessNormalizedPNG is only for stored, previously normalized PNG objects.
// Uploads must continue to use Process. All dimension/decode limits still apply.
func ProcessNormalizedPNG(data []byte, kind string) ([]Variant, *string, error) {
	return process(data, kind, MaxNormalizedBytes, true)
}

func process(data []byte, kind string, maxBytes int, normalized bool) ([]Variant, *string, error) {
	if len(data) > maxBytes {
		return nil, nil, errors.New("image too large")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, errors.New("unsupported image")
	}
	if (normalized && format != "png") || (format != "png" && format != "jpeg" && format != "webp") {
		return nil, nil, errors.New("unsupported image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 32000000 {
		return nil, nil, errors.New("image dimensions exceed limits")
	}
	if kind == "icon" {
		ratio := float64(cfg.Width) / float64(cfg.Height)
		if cfg.Width < 128 || cfg.Height < 128 || ratio < 0.9 || ratio > 1.1 {
			return nil, nil, errors.New("invalid icon dimensions")
		}
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, errors.New("corrupt image")
	}
	sizes := []int{1280, 640}
	if kind == "icon" {
		sizes = []int{256}
		if min(cfg.Width, cfg.Height) >= 512 {
			sizes = append(sizes, 512)
		}
	}
	out := make([]Variant, 0, len(sizes))
	for _, width := range sizes {
		height := width
		if kind != "icon" {
			height = max(1, int(math.Round(float64(cfg.Height)*float64(width)/float64(cfg.Width))))
		}
		if height > 8192 || int64(width)*int64(height) > 32000000 {
			return nil, nil, errors.New("normalized image dimensions exceed limits")
		}
		target := image.NewNRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(target, target.Bounds(), source, source.Bounds(), draw.Src, nil)
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, target); err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(encoded.Bytes())
		out = append(out, Variant{Width: width, Height: height, PNG: encoded.Bytes(), SHA256: hex.EncodeToString(sum[:])})
	}
	// Retain the input exactly, including its original format and resolution.
	originalSum := sha256.Sum256(data)
	out[0].Files = []Rendition{{Width: cfg.Width, Height: cfg.Height, Role: "original", MIME: "image/" + format, SHA256: hex.EncodeToString(originalSum[:]), Data: data}}
	if kind == "icon" || kind == "screenshot" {
		for _, width := range []int{64, 128, 256} {
			height := width
			if kind == "screenshot" {
				height = max(1, int(math.Round(float64(cfg.Height)*float64(width)/float64(cfg.Width))))
			}
			file, err := webpRendition(source, source.Bounds(), width, height, "responsive")
			if err != nil {
				return nil, nil, err
			}
			out[0].Files = append(out[0].Files, file)
		}
		if kind == "screenshot" {
			crop := source.Bounds()
			if cfg.Width*200 > cfg.Height*318 {
				w := max(1, cfg.Height*318/200)
				crop.Min.X += (cfg.Width - w) / 2
				crop.Max.X = crop.Min.X + w
			} else {
				h := max(1, cfg.Width*200/318)
				crop.Min.Y += (cfg.Height - h) / 2
				crop.Max.Y = crop.Min.Y + h
			}
			// Use the same source rectangle at both densities, including for small
			// originals: srcset must never change the card's composition.
			for _, scale := range []int{1, 2} {
				file, err := webpRendition(source, crop, 318*scale, 200*scale, "thumbnail")
				if err != nil {
					return nil, nil, err
				}
				out[0].Files = append(out[0].Files, file)
			}
		}
	}
	return out, Accent(source), nil
}

func webpRendition(source image.Image, bounds image.Rectangle, width, height int, role string) (Rendition, error) {
	target := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Src, nil)
	var encoded bytes.Buffer
	if err := nativewebp.Encode(&encoded, target, nil); err != nil {
		return Rendition{}, err
	}
	sum := sha256.Sum256(encoded.Bytes())
	return Rendition{Width: width, Height: height, Role: role, MIME: "image/webp", SHA256: hex.EncodeToString(sum[:]), Data: encoded.Bytes()}, nil
}

type pixel struct{ r, g, b float64 }

func saturation(p pixel) float64 {
	maximum := max(p.r, p.g, p.b)
	if maximum == 0 {
		return 0
	}
	return (maximum - min(p.r, p.g, p.b)) / maximum
}
func distance(a, b pixel) float64 {
	r, g, blue := a.r-b.r, a.g-b.g, a.b-b.b
	return r*r + g*g + blue*blue
}
func Accent(source image.Image) *string {
	target := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	draw.CatmullRom.Scale(target, target.Bounds(), source, source.Bounds(), draw.Src, nil)
	pixels := []pixel{}
	for y := range 32 {
		for x := range 32 {
			c := color.NRGBAModel.Convert(target.At(x, y)).(color.NRGBA)
			p := pixel{float64(c.R), float64(c.G), float64(c.B)}
			if c.A >= 128 && saturation(p) >= 0.25 {
				pixels = append(pixels, p)
			}
		}
	}
	if len(pixels) == 0 {
		return nil
	}
	centers := []pixel{pixels[0]}
	for len(centers) < 3 {
		best, bestDistance := pixels[0], -1.0
		for _, p := range pixels {
			nearest := math.MaxFloat64
			for _, center := range centers {
				nearest = min(nearest, distance(p, center))
			}
			if nearest > bestDistance {
				best, bestDistance = p, nearest
			}
		}
		centers = append(centers, best)
	}
	counts := make([]int, 3)
	for range 12 {
		sums := make([]pixel, 3)
		counts = make([]int, 3)
		for _, p := range pixels {
			cluster, nearest := 0, math.MaxFloat64
			for i, center := range centers {
				if d := distance(p, center); d < nearest {
					cluster, nearest = i, d
				}
			}
			counts[cluster]++
			sums[cluster].r += p.r
			sums[cluster].g += p.g
			sums[cluster].b += p.b
		}
		for i, count := range counts {
			if count > 0 {
				n := float64(count)
				centers[i] = pixel{sums[i].r / n, sums[i].g / n, sums[i].b / n}
			}
		}
	}
	best, weight := centers[0], -1.0
	for i, center := range centers {
		score := float64(counts[i]) * saturation(center)
		if score > weight {
			best, weight = center, score
		}
	}
	value := fmt.Sprintf("#%02X%02X%02X", int(math.Round(best.r)), int(math.Round(best.g)), int(math.Round(best.b)))
	return &value
}
