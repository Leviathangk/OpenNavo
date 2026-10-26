package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"sync"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/opennavo/opennavo/server/internal/images"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestBackfillSourcePreference(t *testing.T) {
	for _, tc := range []struct {
		name, files, primary, want string
	}{
		{"original preferred", `[{"role":"normalized","storageKey":"small.png"},{"role":"original","storageKey":"source.jpg"}]`, "primary.png", "source.jpg"},
		{"legacy", `[]`, "primary.png", "primary.png"},
		{"normalized", `[{"role":"normalized","storageKey":"normal.png"}]`, "", "normal.png"},
		{"missing", `[]`, "", ""},
		{"malformed", `{}`, "primary.png", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := backfillSource(store.Asset{Files: json.RawMessage(tc.files), StorageKey: tc.primary})
			require.Equal(t, tc.want, key)
			require.Equal(t, tc.want == "", err != nil)
		})
	}
}

func TestRenditionFileContentAddress(t *testing.T) {
	objects := newBackfillObjects()
	file := renditionFile(objects, "icons/uploads/source/", "0123456789abcdef", images.Rendition{Role: "responsive", MIME: "image/webp", Width: 64, Height: 64, SHA256: "abcdef0123456789", Data: []byte("webp")})
	require.Equal(t, "icons/uploads/source/0123456789ab-responsive-abcdef012345-64.webp", file.StorageKey)
	require.Equal(t, objects.URL(file.StorageKey), file.URL)
	require.EqualValues(t, 4, file.Bytes)
	require.Equal(t, "abcdef0123456789", file.SHA256)
}

func TestBackfillLargeNormalizedScreenshot(t *testing.T) {
	// A valid upload can expand past the upload limit after PNG normalization.
	source := image.NewNRGBA(image.Rect(0, 0, 1280, 1920))
	// Deterministic noise keeps the PNG large without a random generator.
	noise := sha256.Sum256([]byte("backfill screenshot fixture"))
	for i := 0; i < len(source.Pix); i += 4 {
		if i%32 == 0 {
			noise = sha256.Sum256(noise[:])
		}
		j := i % 32
		source.Pix[i], source.Pix[i+1], source.Pix[i+2], source.Pix[i+3] = noise[j], noise[j+1], noise[j+2], 255
	}
	var uploaded, normalized bytes.Buffer
	require.NoError(t, jpeg.Encode(&uploaded, source, &jpeg.Options{Quality: 75}))
	require.Less(t, uploaded.Len(), 5*1024*1024)
	decoded, err := jpeg.Decode(bytes.NewReader(uploaded.Bytes()))
	require.NoError(t, err)
	require.NoError(t, png.Encode(&normalized, decoded))
	require.Greater(t, normalized.Len(), 5*1024*1024)
	objects := newBackfillObjects()
	asset := store.Asset{Kind: "screenshot", StorageKey: "screenshots/legacy/source-1280.png", Files: json.RawMessage(`[]`)}
	objects.files[asset.StorageKey] = normalized.Bytes()
	files, err := prepareRenditions(context.Background(), objects, asset, store.RequiredAssetRenditions(asset.Kind))
	require.NoError(t, err)
	require.Empty(t, store.MissingAssetRenditions(asset.Kind, files))
	_, _, err = processImage(context.Background(), normalized.Bytes(), "screenshot")
	require.EqualError(t, err, "image too large", "ordinary uploads retain their limit")
	asset.Files = json.RawMessage(`[{"role":"original","storageKey":"screenshots/legacy/source-1280.png"}]`)
	_, err = prepareRenditions(context.Background(), objects, asset, store.RequiredAssetRenditions(asset.Kind))
	require.EqualError(t, err, "read_source: image too large", "recorded originals retain the upload limit")
}

func TestBackfillFailureReasonRedactsTransportDetails(t *testing.T) {
	secret := "signed-request-fixture-not-for-logs"
	require.Equal(t, "read_source:operation_failed", BackfillFailureReason(&backfillError{stage: "read_source", err: errors.New(secret)}))
	require.Equal(t, "upload_rendition:storage_access_denied", BackfillFailureReason(&backfillError{stage: "upload_rendition", err: minio.ErrorResponse{Code: "AccessDenied", Message: secret}}))
	require.Equal(t, "process_image:unsupported image", BackfillFailureReason(&backfillError{stage: "process_image", err: errors.New("unsupported image")}))
	require.Equal(t, "manifest_transaction:canceled", BackfillFailureReason(context.Canceled))
}

// This fake models atomic object creation and counts actual writes, so restart
// tests distinguish reusing an object from overwriting identical bytes.
type backfillMemoryObjects struct {
	mu      sync.Mutex
	files   map[string][]byte
	writes  map[string]int
	reads   []string
	failKey string
}

func newBackfillObjects() *backfillMemoryObjects {
	return &backfillMemoryObjects{files: map[string][]byte{}, writes: map[string]int{}}
}

func (o *backfillMemoryObjects) URL(key string) string { return "https://cdn.example.test/" + key }
func (o *backfillMemoryObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.files[key] = bytes.Clone(data)
	o.writes[key]++
	return nil
}
func (o *backfillMemoryObjects) PutMissing(ctx context.Context, key string, data []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if key == o.failKey {
		return errors.New("fixture upload failure")
	}
	if _, exists := o.files[key]; !exists {
		o.files[key] = bytes.Clone(data)
		o.writes[key]++
	}
	return nil
}
func (o *backfillMemoryObjects) Read(_ context.Context, key string) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reads = append(o.reads, key)
	data, ok := o.files[key]
	if !ok {
		return nil, minio.ErrorResponse{Code: "NoSuchKey"}
	}
	return bytes.Clone(data), nil
}

func (o *backfillMemoryObjects) ReadLimited(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	data, err := o.Read(ctx, key)
	if err == nil && int64(len(data)) > maxBytes {
		return nil, errors.New("image too large")
	}
	return data, err
}

func backfillPNG(t *testing.T, width int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < width; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), A: 255})
		}
	}
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, source))
	return data.Bytes()
}
