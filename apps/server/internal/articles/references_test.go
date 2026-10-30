package articles

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactPrivatePackageReferences(t *testing.T) {
	private := map[string]bool{"private-sample": true, "private-alias": true}
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "relative link",
			body: "See [Tencent Meeting](/apps/private-sample) for notes.",
			want: "See Tencent Meeting for notes.",
		},
		{
			name: "localized link",
			body: "See [Tencent Meeting](/zh/apps/private-sample) and [Alias](/ja/apps/private-alias).",
			want: "See Tencent Meeting and Alias.",
		},
		{
			name: "absolute links",
			body: "[Tencent Meeting](https://opennavo.com/apps/private-sample) and [Web](https://www.opennavo.com/zh/apps/private-sample).",
			want: "Tencent Meeting and Web.",
		},
		{
			name: "http links",
			body: "See [Tencent Meeting](http://opennavo.com/apps/private-sample) and [Web](http://www.opennavo.com/zh/apps/private-sample).",
			want: "See Tencent Meeting and Web.",
		},
		{
			name: "scheme-less links",
			body: "See [Tencent Meeting](opennavo.com/apps/private-sample) and [Web](www.opennavo.com/zh/apps/private-sample).",
			want: "See Tencent Meeting and Web.",
		},
		{
			name: "trailing segment query and fragment",
			body: "[Releases](/apps/private-sample/releases?tab=versions#v2) and [Localized](/ja/apps/private-sample/?page=2#top).",
			want: "Releases and Localized.",
		},
		{
			name: "app directive line",
			body: "Before\n::app[private-sample]\nAfter\n::app[private-sample]",
			want: "Before\nAfter\n",
		},
		{
			name: "public and unknown references kept",
			body: "::app[public-child]\n::app[unknown-sample]\n[Public](/apps/public-child) [Unknown](/apps/unknown-sample)",
			want: "::app[public-child]\n::app[unknown-sample]\n[Public](/apps/public-child) [Unknown](/apps/unknown-sample)",
		},
		{
			name: "fenced code untouched",
			body: "Before [Tencent Meeting](/apps/private-sample).\n```\n[Tencent Meeting](/apps/private-sample)\n::app[private-sample]\n```\nAfter [x](/zh/apps/private-sample).",
			want: "Before Tencent Meeting.\n```\n[Tencent Meeting](/apps/private-sample)\n::app[private-sample]\n```\nAfter x.",
		},
		{
			name: "inline code untouched",
			body: "Use `[Tencent Meeting](/apps/private-sample)` literally, then [x](/apps/private-sample).",
			want: "Use `[Tencent Meeting](/apps/private-sample)` literally, then x.",
		},
		{
			name: "long inline code untouched",
			body: "Use ``[Tencent Meeting](/apps/private-sample)`` literally, then [x](/apps/private-sample).",
			want: "Use ``[Tencent Meeting](/apps/private-sample)`` literally, then x.",
		},
		{
			name: "multiple links on one line",
			body: "[One](/apps/private-sample) / [Two](/zh/apps/private-sample) / [Public](/apps/public-child) / [Unknown](/apps/unknown-sample)",
			want: "One / Two / [Public](/apps/public-child) / [Unknown](/apps/unknown-sample)",
		},
		{
			name: "autolinks",
			body: "See <https://opennavo.com/apps/private-sample> and <https://www.opennavo.com/ja/apps/private-alias?tab=releases#top> for details.",
			want: "See  and  for details.",
		},
		{
			name: "http autolinks",
			body: "See <http://opennavo.com/apps/private-sample> and <http://www.opennavo.com/ja/apps/private-alias?tab=releases#top> for details.",
			want: "See  and  for details.",
		},
		{
			name: "bare linkified URLs",
			body: "Read https://opennavo.com/apps/private-sample, https://www.opennavo.com/zh/apps/private-alias. Then open opennavo.com/apps/private-sample for the rest.",
			want: "Read , . Then open  for the rest.",
		},
		{
			name: "http bare linkified URLs",
			body: "Read http://opennavo.com/apps/private-sample, http://www.opennavo.com/zh/apps/private-alias. Then open www.opennavo.com/apps/private-sample for the rest.",
			want: "Read , . Then open  for the rest.",
		},
		{
			name: "bare URL with trailing punctuation",
			body: "See opennavo.com/apps/private-sample; it is gone.",
			want: "See ; it is gone.",
		},
		{
			name: "other host with the same suffix kept",
			body: "See https://notopennavo.com/apps/private-sample and foo.opennavo.com/apps/private-sample.",
			want: "See https://notopennavo.com/apps/private-sample and foo.opennavo.com/apps/private-sample.",
		},
		{
			name: "codex r2: invalid definition after a paragraph cannot hide a private reference",
			body: "intro\n[p]: /apps/public-child\n\n[x][p]\n\n[p]: /apps/private-sample\n",
			// markdown-it resolves [p] to the private definition, so it becomes plain text.
			want: "intro\np: /apps/public-child\n\nx\n\n",
		},
		{
			name: "codex r2: escaped backslash before a private link keeps the public link",
			body: "[public](/apps/public-child) \\\\[private](/apps/private-sample)",
			want: "[public](/apps/public-child) \\\\private",
		},
		{
			name: "codex r2: multiline link in a blockquote leaves no private URL",
			body: "> [x](https://opennavo.com/apps/private-sample\n> \"title\")",
			want: "> \n> \"title\")",
		},
		{
			name: "codex r2: uppercase bare URLs",
			body: "HTTPS://opennavo.com/apps/private-sample and https://OPENNAVO.COM/apps/private-sample end",
			want: " and  end",
		},
		{
			name: "codex r2: zero padded default port",
			body: "https://opennavo.com:0443/apps/private-sample end",
			want: " end",
		},
		{
			name: "codex r2: embedded and lookalike URLs untouched by the residual pass",
			body: "https://example.com/https://opennavo.com/apps/private-sample https://notopennavo.com/apps/private-sample https://opennavo.com:8443/apps/private-sample",
			want: "https://example.com/https://opennavo.com/apps/private-sample https://notopennavo.com/apps/private-sample https://opennavo.com:8443/apps/private-sample",
		},
		{
			name: "reference links and definitions",
			body: "[Tencent Meeting][tm] and [Alias][] and [tm].\n[tm]: https://opennavo.com/apps/private-sample\n[Alias]: /ja/apps/private-alias\n",
			// Definitions cannot interrupt a paragraph, so markdown-it renders these lines as text;
			// the residual pass strips the private paths from that text.
			want: "[Tencent Meeting][tm] and [Alias][] and [tm].\n[tm]: \n[Alias]: \n",
		},
		{
			name: "http reference links and definitions",
			body: "[Tencent Meeting][tm] and [Alias][] and [tm].\n[tm]: http://opennavo.com/apps/private-sample\n[Alias]: http://www.opennavo.com/ja/apps/private-alias\n",
			want: "[Tencent Meeting][tm] and [Alias][] and [tm].\n[tm]: \n[Alias]: \n",
		},
		{
			name: "public reference links kept",
			body: "[Public child][pub] stays.\n[pub]: https://opennavo.com/apps/public-child",
			want: "[Public child][pub] stays.\n[pub]: https://opennavo.com/apps/public-child",
		},
		{
			name: "raw HTML anchor guard keeps text",
			body: "Raw <a href=\"https://opennavo.com/apps/private-sample\" class=\"x\">Tencent Meeting</a> and <a href='/apps/public-child'>Public child</a>.",
			want: "Raw Tencent Meeting and <a href='/apps/public-child'>Public child</a>.",
		},
		{
			name: "http raw HTML anchor guard keeps text",
			body: "Raw <a href=\"http://opennavo.com/apps/private-sample\" class=\"x\">Tencent Meeting</a> and <a href='http://www.opennavo.com/zh/apps/private-sample'>Web</a>.",
			want: "Raw Tencent Meeting and Web.",
		},
		{
			name: "other paths hosts and syntax untouched",
			body: "[Other](/packages/private-sample) [External](https://example.com/apps/private-sample) ![Image](https://opennavo.com/apps/private-sample/icon.png)",
			// Article images without a listing render as links, so a private image path is removed too.
			want: "[Other](/packages/private-sample) [External](https://example.com/apps/private-sample) ",
		},
		{
			name: "formatted link text kept",
			body: "[**Tencent** Meeting](/apps/private-sample)",
			want: "**Tencent** Meeting",
		},
		{
			name: "explicit default ports",
			body: "[Secure](https://opennavo.com:443/apps/private-sample) [Plain](http://www.opennavo.com:80/apps/private-alias)",
			want: "Secure Plain",
		},
		{
			name: "non-default ports kept",
			body: "[Port](https://opennavo.com:8443/apps/private-sample)",
			want: "[Port](https://opennavo.com:8443/apps/private-sample)",
		},
		{
			name: "percent encoded paths",
			body: "[Encoded](/%61pps/private-sample) and [Absolute](https://opennavo.com/%61pps/private-sample)",
			want: "Encoded and Absolute",
		},
		{
			name: "markdown character references in destinations",
			body: "[Entity](/apps/&#112;rivate-sample) and [Hex](/apps/&#x70;rivate-sample)",
			want: "Entity and Hex",
		},
		{
			name: "backslash escapes in destinations",
			body: "[Escaped](/apps/private\\-sample) and [Host](https://opennavo\\.com/apps/private-sample)",
			want: "Escaped and Host",
		},
		{
			name: "uppercase host with default port",
			body: "[Upper](HTTPS://OPENNAVO.COM:443/apps/private-sample)",
			want: "Upper",
		},
		{
			name: "multiline reference destination",
			body: "[x][p]\n\n[p]:\n  /apps/private-sample\n",
			want: "x\n\n",
		},
		{
			name: "multiline reference destination inside a blockquote",
			body: "[x][p]\n\n> [p]:\n>   /apps/private-sample\n",
			want: "x\n\n",
		},
		{
			name: "reference definition inside a blockquote",
			body: "[x][p]\n\n> [p]: /apps/private-sample\n",
			want: "x\n\n",
		},
		{
			name: "reference definition inside a list",
			body: "[x][p]\n\n- [p]: /apps/private-sample\n",
			want: "x\n\n",
		},
		{
			name: "first reference definition wins over a later private duplicate",
			body: "[public][p]\n\n[p]: /apps/public-child\n[p]: /apps/private-sample\n",
			// The unused duplicate is invisible; only its private path is removed.
			want: "[public][p]\n\n[p]: /apps/public-child\n[p]: \n",
		},
		{
			name: "first private reference definition wins over a later public duplicate",
			body: "[public][p]\n\n[p]: /apps/private-sample\n[p]: /apps/public-child\n",
			want: "public\n\n",
		},
		{
			name: "link text containing inline code is redacted",
			body: "[Use `tool`](/apps/private-sample)",
			want: "Use `tool`",
		},
		{
			name: "indented code block is preserved",
			body: "Before [x](/apps/private-sample)\n\n    [x](/apps/private-sample)\n    https://opennavo.com/apps/private-sample\n    ::app[private-sample]\n\nAfter [y](/apps/private-sample)",
			want: "Before x\n\n    [x](/apps/private-sample)\n    https://opennavo.com/apps/private-sample\n    ::app[private-sample]\n\nAfter y",
		},
		{
			name: "fenced code block inside a blockquote is preserved",
			body: "> ```\n> [x](/apps/private-sample)\n> https://opennavo.com/apps/private-sample\n> ::app[private-sample]\n> ```\n\nAfter [y](/apps/private-sample)",
			want: "> ```\n> [x](/apps/private-sample)\n> https://opennavo.com/apps/private-sample\n> ::app[private-sample]\n> ```\n\nAfter y",
		},
		{
			name: "multiline code span is preserved",
			body: "`multi\n[x](/apps/private-sample)\n::app[private-sample]\nline` and [y](/apps/private-sample)",
			want: "`multi\n[x](/apps/private-sample)\n::app[private-sample]\nline` and y",
		},
		{
			name: "external URL containing an OpenNavo URL is not truncated",
			body: "See https://example.com/https://opennavo.com/apps/private-sample and [Ext](https://example.com/https://opennavo.com/apps/private-sample).",
			want: "See https://example.com/https://opennavo.com/apps/private-sample and [Ext](https://example.com/https://opennavo.com/apps/private-sample).",
		},
		{
			name: "host suffix and subdomain lookalikes kept",
			body: "See https://notopennavo.com/apps/private-sample, https://opennavo.com.evil.com/apps/private-sample and foo.opennavo.com/apps/private-sample.",
			want: "See https://notopennavo.com/apps/private-sample, https://opennavo.com.evil.com/apps/private-sample and foo.opennavo.com/apps/private-sample.",
		},
		{
			name: "HTML data-href is not the href attribute",
			body: "<a data-href=\"/apps/private-sample\" href=\"/apps/public-child\">Public</a>",
			// Raw HTML renders as text (html:false), so the private path in data-href is removed.
			want: "<a  href=\"/apps/public-child\">Public</a>",
		},
		{
			name: "HTML href character references are decoded",
			body: "Raw <a href=\"/apps/&#112;rivate-sample\">Private</a>.",
			want: "Raw Private.",
		},
		{
			name: "HTML attribute value containing a greater-than sign",
			body: "Raw <a href=\"/apps/private-sample\" title=\"1 > 0\">Label</a>.",
			want: "Raw Label.",
		},
		{
			name: "nested parentheses in destinations",
			body: "[x](/apps/private-sample?q=(a)) and [Public](/apps/public-child?q=(a))",
			want: "x and [Public](/apps/public-child?q=(a))",
		},
		{
			name: "angle destination with nested parentheses",
			body: "[x](</apps/private-sample?q=(a)>) and [y](</apps/private-sample?q=(a)> \"title > here\")",
			want: "x and y",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, RedactPrivateAppReferences(test.body, private))
		})
	}
}

// TestRedactPrivatePackageReferencesInRealArticleBody uses the article body shape from the web SSR
// fixture: prose, inline, autolink, linkified, reference-style and raw HTML app references together.
func TestRedactPrivatePackageReferencesInRealArticleBody(t *testing.T) {
	private := map[string]bool{"private-sample": true, "private-alias": true}
	body := `## What to compare

Pick by task. Compare the [Tencent Meeting](/apps/private-sample) and [x](/zh/apps/private-sample) notes.

The public [Public child](/apps/public-child) link stays.

An autolink <https://opennavo.com/apps/private-sample>, an http autolink <http://www.opennavo.com/apps/private-sample>, and the bare https://www.opennavo.com/ja/apps/private-sample and http://opennavo.com/apps/private-sample URLs disappear.

A scheme-less [WWW Meeting](www.opennavo.com/apps/private-sample) and an http [HTTP Meeting](http://opennavo.com/zh/apps/private-sample) keep only their text.

Reference-style [Tencent Meeting][meeting], collapsed [meeting][], and shortcut [meeting] references keep their text.

Raw HTML <a href="https://opennavo.com/zh/apps/private-sample">Tencent Meeting</a> and <a href="http://opennavo.com/apps/private-sample">HTTP Meeting</a> also keep only their text.

::app[private-sample]
::app[public-child]

[meeting]: http://opennavo.com/apps/private-sample "Private sample"
[public]: https://opennavo.com/apps/public-child
`
	require.ElementsMatch(t, []string{"private-sample", "public-child"}, AppReferences(body))
	got := RedactPrivateAppReferences(body, private)
	require.NotContains(t, got, "private-sample")
	require.NotContains(t, got, "/apps/private-sample")
	require.NotContains(t, got, "::app[private-sample]")
	require.NotContains(t, got, "<a href")
	require.Contains(t, got, "Tencent Meeting")
	require.Contains(t, got, "HTTP Meeting")
	require.Contains(t, got, "WWW Meeting")
	require.Contains(t, got, "[Public child](/apps/public-child)")
	require.Contains(t, got, "::app[public-child]")
	require.Contains(t, got, "[public]: https://opennavo.com/apps/public-child")
}

func TestAppReferencesUsesTheSameLinkInterpretation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "explicit port percent encoding and character references",
			body: "[a](https://opennavo.com:443/apps/private-sample) [b](/%61pps/private-alias) [c](/apps/&#112;rivate-sample)",
			want: []string{"private-sample", "private-alias"},
		},
		{
			name: "reference definitions in containers and multiline destinations",
			body: "[a][p] and [b][q]\n\n[p]:\n  /apps/private-sample\n> [q]: /apps/private-alias\n",
			want: []string{"private-sample", "private-alias"},
		},
		{
			name: "first definition wins in discovery",
			body: "[a][p]\n\n[p]: /apps/public-child\n[p]: /apps/private-sample\n",
			// Discovery is a superset: every app path is looked up, the effective one first.
			want: []string{"public-child", "private-sample"},
		},
		{
			name: "a private first definition is discovered",
			body: "[a][p]\n\n[p]: /apps/private-sample\n[p]: /apps/public-child\n",
			want: []string{"private-sample", "public-child"},
		},
		{
			name: "code is not discovered",
			body: "```\n[a](/apps/private-sample)\n::app[private-alias]\n```\n`[b](/apps/private-sample)`\n\n[c](/apps/public-child)",
			want: []string{"public-child"},
		},
		{
			name: "external URLs embedding an OpenNavo URL are not discovered",
			body: "https://example.com/https://opennavo.com/apps/private-sample and [x](https://example.com/https://opennavo.com/apps/private-sample)",
			want: []string{},
		},
		{
			name: "HTML href is decoded and data-href is ignored",
			body: "<a data-href=\"/apps/private-alias\" href=\"/apps/&#112;rivate-sample\">Private</a>",
			want: []string{"private-sample", "private-alias"},
		},
		{
			name: "host suffix lookalikes are not discovered",
			body: "https://notopennavo.com/apps/private-sample https://opennavo.com.evil.com/apps/private-alias",
			want: []string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ElementsMatch(t, test.want, AppReferences(test.body))
		})
	}
}
