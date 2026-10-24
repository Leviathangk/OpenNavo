package seoopportunities

import (
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/stretchr/testify/require"
)

func TestDemandIntentAndMultilingualSlugs(t *testing.T) {
	for _, query := range []string{"video editor mac", "antivirus mac", "programme deinstallieren mac", "désinstaller application mac", "mac卸载软件", "mac アンインストール", "mac remove app", "desinstalar mac", "удалить mac", "mac disinstallare", "Mac解除安裝", "download video editor mac", "best editor mac", "unknown product mac"} {
		kind, _ := demandKind(query)
		require.Equal(t, "topic", kind, query)
		require.True(t, articles.ValidSlug(demandSlug(query), 100), query)
	}
	kind, intent := demandKind("download exampleproduct mac")
	require.Equal(t, "listing", kind)
	require.Equal(t, "download", intent)
	for _, query := range []string{"mac crack editor", "mac 破解版", "mac クラック", "editor windows", "antivirus imac"} {
		kind, _ := demandKind(query)
		require.Empty(t, kind, query)
	}
	require.Equal(t, "desinstaller-application-mac", demandSlug("désinstaller application mac"))
	require.NotEqual(t, demandSlug("mac 卸载软件"), demandSlug("mac 视频编辑"))
	require.Equal(t, demandSlug("MAC 卸载软件"), demandSlug("mac 卸载软件"))
	require.True(t, articles.ValidSlug(demandSlug(strings.Repeat("editor ", 100)+"mac"), 100))
}
