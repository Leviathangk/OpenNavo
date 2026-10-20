package seokeywords

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizationIntentAndUnsupportedMarkets(t *testing.T) {
	require.Equal(t, "example editor mac", Normalize(" ＥＸＡＭＰＬＥ＿Editor— Mac　"))
	require.False(t, containsTerm("support for mac", "port"))
	require.True(t, containsTerm("Example Editor for Mac", "example-editor"))
	for _, query := range []string{"Example uninstall", "Example 卸载", "Example アンインストール", "Example desinstalar", "Example удалить", "Example deinstallieren", "Example désinstaller", "Example disinstallare", "Example 解除安裝"} {
		intent, blocked := Intent(query)
		require.Equal(t, "uninstall", intent, query)
		require.False(t, blocked)
	}
	_, blocked := Intent("Example クラック")
	require.True(t, blocked)
	zero := int64(0)
	o := Observation{Keyword: "Example", Source: "openseo:dataforseo", Engine: "google", Country: "RU", Language: "ru-RU", Device: "desktop", Period: "2026-10", SearchVolume: &zero, VolumeState: "known", ObservedAt: time.Now().UTC()}
	require.NoError(t, validate(&o, time.Now()))
	require.Equal(t, "unsupported", o.VolumeState)
	require.Nil(t, o.SearchVolume)
	o.Country = "CN"
	require.Error(t, validate(&o, time.Now()))
	o.Country = "US"
	o.Language = "en-US"
	o.VolumeState = "unknown"
	o.Period = "2026-99"
	require.Error(t, validate(&o, time.Now()))
}
