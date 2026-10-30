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
			name: "reference links and definitions",
			body: "[Tencent Meeting][tm] and [Alias][] and [tm].\n[tm]: https://opennavo.com/apps/private-sample\n[Alias]: /ja/apps/private-alias",
			want: "Tencent Meeting and Alias and tm.\n",
		},
		{
			name: "http reference links and definitions",
			body: "[Tencent Meeting][tm] and [Alias][] and [tm].\n[tm]: http://opennavo.com/apps/private-sample\n[Alias]: http://www.opennavo.com/ja/apps/private-alias",
			want: "Tencent Meeting and Alias and tm.\n",
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
			want: "[Other](/packages/private-sample) [External](https://example.com/apps/private-sample) ![Image](https://opennavo.com/apps/private-sample/icon.png)",
		},
		{
			name: "formatted link text kept",
			body: "[**Tencent** Meeting](/apps/private-sample)",
			want: "**Tencent** Meeting",
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
