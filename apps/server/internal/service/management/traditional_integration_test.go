//go:build integration

package management

import (
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTraditionalSEOMetadataRemainsWritable(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://example.test"}}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','example-editor','example-editor','homebrew/cask','Example Editor','1','1','{}','fixture')`).Error)
	_, err := svc.Execute(hermesContext("seo:keyword:edit"), "UpsertSeoKeywords", Input{Body: map[string]any{
		"records": []any{map[string]any{"keyword": "編輯器", "source": "gsc", "engine": "google", "country": "TW", "language": "zh-Hant", "device": "desktop", "period": "2026-10", "volumeState": "unknown", "observedAt": time.Now().UTC().Format(time.RFC3339)}},
		"targets": []any{map[string]any{"entityKey": "pkg:701", "intent": "download", "locale": "zh-Hant", "targetUrl": "https://example.test/zh-hant/apps/example-editor"}},
	}})
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("seo_targets").Where("locale='zh-Hant'").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
