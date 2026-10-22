//go:build integration

package assets

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type fakeObjects struct {
	mu    sync.Mutex
	files map[string][]byte
	err   error
}

func (f *fakeObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.files[key] = data
	return nil
}
func (f *fakeObjects) URL(key string) string { return "https://cdn.example.test/" + key }
func TestAssetUploadAuditAndAccent(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	objects := &fakeObjects{files: map[string][]byte{}}
	service := &Service{Store: st, Objects: objects}
	var n int64
	asset, err := service.Upload(ctx, "screenshot", fixturePNG(t), nil, store.AuditActor{IP: "192.0.2.1"})
	require.NoError(t, err)
	require.Equal(t, 1280, *asset.Width)
	require.NoError(t, st.DB.WithContext(ctx).Table("audit_logs").Count(&n).Error)
	require.Equal(t, int64(1), n)
	_, err = service.Upload(ctx, "icon", []byte("invalid"), nil, store.AuditActor{})
	require.Error(t, err)
	_, err = service.Upload(ctx, "unknown", fixturePNG(t), nil, store.AuditActor{})
	require.Error(t, err)
	icon, err := service.Upload(ctx, "icon", fixturePNG(t), nil, store.AuditActor{})
	require.NoError(t, err)
	accent, err := service.IconAccent(ctx, icon.Id)
	require.NoError(t, err)
	require.NotNil(t, accent)
	require.Equal(t, "#336699", *accent)
	objects.err = errors.New("storage unavailable")
	_, err = service.Upload(ctx, "icon", fixturePNG(t), nil, store.AuditActor{})
	require.Error(t, err)
}

func (f *fakeObjects) Read(_ context.Context, key string) ([]byte, error) { return f.files[key], nil }
func fixturePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1280, 1280))
	for y := 0; y < 1280; y++ {
		for x := 0; x < 1280; x++ {
			img.Set(x, y, color.RGBA{R: 51, G: 102, B: 153, A: 255})
		}
	}
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return b.Bytes()
}

func TestAssetURLUploadAndPreview(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	objects := &fakeObjects{files: map[string][]byte{}}
	source := "https://example.com/icon.png"
	service := &Service{Store: st, Objects: objects, URLClient: &http.Client{Transport: imageURLTransport(func(r *http.Request) (*http.Response, error) {
		data := fixturePNG(t)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: r}, nil
	})}}
	data, err := service.DownloadURL(ctx, source)
	require.NoError(t, err)
	preview, err := service.Preview(ctx, "icon", data)
	require.NoError(t, err)
	require.Equal(t, "icon", preview["kind"])
	var count int64
	require.NoError(t, st.DB.Table("assets").Count(&count).Error)
	require.Zero(t, count)
	require.Empty(t, objects.files)
	asset, err := service.Upload(ctx, "icon", data, &source, store.AuditActor{})
	require.NoError(t, err)
	require.Positive(t, asset.Id)
	stored, err := st.AssetByID(ctx, asset.Id)
	require.NoError(t, err)
	require.Equal(t, &source, stored.SourceURL)
	require.NotEmpty(t, objects.files)
}

func TestAgentUploadWithoutTaskOrEvidence(t *testing.T) {
	st := testutil.NewSEOStore(t)
	objects := &fakeObjects{files: map[string][]byte{}}
	service := &Service{Store: st, Objects: objects}
	op := &domain.Operation{RequestID: uuid.NewString(), AgentClientID: 1, AgentTokenID: 2, Name: "uploadAsset", Permissions: []string{"catalog:asset:upload"}, RequiredPermissions: []string{"catalog:asset:upload"}, DryRun: true}
	ctx := domain.WithOperation(context.Background(), op)
	require.NoError(t, service.ValidateUploadTicket(ctx))
	preview, err := service.UploadAgent(ctx, "icon", fixturePNG(t), nil, store.AuditActor{})
	require.NoError(t, err)
	require.Equal(t, "preview", preview.(map[string]any)["mode"])
	require.Empty(t, objects.files)
	op.DryRun = false
	_, err = service.UploadAgent(ctx, "icon", fixturePNG(t), nil, store.AuditActor{})
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("assets").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("content_revisions").Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err = service.UploadAgent(ctx, "icon", []byte("invalid image"), nil, store.AuditActor{})
	require.Error(t, err)
}
