package assets

import (
	"strconv"
	"strings"

	"github.com/opennavo/opennavo/server/internal/images"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Both uploads and historical backfills use the normalized and rendition content
// hashes, retaining the existing asset's directory during a backfill.
func renditionFile(objects Objects, prefix, normalizedHash string, v images.Rendition) store.AssetFile {
	key := prefix + normalizedHash[:12] + "-" + v.Role + "-" + v.SHA256[:12] + "-" + strconv.Itoa(v.Width) + "." + strings.TrimPrefix(v.MIME, "image/")
	return store.AssetFile{Width: v.Width, Height: v.Height, URL: objects.URL(key), Type: v.MIME, Role: v.Role, StorageKey: key, SHA256: v.SHA256, Bytes: int64(len(v.Data))}
}
