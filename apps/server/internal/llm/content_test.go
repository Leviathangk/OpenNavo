package llm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentValidationProtectsSourceAndLanguage(t *testing.T) {
	source := "The Orbit app manages files.\n\n```sh\nbrew install --cask orbit\n```\nUse `OPENNAVO_HOME` and [guide](https://example.com/guide), version 1.2.3 at /Applications/Orbit.app."
	good := "Orbit はファイルを管理するアプリです。\n\n```sh\nbrew install --cask orbit\n```\n`OPENNAVO_HOME` と [ガイド](https://example.com/guide)、バージョン 1.2.3 の /Applications/Orbit.app を使用します。"
	in := ContentInput{SourceLocale: "en-US", Targets: []string{"ja-JP"}, Fields: map[string]string{"body": source}, Limits: map[string]int{"body": 1000}, Protected: []string{"Orbit"}}
	require.NoError(t, ValidateContent(ContentOutput{"ja-JP": {"body": good}}, in))
	for _, test := range []struct{ name, text, reason string }{
		{"code", strings.Replace(good, "install", "uninstall", 1), "changed_literal"},
		{"link", strings.ReplaceAll(good, "https://example.com/guide", "https://example.com/other"), "changed_literal"},
		{"missing link", strings.ReplaceAll(good, "https://example.com/guide", ""), "changed_literal"},
		{"brand", strings.ReplaceAll(good, "Orbit", "軌道"), "changed_literal"},
		{"unchanged", source, "untranslated"},
		{"too long", good + strings.Repeat("あ", 1000), "too_long"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateContent(ContentOutput{"ja-JP": {"body": test.text}}, in)
			require.ErrorContains(t, err, test.reason)
		})
	}
	require.Equal(t, source, in.Fields["body"])
	for _, test := range []struct{ locale, text string }{{"en-US", "The application manages your files"}, {"zh-CN", "管理文件的应用"}, {"ja-JP", "ファイルを管理するアプリです"}, {"es-ES", "La aplicación permite gestionar tus archivos"}, {"pt-BR", "O aplicativo permite gerenciar seus arquivos"}, {"ru-RU", "Приложение для управления файлами"}} {
		t.Run(test.locale, func(t *testing.T) {
			require.True(t, MatchesLanguage(test.text, test.locale))
			bad := "Это приложение для файлов"
			if test.locale == "ru-RU" {
				bad = "ファイルを管理します"
			}
			require.False(t, MatchesLanguage(bad, test.locale))
		})
	}
	require.False(t, MatchesLanguage("The application manages your files", "es-ES"))
	require.False(t, MatchesLanguage("The application manages your files", "pt-BR"))
	require.ErrorContains(t, ValidateContent(ContentOutput{"ja-JP": {"body": "The application manages your files"}}, ContentInput{SourceLocale: "zh-CN", Targets: []string{"ja-JP"}, Fields: map[string]string{"body": "管理文件的应用"}}), "wrong_language")
}
func TestContentValidationSchemaGlossaryAndBrand(t *testing.T) {
	in := ContentInput{SourceLocale: "en-US", Targets: []string{"zh-CN"}, Fields: map[string]string{"text": "Install Orbit"}, Protected: []string{"Orbit"}, Glossary: map[string]map[string]string{"Install": {"zh-CN": "安装"}}}
	require.NoError(t, ValidateContent(ContentOutput{"zh-CN": {"text": "安装 Orbit 应用"}}, in))
	require.ErrorContains(t, ValidateContent(ContentOutput{"zh-CN": {"text": "安装应用"}}, in), "changed_brand")
	require.ErrorContains(t, ValidateContent(ContentOutput{"zh-CN": {"text": "获取 Orbit 应用"}}, in), "missing_glossary")
	require.ErrorContains(t, ValidateContent(ContentOutput{"zh-CN": {"text": "安装 Orbit", "extra": "额外"}}, in), "invalid_schema")
	require.ErrorContains(t, ValidateContent(ContentOutput{"ru-RU": {"text": "安装 Orbit"}}, in), "invalid_schema")
}

func TestRelativeLinksCannotBeRemovedOrChanged(t *testing.T) {
	in := ContentInput{SourceLocale: "en-US", Targets: []string{"ja-JP"}, Fields: map[string]string{"text": "Read the [guide](docs/help.md)"}}
	require.NoError(t, ValidateContent(ContentOutput{"ja-JP": {"text": "[ガイド](docs/help.md) をお読みください"}}, in))
	require.ErrorContains(t, ValidateContent(ContentOutput{"ja-JP": {"text": "[ガイド](docs/other.md) をお読みください"}}, in), "changed_literal")
}

func TestPathLiteralEndsAtCJKProsePunctuation(t *testing.T) {
	in := ContentInput{SourceLocale: "en-US", Targets: []string{"zh-CN"}, Fields: map[string]string{"text": "Flip X/Y and wheels, swap X/Y and swap wheels. Open /Applications/Orbit.app."}}
	translated := "翻转 X/Y 和滚轮、交换 X/Y，以及交换滚轮。打开 /Applications/Orbit.app。"
	require.NoError(t, ValidateContent(ContentOutput{"zh-CN": {"text": translated}}, in))
	require.ErrorContains(t, ValidateContent(ContentOutput{"zh-CN": {"text": strings.Replace(translated, "/Applications/Orbit.app", "/Applications/Other.app", 1)}}, in), "changed_literal")
}

func TestLiteralPathsAdjacentToCJK(t *testing.T) {
	for _, pair := range [][2]string{
		{"src/theme/settings.ts 中的变量", "src/theme/settings.tsの変数"},
		{"desktop/{channelKey}/latest.json 中的版本", "desktop/{channelKey}/latest.jsonのバージョン"},
		{"https://example.com/path?q=1 中的内容", "https://example.com/path?q=1の内容"},
		{"/opt/app/config.json 中文", "/opt/app/config.json中文"},
		{"https://example.com/file 中文", "https://example.com/file中文"},
		{"/opt/app/config.json", "/opt/app/config.json（設定）"},
		{"/opt/app/config.json", "/opt/app/config.json「設定」"},
		{"/opt/app/config.json", "/opt/app/config.json　設定"},
	} {
		require.Equal(t, LiteralSpans(pair[0]), LiteralSpans(pair[1]))
	}
	require.NotEqual(t, LiteralSpans("src/theme/settings.ts"), LiteralSpans("src/theme/changed.tsの変数"))
	require.NotEqual(t, LiteralSpans("https://example.com/path"), LiteralSpans("https://example.com/changedの内容"))
}

func TestProseSlashesAreNotPathLiterals(t *testing.T) {
	source := "See [Installation and desktop/web differences](https://learning.postman.com/docs/install/). Use it with and/or without the CLI; settings live in ~/Library/App/config.json and docs/help.md."
	in := ContentInput{SourceLocale: "en-US", Targets: []string{"de-DE"}, Fields: map[string]string{"text": source}}
	translated := "Siehe [Installation und Unterschiede zwischen Desktop- und Web-App](https://learning.postman.com/docs/install/). Nutze sie mit oder ohne die CLI; die Einstellungen liegen in ~/Library/App/config.json und docs/help.md."
	require.NoError(t, ValidateContent(ContentOutput{"de-DE": {"text": translated}}, in))
	for _, changed := range []string{"~/Library/App/other.json", "docs/other.md"} {
		broken := strings.Replace(strings.Replace(translated, "~/Library/App/config.json", changed, 1), "docs/help.md", changed, 1)
		require.ErrorContains(t, ValidateContent(ContentOutput{"de-DE": {"text": broken}}, in), "changed_literal", changed)
	}
	require.Empty(t, LiteralSpans("desktop/web and/or X/Y TCP/IP"))
	require.Equal(t, []string{"/Applications/Orbit.app", "/web"}, LiteralSpans("Open /Applications/Orbit.app or the /web endpoint"))
}
