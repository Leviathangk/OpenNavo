package assets

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/opennavo/opennavo/server/internal/images"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestS3BackfillBoundedRead(t *testing.T) {
	data := bytes.Repeat([]byte{42}, images.MaxUploadBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Last-Modified", "Mon, 26 Oct 2026 00:00:00 GMT")
		// No Content-Length: enforce the bound while reading the stream too.
		w.(http.Flusher).Flush()
		_, _ = w.Write(data)
	}))
	defer server.Close()
	client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{Creds: credentials.NewStaticV4("fixture-access", "fixture-secret", ""), Region: "us-east-1"})
	require.NoError(t, err)
	objects := S3Objects{Client: &storage.Client{S3: client, Bucket: "fixture-bucket"}}
	read, err := objects.ReadLimited(context.Background(), "normalized.png", images.MaxNormalizedBytes)
	require.NoError(t, err)
	require.Equal(t, data, read)
	read, err = objects.ReadLimited(context.Background(), "normalized.png", int64(len(data)))
	require.NoError(t, err)
	require.Equal(t, data, read)
	read, err = objects.ReadLimited(context.Background(), "normalized.png", 1024)
	require.EqualError(t, err, "image too large")
	require.Nil(t, read)
	_, err = objects.Read(context.Background(), "original.png")
	require.EqualError(t, err, "image too large")
}

func TestS3BackfillPutMissing(t *testing.T) {
	for _, tc := range []struct {
		name         string
		initialSize  int
		race, denied bool
		wantPuts     int
		wantError    bool
	}{
		{name: "new then repeat", initialSize: -1, wantPuts: 1},
		{name: "reuse interrupted upload", initialSize: 4},
		{name: "conditional race", initialSize: -1, race: true, wantPuts: 1},
		{name: "never overwrite conflicting size", initialSize: 5, wantError: true},
		{name: "head denied does not mean missing", initialSize: -1, denied: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			size, puts, conditional := tc.initialSize, 0, ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case http.MethodHead:
					if tc.denied {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					if size < 0 {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					w.Header().Set("Content-Length", strconv.Itoa(size))
					w.Header().Set("Content-Type", "image/webp")
					w.Header().Set("Last-Modified", "Mon, 26 Oct 2026 00:00:00 GMT")
					w.Header().Set("ETag", `"fixture"`)
				case http.MethodPut:
					puts++
					conditional = r.Header.Get("If-None-Match")
					_, _ = io.Copy(io.Discard, r.Body)
					size = 4
					if tc.race {
						w.WriteHeader(http.StatusPreconditionFailed)
						_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code><Message>already exists</Message></Error>`)
						return
					}
					w.Header().Set("ETag", `"fixture"`)
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{Creds: credentials.NewStaticV4("fixture-access", "fixture-secret", ""), Region: "us-east-1"})
			require.NoError(t, err)
			objects := S3Objects{Client: &storage.Client{S3: client, Bucket: "fixture-bucket"}}
			for range 2 {
				err := objects.PutMissing(context.Background(), "icons/fixture.webp", []byte("webp"), "image/webp")
				require.Equal(t, tc.wantError, err != nil, "%v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, tc.wantPuts, puts)
			if puts > 0 {
				require.Equal(t, "*", conditional)
			}
		})
	}
}
