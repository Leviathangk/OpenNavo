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
			name: "other paths hosts and syntax untouched",
			body: "[Other](/packages/private-sample) [External](https://example.com/apps/private-sample) [HTTP](http://opennavo.com/apps/private-sample) [Reference][ref] ![Image](/apps/private-sample)\n[ref]: /apps/private-sample",
			want: "[Other](/packages/private-sample) [External](https://example.com/apps/private-sample) [HTTP](http://opennavo.com/apps/private-sample) [Reference][ref] ![Image](/apps/private-sample)\n[ref]: /apps/private-sample",
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
