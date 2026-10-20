package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHermesTranslationGuard(t *testing.T) {
	source := "## Organize files\n\n- Read [the guide](https://example.test/guide) and keep {count} files.\n\n```sh\nbrew list\n```"
	target := "## 整理文件\n\n- 阅读[使用指南](https://example.test/guide)，并保留 {count} 个文件。\n\n```sh\nbrew list\n```"
	in := ContentInput{SourceLocale: "en-US", Targets: []string{"zh-CN"}, Fields: map[string]string{"body": source}, Strict: true, Glossary: map[string]map[string]string{"files": {"zh-CN": "文件"}}}
	require.NoError(t, ValidateContent(ContentOutput{"zh-CN": {"body": target}}, in))
	for name, bad := range map[string]string{
		"heading":     "整理文件\n\n- 阅读[使用指南](https://example.test/guide)，并保留 {count} 个文件。\n\n```sh\nbrew list\n```",
		"placeholder": "## 整理文件\n\n- 阅读[使用指南](https://example.test/guide)，并保留文件。\n\n```sh\nbrew list\n```",
		"link":        "## 整理文件\n\n- 阅读[使用指南](https://example.test/evil)，并保留 {count} 个文件。\n\n```sh\nbrew list\n```",
		"code":        "## 整理文件\n\n- 阅读[使用指南](https://example.test/guide)，并保留 {count} 个文件。\n\n```sh\nbrew uninstall\n```",
		"language":    source,
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, ValidateContent(ContentOutput{"zh-CN": {"body": bad}}, in)) })
	}
	in.Glossary = map[string]map[string]string{"documents": {"zh-CN": "文档"}}
	in.Fields = map[string]string{"summary": "This application organizes documents into folders and keeps your work easy to find."}
	require.Error(t, ValidateContent(ContentOutput{"zh-CN": {"summary": "文件"}}, in))
	require.Error(t, ValidateContent(ContentOutput{"zh-CN": {"summary": "这款应用可以整理工作内容，让你更方便地查找。"}}, in), "glossary must be enforced")
}
