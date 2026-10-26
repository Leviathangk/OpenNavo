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

// The wording rules are OpenNavo data: outside the upstream manifest, and tied to Version, because
// stored derived text is re-derived only when Version changes.
func TestWordingRulesData(t *testing.T) {
	raw, err := os.ReadFile("data/upstream.json")
	require.NoError(t, err)
	require.NotContains(t, string(raw), "opennavo-terms.json")
	raw, err = os.ReadFile("data/opennavo-terms.json")
	require.NoError(t, err)
	require.Equal(t, termsFile, raw, "the embedded rules are the file")
	hash := sha256.Sum256(raw)
	require.Equal(t, [2]string{"opencc-data-1.1.9-s2twp-v5", "e6618093d4891776e8c66266600d586242f0e01374e35252a3357a8da1e5360e"}, [2]string{Version, hex.EncodeToString(hash[:])},
		"a wording change needs a new Version, then this pair")
	rules, err := wording()
	require.NoError(t, err)
	require.Len(t, rules, 21)
	seen := map[string]bool{}
	for _, rule := range rules {
		require.False(t, seen[rule.From], rule.From)
		seen[rule.From] = true
	}
	for _, bad := range []string{
		`{"replace":[{"from":"賬號","to":"帳號","why":"x","whole":true}]}`,
		`{"replace":[{"from":"賬號","to":"帳號"}]}`,
		`{"replace":[{"from":"帳號","to":"帳號","why":"x"}]}`,
	} {
		_, err := loadTerms([]byte(bad))
		require.Error(t, err, bad)
	}
}

// The cases of the repository generator's former replace tests, on the converter's output.
func TestWordingRules(t *testing.T) {
	rules, err := wording()
	require.NoError(t, err)
	for _, pair := range [][2]string{
		{"需要管理員許可權", "需要管理員權限"},
		{"移除登入項", "移除登入項目"},
		{"已在登入項目裡", "已在登入項目裡"},
		{"登入項目和登入項", "登入項目和登入項目"},
		{"授予完全磁碟訪問許可權", "授予完整磁碟取用權限"},
		{"使用輔助功能和輔助功能", "使用輔助使用功能和輔助使用"},
		{"開啟「允許在後臺」", "開啟「允許在背景執行」"},
		{"系統設定 → 通用 → 登入項", "系統設定 → 一般 → 登入項目"},
		{"Apple 賬戶、App Store 賬戶、賬號和賬戶", "Apple 帳號、App Store 帳號、帳號和帳戶"},
		{"這臺、整臺和每臺 Mac", "這台、整台和每台 Mac"},
		{"它宣告瞭權限", "它宣告了權限"},
		{"在後臺執行", "在後臺執行"}, // context rule: the repository generator decides by the English source
	} {
		require.Equal(t, pair[1], applyTerms(pair[0], rules), pair[0])
	}
}

// Wording applies after conversion, to prose only.
func TestWordingSkipsLiterals(t *testing.T) {
	got, err := Text("打开「通用」，`「通用」` {「通用」} [「通用」](https://example.test/「通用」) <b title=\"「通用」\">「通用」</b> 品牌「通用」", []string{"品牌「通用」"})
	require.NoError(t, err)
	require.Equal(t, "開啟「一般」，`「通用」` {「通用」} [「一般」](https://example.test/「通用」) <b title=\"「通用」\">「一般」</b> 品牌「通用」", got)
	for _, pair := range [][2]string{
		{"需要管理员权限", "需要管理員權限"},
		{"移除登录项", "移除登入項目"},
		{"菜单栏图标和废纸篓", "選單列圖示和垃圾桶"},
		{"```sh\necho 菜单栏\n```\n菜单栏", "```sh\necho 菜单栏\n```\n選單列"},
	} {
		got, err := Text(pair[0], nil)
		require.NoError(t, err)
		require.Equal(t, pair[1], got, pair[0])
	}
}

func TestPhraseDisambiguation(t *testing.T) {
	for _, pair := range [][2]string{{"头发发展", "頭髮發展"}, {"干燥的面条", "乾燥的麵條"}, {"软件和数据库", "軟體和資料庫"}, {"计算机鼠标", "計算機滑鼠"}} {
		got, err := Text(pair[0], nil)
		require.NoError(t, err)
		require.Equal(t, pair[1], got)
	}
}
