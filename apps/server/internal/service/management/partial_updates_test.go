package management

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPartialStateHashMatchesMCP(t *testing.T) {
	hash, err := partialStateHash(map[string]any{"id": 1, "i18n": map[string]any{"zh-CN": map[string]any{"name": "内存<>&\x7f😀\n"}}})
	require.NoError(t, err)
	require.Equal(t, "471253902f4f88d7a895c263464f4a7e4a342c344baa56ba98e219601d6bc24e", hash)
}
