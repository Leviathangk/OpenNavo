package traditional

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversionProtectsLiteralsAndStructure(t *testing.T) {
	text, err := Text("软件和鼠标，头发；{数量} `简体命令` [下载](https://example.test/简体) <a title=\"简体\">下载</a> 品牌软件", []string{"品牌软件"})
	require.NoError(t, err)
	require.Equal(t, "軟體和滑鼠，頭髮；{数量} `简体命令` [下載](https://example.test/简体) <a title=\"简体\">下載</a> 品牌软件", text)
	fields, err := Fields(map[string]any{"简体键": []any{"软件", nil, map[string]any{"url": "https://example.test/简体"}}}, nil)
	require.NoError(t, err)
	require.Equal(t, []any{"軟體", nil, map[string]any{"url": "https://example.test/简体"}}, fields["简体键"])
}
func TestConcurrentConversion(t *testing.T) {
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 10 {
				out, err := Text("软件", nil)
				if err != nil || out != "軟體" {
					t.Error("conversion failed")
				}
			}
		})
	}
	wg.Wait()
}

func TestFencesProtectLiteralExamples(t *testing.T) {
	for _, block := range []string{
		"````markdown\n```sh\necho 软件\n```\n````\n",
		"~~~~markdown\n~~~sh\necho 软件\n~~~\n~~~~\n",
		"```sh\necho 软件 ```\necho 软件\n```\n",
		"   ````sh\n```\necho 软件\n   `````  \n",
		"```sh\necho 软件\n", // Unclosed fence continues to EOF.
		"```sh\r\necho 软件\r\n```\r\n",
	} {
		got, err := Text("软件\n"+block, nil)
		require.NoError(t, err)
		require.Equal(t, "軟體\n"+block, got)
	}
	got, err := Text("````\n```\necho 软件\n```\n````\n软件", nil)
	require.NoError(t, err)
	require.Equal(t, "````\n```\necho 软件\n```\n````\n軟體", got)
}

func TestContainerCodeBlocks(t *testing.T) {
	for _, block := range []string{
		"> ~~~sh\n> echo 软件\n> ~~~\n",
		"- 软件\n\n    ~~~sh\n    echo 软件\n    ~~~\n",
		"> ````markdown\n> ```sh\n> echo 软件\n> ```\n> ````\n",
		"1. 软件\n\n   > ~~~sh\n   > echo 软件\n   > ~~~\n",
		"- 软件\n\n      echo 软件\n",
	} {
		got, err := Text(block+"\n软件", nil)
		require.NoError(t, err)
		require.Contains(t, got, "echo 软件")
		require.NotContains(t, got, "echo 軟體")
		require.True(t, strings.HasSuffix(got, "\n軟體"), got)
	}
	got, err := Text("> ~~~sh\n> echo 软件\n\n软件", nil)
	require.NoError(t, err)
	require.Equal(t, "> ~~~sh\n> echo 软件\n\n軟體", got)
}

func TestMarkdownSyntaxNeverBecomesConvertibleProse(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"```sh\necho 软件\n```\n\n软件 `软件.json`", "```sh\necho 软件\n```\n\n軟體 `软件.json`"},
		{"``软件 `echo 软件` 文件`` 软件", "``软件 `echo 软件` 文件`` 軟體"},
		{"软件 [下载](./a(软件)/软件.txt)", "軟體 [下載](./a(软件)/软件.txt)"},
		{"软件 [下载][资源]\n\n[资源]: ./软件.txt", "軟體 [下載][资源]\n\n[资源]: ./软件.txt"},
		{"软件 [软件][]\n\n[软件]: ./软件.txt", "軟體 [软件][]\n\n[软件]: ./软件.txt"},
	} {
		got, err := Text(tc.input, nil)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}
