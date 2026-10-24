package traditional

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPinnedDictionaryEvidence(t *testing.T) {
	raw, err := os.ReadFile("data/upstream.json")
	require.NoError(t, err)
	var manifest struct {
		Files []struct{ File, SHA256 string }
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Len(t, manifest.Files, 8)
	for _, file := range manifest.Files {
		raw, err := os.ReadFile("data/" + file.File)
		require.NoError(t, err)
		hash := sha256.Sum256(raw)
		require.Equal(t, file.SHA256, hex.EncodeToString(hash[:]), file.File)
	}
}
func TestPhraseDisambiguation(t *testing.T) {
	for _, pair := range [][2]string{{"头发发展", "頭髮發展"}, {"干燥的面条", "乾燥的麵條"}, {"软件和数据库", "軟體和資料庫"}, {"计算机鼠标", "計算機滑鼠"}} {
		got, err := Text(pair[0], nil)
		require.NoError(t, err)
		require.Equal(t, pair[1], got)
	}
}
