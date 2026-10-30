package articles

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

func TestRedactPrivatePackageReferences(t *testing.T) {
	private := map[string]bool{"private-sample": true, "private-alias": true}

	assertRedacted := func(t *testing.T, body, got string) {
		t.Helper()
		require.NotContains(t, got, "](/apps/private-sample")
		require.NotContains(t, got, "](/apps/private-alias")
		require.NotContains(t, got, "](/zh/apps/private-sample")
		require.NotContains(t, got, "](/ja/apps/private-alias")
		require.NotContains(t, got, "(https://opennavo.com/apps/private-sample")
		require.NotContains(t, got, "(https://www.opennavo.com/")
		require.NotContains(t, got, "<https://opennavo.com/apps/private-sample>")
		require.NotContains(t, got, "::app[private-sample]")
		require.NotContains(t, got, "::app[private-alias]")
		// Rendered HTML must not expose a clickable private app URL.
		html := renderArticleHTMLBoth(t, got)
		require.NotContains(t, strings.ToLower(html), `href="/apps/private-sample`)
		require.NotContains(t, strings.ToLower(html), `href="https://opennavo.com/apps/private-sample`)
		require.NotContains(t, strings.ToLower(html), `href="http://opennavo.com/apps/private-sample`)
		require.NotContains(t, strings.ToLower(html), `src="/apps/private-sample`)
		_ = body
	}

	t.Run("relative localized absolute http and scheme-less links", func(t *testing.T) {
		cases := []struct{ body, keep string }{
			{"See [Tencent Meeting](/apps/private-sample) for notes.", "Tencent Meeting"},
			{"See [Tencent Meeting](/zh/apps/private-sample) and [Alias](/ja/apps/private-alias).", "Tencent Meeting"},
			{"[Tencent Meeting](https://opennavo.com/apps/private-sample) and [Web](https://www.opennavo.com/zh/apps/private-sample).", "Tencent Meeting"},
			{"See [Tencent Meeting](http://opennavo.com/apps/private-sample) and [Web](http://www.opennavo.com/zh/apps/private-sample).", "Web"},
			{"See [Tencent Meeting](opennavo.com/apps/private-sample) and [Web](www.opennavo.com/zh/apps/private-sample).", "Web"},
			{"[Releases](/apps/private-sample/releases?tab=versions#v2) and [Localized](/ja/apps/private-sample/?page=2#top).", "Releases"},
		}
		for _, c := range cases {
			got := RedactPrivateAppReferences(c.body, private)
			require.Contains(t, got, c.keep)
			assertRedacted(t, c.body, got)
		}
	})

	t.Run("public and unknown references kept", func(t *testing.T) {
		body := "::app[public-child]\n::app[unknown-sample]\n[Public](/apps/public-child) [Unknown](/apps/unknown-sample)"
		got := RedactPrivateAppReferences(body, private)
		require.Contains(t, got, "::app[public-child]")
		require.Contains(t, got, "::app[unknown-sample]")
		require.Contains(t, got, "[Public](/apps/public-child)")
		require.Contains(t, got, "[Unknown](/apps/unknown-sample)")
	})

	t.Run("product: public and external links not harmed", func(t *testing.T) {
		body := strings.Join([]string{
			"[Public](/apps/public-child)",
			"[PublicQ](/apps/public-child?next=/apps/private-sample)",
			"[External](https://example.com/apps/private-sample)",
			"[ExternalQ](https://example.com/?next=https://opennavo.com/apps/private-sample)",
			"[Other](/packages/private-sample)",
			"https://example.com/https://opennavo.com/apps/private-sample",
			"https://notopennavo.com/apps/private-sample",
			"https://opennavo.com.evil.com/apps/private-sample",
			"https://opennavo.com:8443/apps/private-sample",
			"[Port](https://opennavo.com:8443/apps/private-sample)",
			"[PublicEncoded](/apps/private-sample%2Dpublic)",
		}, "\n")
		// private-sample%2Dpublic decodes to private-sample-public (not in private map).
		got := RedactPrivateAppReferences(body, private)
		require.Contains(t, got, "[Public](/apps/public-child)")
		require.Contains(t, got, "[PublicQ](/apps/public-child?next=/apps/private-sample)")
		require.Contains(t, got, "[External](https://example.com/apps/private-sample)")
		require.Contains(t, got, "[ExternalQ](https://example.com/?next=https://opennavo.com/apps/private-sample)")
		require.Contains(t, got, "[Other](/packages/private-sample)")
		require.Contains(t, got, "https://example.com/https://opennavo.com/apps/private-sample")
		require.Contains(t, got, "https://notopennavo.com/apps/private-sample")
		require.Contains(t, got, "https://opennavo.com.evil.com/apps/private-sample")
		require.Contains(t, got, "https://opennavo.com:8443/apps/private-sample")
		require.Contains(t, got, "[Port](https://opennavo.com:8443/apps/private-sample)")
		require.Contains(t, got, "[PublicEncoded](/apps/private-sample%2Dpublic)")
		html := renderArticleHTML(got)
		require.Contains(t, html, `href="/apps/public-child"`)
		require.Contains(t, html, `href="https://example.com/apps/private-sample"`)
	})

	t.Run("product: query and hash on relative apps paths", func(t *testing.T) {
		body := "[A](/apps/private-sample?x=1#y) [B](/zh-CN/apps/private-alias/releases?tab=1#top)"
		got := RedactPrivateAppReferences(body, private)
		require.Contains(t, got, "A")
		require.Contains(t, got, "B")
		assertRedacted(t, body, got)
	})

	t.Run("code fences spans and indented blocks preserve private literals", func(t *testing.T) {
		cases := []string{
			"Before [Tencent Meeting](/apps/private-sample).\n```\n[Tencent Meeting](/apps/private-sample)\n::app[private-sample]\n```\nAfter [x](/zh/apps/private-sample).",
			"Use `[Tencent Meeting](/apps/private-sample)` literally, then [x](/apps/private-sample).",
			"Use ``[Tencent Meeting](/apps/private-sample)`` literally, then [x](/apps/private-sample).",
			"Before [x](/apps/private-sample)\n\n    [x](/apps/private-sample)\n    https://opennavo.com/apps/private-sample\n    ::app[private-sample]\n\nAfter [y](/apps/private-sample)",
			"> ```\n> [x](/apps/private-sample)\n> https://opennavo.com/apps/private-sample\n> ::app[private-sample]\n> ```\n\nAfter [y](/apps/private-sample)",
			"`multi\n[x](/apps/private-sample)\n::app[private-sample]\nline` and [y](/apps/private-sample)",
		}
		for _, body := range cases {
			got := RedactPrivateAppReferences(body, private)
			if strings.Contains(body, "[Tencent Meeting](/apps/private-sample)") {
				require.Contains(t, got, "[Tencent Meeting](/apps/private-sample)", "code should keep literals: %q", body)
			}
			if strings.Contains(body, "    [x](/apps/private-sample)") {
				require.Contains(t, got, "[x](/apps/private-sample)")
			}
			html := renderArticleHTML(got)
			require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*/apps/private-sample`, stripCodeHTML(html))
		}
	})

	t.Run("autolinks linkified uppercase and zero-padded ports", func(t *testing.T) {
		cases := []string{
			"See <https://opennavo.com/apps/private-sample> and <https://www.opennavo.com/ja/apps/private-alias?tab=releases#top> for details.",
			"Read https://opennavo.com/apps/private-sample, https://www.opennavo.com/zh/apps/private-alias. Then open opennavo.com/apps/private-sample for the rest.",
			"HTTPS://opennavo.com/apps/private-sample and https://OPENNAVO.COM/apps/private-sample end",
			"HTTPS://opennavo.com/apps/private-sample.",
			"https://OPENNAVO.COM/apps/private-sample.",
			"https://opennavo.com:0443/apps/private-sample end",
			"https://opennavo.com:0443/apps/private-sample.",
		}
		for _, body := range cases {
			got := RedactPrivateAppReferences(body, private)
			assertRedacted(t, body, got)
			html := renderArticleHTML(got)
			require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*opennavo\.com[^"]*private-sample`, html)
		}
	})

	t.Run("encoding entities ports and escapes", func(t *testing.T) {
		cases := []string{
			"[Secure](https://opennavo.com:443/apps/private-sample) [Plain](http://www.opennavo.com:80/apps/private-alias)",
			"[Encoded](/%61pps/private-sample) and [Absolute](https://opennavo.com/%61pps/private-sample)",
			"[Entity](/apps/&#112;rivate-sample) and [Hex](/apps/&#x70;rivate-sample)",
			"[Escaped](/apps/private\\-sample) and [Host](https://opennavo\\.com/apps/private-sample)",
			"[Upper](HTTPS://OPENNAVO.COM:443/apps/private-sample)",
			"> [x](https://opennavo.com/apps/private-sample\n> \"title\")",
			"> [x](https://opennavo.com/apps/&#112;rivate-sample\n> \"title\")",
			"![Image](https://opennavo.com/apps/private-sample/icon.png)",
			"![x](/apps/&#112;rivate-sample \"title\")",
			"[Use `tool`](/apps/private-sample)",
			"[**Tencent** Meeting](/apps/private-sample)",
		}
		for _, body := range cases {
			got := RedactPrivateAppReferences(body, private)
			assertRedacted(t, body, got)
		}
		got := RedactPrivateAppReferences("[**Tencent** Meeting](/apps/private-sample)", private)
		require.Contains(t, got, "Tencent")
		require.Contains(t, got, "Meeting")
	})

	t.Run("reference definitions and duplicates", func(t *testing.T) {
		cases := []string{
			"[x][p]\n\n[p]:\n  /apps/private-sample\n",
			"[x][p]\n\n> [p]:\n>   /apps/private-sample\n",
			"[x][p]\n\n> [p]: /apps/private-sample\n",
			"[x][p]\n\n- [p]: /apps/private-sample\n",
			"intro\n[p]: /apps/public-child\n\n[x][p]\n\n[p]: /apps/private-sample\n",
			"[public][p]\n\n[p]: /apps/public-child\n[p]: /apps/private-sample\n",
			"[public][p]\n\n[p]: /apps/private-sample\n[p]: /apps/public-child\n",
			"[Tencent Meeting][tm] and [Alias][] and [tm].\n\n[tm]: https://opennavo.com/apps/private-sample\n[Alias]: /ja/apps/private-alias\n",
			"[public](/apps/public-child) \\\\[private](/apps/private-sample)",
		}
		for _, body := range cases {
			got := RedactPrivateAppReferences(body, private)
			html := renderArticleHTML(got)
			require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*/apps/private-(sample|alias)`, stripCodeHTML(html), "body=%q got=%q", body, got)
		}
		got := RedactPrivateAppReferences("[public][p]\n\n[p]: /apps/public-child\n[p]: /apps/private-sample\n", private)
		require.Contains(t, got, "/apps/public-child")
		require.NotContains(t, got, "/apps/private-sample")
	})

	t.Run("raw HTML anchors keep text; data-href left alone", func(t *testing.T) {
		got := RedactPrivateAppReferences(
			`Raw <a href="https://opennavo.com/apps/private-sample" class="x">Tencent Meeting</a> and <a href='/apps/public-child'>Public child</a>.`,
			private,
		)
		require.Contains(t, got, "Tencent Meeting")
		require.Contains(t, got, `href='/apps/public-child'`)
		require.NotContains(t, got, "opennavo.com/apps/private-sample")

		got = RedactPrivateAppReferences(`<a data-href="/apps/private-sample" href="/apps/public-child">Public</a>`, private)
		require.Contains(t, got, `data-href="/apps/private-sample"`)
		require.Contains(t, got, `href="/apps/public-child"`)

		got = RedactPrivateAppReferences(`Raw <a href="/apps/&#112;rivate-sample">Private</a>.`, private)
		require.Equal(t, "Raw Private.", strings.TrimSpace(got))
	})

	t.Run("app directive lines", func(t *testing.T) {
		got := RedactPrivateAppReferences("Before\n::app[private-sample]\nAfter\n::app[private-sample]", private)
		require.NotContains(t, got, "::app[private-sample]")
		require.Contains(t, got, "Before")
		require.Contains(t, got, "After")
	})

	t.Run("codex: code delimiter and NBSP not destroyed", func(t *testing.T) {
		both := map[string]bool{"private-sample": true, "private-alias": true}
		body := "`https://opennavo.com/apps/private-sample suffix`/apps/private-alias"
		got := RedactPrivateAppReferences(body, both)
		require.Contains(t, got, "`")
		require.Contains(t, got, "https://opennavo.com/apps/private-sample suffix")
		html := renderArticleHTML(got)
		require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*private-alias`, html)

		body = "Keep\u00a0/apps/private-sample\u00a0for\u00a0reference"
		got = RedactPrivateAppReferences(body, both)
		require.Contains(t, got, "Keep")
		require.Contains(t, got, "reference")
		require.NotContains(t, got, "/apps/private-sample")
	})
}

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
	require.NotContains(t, got, "::app[private-sample]")
	require.Contains(t, got, "Tencent Meeting")
	require.Contains(t, got, "HTTP Meeting")
	require.Contains(t, got, "WWW Meeting")
	require.Contains(t, got, "[Public child](/apps/public-child)")
	require.Contains(t, got, "::app[public-child]")
	require.Contains(t, got, "[public]: https://opennavo.com/apps/public-child")
	html := renderArticleHTML(got)
	require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*private-sample`, stripCodeHTML(html))
	require.Contains(t, html, `href="/apps/public-child"`)
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
			want: []string{"private-sample"},
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

func TestRedactPrivateAppHTMLEncodingBypass(t *testing.T) {
	private := map[string]bool{"private-sample": true, "private-alias": true}
	cases := []struct {
		name string
		html string
	}{
		{"percent-encoded slash path", `<p><a href="/%2Fapps%2Fprivate-sample">x</a></p>`},
		{"percent-encoded apps segment", `<p><a href="/%61pps/private-sample">x</a></p>`},
		{"entity in href", `<p><a href="/apps/&#112;rivate-sample">x</a></p>`},
		{"absolute percent-encoded", `<p><a href="https://opennavo.com/%2Fapps%2Fprivate-sample">x</a></p>`},
		{"image src", `<p><img src="/apps/private-sample/icon.png" alt="x"></p>`},
		{"data-href untouched", `<p><a data-href="/apps/private-sample" href="/apps/public-child">Public</a></p>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactPrivateAppHTML(c.html, private)
			if strings.Contains(c.name, "data-href") {
				require.Contains(t, got, `data-href="/apps/private-sample"`)
				require.Contains(t, got, `href="/apps/public-child"`)
				return
			}
			require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*private-sample`, got)
			require.NotRegexp(t, `(?i)<img[^>]+src="[^"]*private-sample`, got)
			if strings.Contains(c.html, "<a ") {
				require.Contains(t, got, "x")
			}
		})
	}
}

func TestRedactPrivateAppReferencesReviewProbes(t *testing.T) {
	private := map[string]bool{"private-sample": true}

	t.Run("H1 tilde fence with embedded backticks stays inert code", func(t *testing.T) {
		body := "~~~\n```\n[private](/apps/private-sample)\n```\n~~~\n\n[z](/apps/private-sample)"
		got := RedactPrivateAppReferences(body, private)
		require.Contains(t, got, "~~~")
		require.Contains(t, got, "[private](/apps/private-sample)", "code fence must keep the literal")
		require.NotContains(t, got, "[z](/apps/private-sample)", "outside-fence link must be unwrapped")
		require.Contains(t, got, "z")
		// Closing fence must still be tildes so the embedded ``` cannot reopen the link.
		require.Regexp(t, `(?m)^~~~\s*$`, got)
		html := renderArticleHTMLBoth(t, got)
		require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*/apps/private-sample`, stripCodeHTML(html))
	})

	t.Run("H1 four-backtick fence preserves length", func(t *testing.T) {
		body := "````\n```\n[private](/apps/private-sample)\n```\n````\n\n[z](/apps/private-sample)"
		got := RedactPrivateAppReferences(body, private)
		require.Contains(t, got, "````")
		require.Contains(t, got, "[private](/apps/private-sample)")
		html := renderArticleHTMLBoth(t, got)
		require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*/apps/private-sample`, stripCodeHTML(html))
	})

	t.Run("H2 nested private raw HTML anchors do not panic", func(t *testing.T) {
		body := `pre <a href="/apps/private-sample"><a href="/apps/private-sample">x</a></a> post`
		require.NotPanics(t, func() {
			got := RedactPrivateAppReferences(body, private)
			require.Equal(t, "pre x post", strings.TrimSpace(got))
			require.NotContains(t, got, "/apps/private-sample")
		})
	})

	t.Run("H3 standalone scheme-less URL is scrubbed", func(t *testing.T) {
		for _, body := range []string{
			"opennavo.com/apps/private-sample",
			"www.opennavo.com/apps/private-sample",
			"opennavo.com./apps/private-sample",
		} {
			got := RedactPrivateAppReferences(body, private)
			require.NotContains(t, got, "private-sample", "body=%q got=%q", body, got)
			require.NotContains(t, got, "/apps/", "body=%q got=%q", body, got)
			html := renderArticleHTMLBoth(t, got)
			require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*private-sample`, html)
		}
	})

	t.Run("M1 external query embed is not truncated", func(t *testing.T) {
		body := "See example.com/?next=/apps/private-sample for more."
		got := RedactPrivateAppReferences(body, private)
		require.Equal(t, body, got)
	})

	t.Run("M2 nested private image inside private link is removed", func(t *testing.T) {
		body := "[![x](/apps/private-sample/icon.png)](/apps/private-sample)"
		got := RedactPrivateAppReferences(body, private)
		require.NotContains(t, got, "/apps/private-sample")
		require.NotContains(t, got, "![")
		html := renderArticleHTMLBoth(t, got)
		require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*private-sample`, html)
		require.NotRegexp(t, `(?i)<img[^>]+src="[^"]*private-sample`, html)
	})

	t.Run("M3 protocol-relative and trailing-dot hosts are redacted", func(t *testing.T) {
		for _, body := range []string{
			"[x](//opennavo.com/apps/private-sample)",
			"[x](https://opennavo.com./apps/private-sample)",
			"[x](https://www.opennavo.com./zh/apps/private-sample)",
		} {
			got := RedactPrivateAppReferences(body, private)
			require.Contains(t, got, "x")
			require.NotContains(t, got, "/apps/private-sample", "body=%q got=%q", body, got)
			require.NotContains(t, got, "//opennavo.com", "body=%q got=%q", body, got)
		}
	})

	t.Run("M4 nested lists and list HTML are not flattened", func(t *testing.T) {
		nested := "- one\n  - two\n  - three\n\n[z](/apps/private-sample)"
		got := RedactPrivateAppReferences(nested, private)
		require.Contains(t, got, "- one")
		require.Contains(t, got, "  - two")
		require.Contains(t, got, "  - three")
		require.NotContains(t, got, "](/apps/private-sample)")

		withHTML := "- item\n  <div>x</div>\n\n[z](/apps/private-sample)"
		got = RedactPrivateAppReferences(withHTML, private)
		require.Contains(t, got, "- item")
		require.Contains(t, got, "<div>x</div>")
		// Must not duplicate the item text the way the old default branch did.
		require.Equal(t, 1, strings.Count(got, "item"))
	})
}

func TestRedactPrivateAppReferencesReviewRound2(t *testing.T) {
	private := map[string]bool{"private-sample": true}

	t.Run("N1 headings breaks and definitions inside list items survive", func(t *testing.T) {
		body := "- item\n\n  # heading\n\n  ***\n\n  [r]: /docs\n\n  see [r]\n- next\n\n[z](/apps/private-sample)"
		got := RedactPrivateAppReferences(body, private)
		require.Contains(t, got, "  # heading")
		require.Contains(t, got, "  ***")
		require.Contains(t, got, "  [r]: /docs")
		require.Contains(t, got, "- next")
		html := renderArticleHTMLBoth(t, got)
		require.Contains(t, html, "<h1>heading</h1>")
		require.Contains(t, html, "<hr")
		require.Contains(t, html, `href="/docs"`)
	})

	t.Run("N2 dot segments are resolved before matching", func(t *testing.T) {
		got := RedactPrivateAppReferences("[x](/apps/public-child/../private-sample) [y](/apps/private-sample/../public-child) [w](/apps\\private-sample)", private)
		require.NotContains(t, got, "public-child/../private-sample")
		require.Contains(t, got, "[y](/apps/private-sample/../public-child)")
		require.NotContains(t, got, "](/apps\\private-sample)")
	})

	t.Run("N3 encoded and escaped plain-text URLs are scrubbed", func(t *testing.T) {
		for _, body := range []string{
			"a opennavo.com/apps/private%2Dsample b",
			"a opennavo.com/apps/private&#45;sample b",
			"a opennavo.com/apps/private\\-sample b",
			"a /apps/private&#x2d;sample b",
		} {
			got := RedactPrivateAppReferences(body, private)
			require.NotContains(t, got, "private", "body=%q got=%q", body, got)
			require.Contains(t, got, "a ")
		}
		keep := "a opennavo.com/apps/public&#45;child b"
		require.Equal(t, keep, RedactPrivateAppReferences(keep, private))
	})

	t.Run("low: raw img, port and www trailing-dot text", func(t *testing.T) {
		got := RedactPrivateAppReferences(`<img src="/apps/private-sample/i.png"> <img src="/apps/public-child/i.png">`, private)
		require.NotContains(t, got, "private-sample")
		require.Contains(t, got, `src="/apps/public-child/i.png"`)
		got = RedactPrivateAppReferences("x www.opennavo.com./apps/private-sample and opennavo.com:443/apps/private-sample y", private)
		require.NotContains(t, got, "private-sample")
		require.Contains(t, got, " y")
	})
}

func TestRedactPrivateAppReferencesReviewRound3(t *testing.T) {
	// The private map is built from AppReferences, as catalog/articles.go does in production.
	redact := func(body string) string {
		private := map[string]bool{}
		for _, token := range AppReferences(body) {
			if strings.HasPrefix(token, "private-") {
				private[token] = true
			}
		}
		return RedactPrivateAppReferences(body, private)
	}
	noLive := func(t *testing.T, got string) {
		t.Helper()
		html := renderArticleHTMLBoth(t, got)
		require.NotRegexp(t, `(?i)<a[^>]+href="[^"]*private-sample`, html, "got=%q", got)
		require.NotRegexp(t, `(?i)<(img|source)[^>]+src(set)?="[^"]*private-sample`, html, "got=%q", got)
	}

	t.Run("F1 encoded and ideographic host dots", func(t *testing.T) {
		for _, body := range []string{
			"[x](https://opennavo%2Ecom/apps/private-sample)",
			"![i](https://opennavo%2ecom/apps/private-sample/i.png)",
			"see https://opennavo%2Ecom/apps/private-sample now",
			"[x](https://opennavo\u3002com/apps/private-sample)",
			"see opennavo\u3002com/apps/private-sample now",
			"see www\uff0eopennavo\uff61com/apps/private-sample now",
			"[x][r]\n\n[r]: https://opennavo%2Ecom/apps/private-sample",
		} {
			got := redact(body)
			require.NotContains(t, got, "private-sample", "body=%q", body)
			noLive(t, got)
		}
		keep := "[p](https://opennavo%2Ecom/apps/public-child)"
		require.Equal(t, keep, redact(keep))
	})

	t.Run("F2 discovery decodes text and accepts default ports", func(t *testing.T) {
		require.Contains(t, AppReferences("opennavo.com/apps/private&#45;sample"), "private-sample")
		require.Contains(t, AppReferences("opennavo.com:443/apps/private-sample"), "private-sample")
		require.Contains(t, AppReferences("opennavo.com/apps/private\\-sample"), "private-sample")
		for _, body := range []string{
			"see opennavo.com/apps/private&#45;sample now",
			"see opennavo.com:443/apps/private-sample now",
			"see opennavo.com&#47;apps/private-sample now",
		} {
			got := redact(body)
			require.NotContains(t, got, "private", "body=%q", body)
			noLive(t, got)
		}
	})

	t.Run("low: srcset source and star-bullet thematic break", func(t *testing.T) {
		pv := map[string]bool{"private-sample": true}
		got := RedactPrivateAppReferences(`<picture><source srcset="/apps/private-sample/a.png 2x"><img srcset="/x.png 1x, /apps/private-sample/b.png 2x" src="/x.png"></picture> <img srcset="/apps/public-child/c.png">`, pv)
		require.NotContains(t, got, "private-sample")
		require.Contains(t, got, "/apps/public-child/c.png")
		got = RedactPrivateAppReferences("* item\n\n  ***\n* next\n\n[z](/apps/private-sample)", pv)
		require.NotContains(t, renderArticleHTML(got), "<hr>\n<ul>")
		require.Contains(t, renderArticleHTML(got), "<li>")
	})
}

// renderArticleHTMLBoth renders with goldmark and, when available, production markdown-it
// (html:false, linkify:true, fuzzyLink:true) so scheme-less private URLs are exercised.
func renderArticleHTMLBoth(t *testing.T, source string) string {
	t.Helper()
	gold := renderArticleHTML(source)
	if mdit := renderArticleHTMLMarkdownIt(t, source); mdit != "" {
		return gold + "\n" + mdit
	}
	return gold
}

func renderArticleHTML(source string) string {
	md := goldmark.New(goldmark.WithExtensions(extension.Linkify))
	var b strings.Builder
	if err := md.Convert([]byte(source), &b); err != nil {
		return ""
	}
	return b.String()
}

// renderArticleHTMLMarkdownIt renders with the production renderer (markdown-it, html:false,
// linkify:true) plus fuzzyLink so scheme-less URLs are exercised. It never fails silently:
// missing node/markdown-it is printed once to stderr and becomes a hard failure when
// OPENNAVO_REQUIRE_MARKDOWN_IT=1 or CI is set; a render error is always a test failure.
func renderArticleHTMLMarkdownIt(t *testing.T, source string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("markdown-it oracle: cannot locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	_, nodeErr := exec.LookPath("node")
	_, modErr := os.Stat(filepath.Join(root, "node_modules", "markdown-it"))
	if nodeErr != nil || modErr != nil {
		msg := fmt.Sprintf("markdown-it oracle unavailable (node: %v, module: %v); run pnpm install", nodeErr, modErr)
		if os.Getenv("OPENNAVO_REQUIRE_MARKDOWN_IT") == "1" || os.Getenv("CI") != "" {
			t.Fatal(msg)
		}
		markdownItWarn.Do(func() { fmt.Fprintln(os.Stderr, "WARNING: SKIPPING "+msg) })
		return ""
	}
	cmd := exec.Command("node", "-e", `const MarkdownIt = require('markdown-it');
const md = new MarkdownIt({ html: false, linkify: true });
// fuzzyLink is stricter than production (default false): scheme-less hosts become links too,
// so anything that passes here also passes the real renderer.
md.linkify.set({ fuzzyLink: true });
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', c => input += c);
process.stdin.on('end', () => { process.stdout.write(md.render(input)); });`)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("markdown-it oracle failed: %v\n%s", err, out)
	}
	return string(out)
}

var markdownItWarn sync.Once

func stripCodeHTML(html string) string {
	for {
		start := strings.Index(strings.ToLower(html), "<code")
		if start < 0 {
			break
		}
		end := strings.Index(strings.ToLower(html[start:]), "</code>")
		if end < 0 {
			html = html[:start]
			break
		}
		html = html[:start] + html[start+end+len("</code>"):]
	}
	for {
		start := strings.Index(strings.ToLower(html), "<pre")
		if start < 0 {
			break
		}
		end := strings.Index(strings.ToLower(html[start:]), "</pre>")
		if end < 0 {
			html = html[:start]
			break
		}
		html = html[:start] + html[start+end+len("</pre>"):]
	}
	return html
}
