package articles

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func ptr[T any](value T) *T { return &value }

func TestFieldValidation(t *testing.T) {
	require.True(t, ValidSlug("best-screenshot-apps", 100))
	for _, slug := range []string{"", "Best", "a--b", "-a", "a b", strings.Repeat("a", 101)} {
		require.False(t, ValidSlug(slug, 100), slug)
	}
	require.True(t, ValidType("roundup"))
	require.False(t, ValidType("listicle"))

	text, err := Text(ptr("  Title  "), 10)
	require.NoError(t, err)
	require.Equal(t, "Title", *text)
	empty, err := Text(ptr("   "), 10)
	require.NoError(t, err)
	require.Nil(t, empty)
	none, err := Text(nil, 10)
	require.NoError(t, err)
	require.Nil(t, none)
	_, err = Text(ptr("too long text"), 5)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = Text(ptr("bad\x00"), 5)
	require.ErrorIs(t, err, ErrInvalid)

	body, err := Body(ptr("line\r\nnext\n"))
	require.NoError(t, err)
	require.Equal(t, "line\nnext", *body)
	for _, value := range []*string{nil, ptr("  ")} {
		out, err := Body(value)
		require.NoError(t, err)
		require.Nil(t, out)
	}
	_, err = Body(ptr(strings.Repeat("a", MaxBody+1)))
	require.ErrorIs(t, err, ErrInvalid)
	_, err = Body(ptr("bell\a"))
	require.ErrorIs(t, err, ErrInvalid)

	require.True(t, HTTPS("https://vendor.example/a"))
	for _, raw := range []string{"http://vendor.example", "https://u:p@vendor.example", "https://", "https://x/" + strings.Repeat("a", 2048), "::"} {
		require.False(t, HTTPS(raw), raw)
	}

	sources, err := Sources([]Source{{Title: " Pricing ", URL: " https://vendor.example/pricing "}})
	require.NoError(t, err)
	require.Equal(t, []Source{{Title: "Pricing", URL: "https://vendor.example/pricing"}}, sources)
	for _, in := range [][]Source{
		make([]Source, MaxSources+1),
		{{Title: "", URL: "https://vendor.example"}},
		{{Title: "A", URL: "http://vendor.example"}},
		{{Title: "A", URL: "https://vendor.example"}, {Title: "B", URL: "https://vendor.example"}},
	} {
		_, err := Sources(in)
		require.ErrorIs(t, err, ErrInvalid)
	}

	ids, err := Packages(nil)
	require.NoError(t, err)
	require.Equal(t, []int64{}, ids)
	ids, err = Packages([]int64{3, 1})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 1}, ids)
	for _, in := range [][]int64{{0}, {1, 1}, make([]int64, MaxPackages+1)} {
		_, err := Packages(in)
		require.ErrorIs(t, err, ErrInvalid)
	}
}

func TestScanBodyFindsEmbedsAndUnsafeContent(t *testing.T) {
	body := strings.Join([]string{
		"## Pick by task",
		"![Annotated capture](https://cdn.opennavo.example/a.png \"title\")",
		"See [docs](https://vendor.example/docs), [anchor](#pick), [local](/apps/shottr) and <https://vendor.example>.",
		"::app[shottr]",
		"::app[shottr]",
		"::app[cleanshot]",
		"```html",
		"<script>alert(1)</script> [x](javascript:alert(1)) ![](http://evil)",
		"```",
		"Inline `<kbd>` is code.",
		"Mail [us](mailto:hi@opennavo.example).",
	}, "\n")
	scan := ScanBody(body)
	require.Empty(t, scan.Problems)
	require.Equal(t, []Image{{URL: "https://cdn.opennavo.example/a.png", Alt: "Annotated capture"}}, scan.Images)
	require.Equal(t, []string{"shottr", "cleanshot"}, scan.AppCards)

	bad := ScanBody("<img src=x onerror=alert(1)>\n![](https://cdn.example/b.png)\n![x](http://cdn.example/c.png)\n[x](javascript:alert(1))\n<javascript:alert(1)>\n[y](//evil.example)\n[z](https://user:pw@evil.example)\n[w](%zz)")
	require.Equal(t, []string{ProblemRawHTML, ProblemImageAlt, ProblemImageHost, ProblemLink}, bad.Problems)

	many := strings.Repeat("![a](https://cdn.example/a.png)\n", MaxImages+1) + strings.Repeat("::app[x]\n", MaxAppCards+1)
	require.Equal(t, []string{ProblemImages, ProblemAppCards}, ScanBody(many).Problems)
}

func TestReadingMinutes(t *testing.T) {
	require.Equal(t, 1, ReadingMinutes(""))
	require.Equal(t, 2, ReadingMinutes(strings.Repeat("word ", 300)))
	require.Equal(t, 2, ReadingMinutes(strings.Repeat("截图", 300)))
	// Code does not count.
	require.Equal(t, 1, ReadingMinutes("```\n"+strings.Repeat("token ", 1000)+"\n```"))
}

func TestContentHashAndReadiness(t *testing.T) {
	c := Content{Type: "review", Title: "Shottr review", Summary: ptr("Fast"), Body: ptr("Body"), Packages: []int64{1}, Sources: []Source{}}
	same := c
	same.Packages = []int64{1}
	require.Equal(t, c.Hash(), same.Hash())
	changed := c
	changed.Title = "Shottr review, updated"
	require.NotEqual(t, c.Hash(), changed.Hash())

	require.Equal(t, []string{ProblemTestNote}, Readiness(c, Scan{}))
	c.TestNote = ptr("MacBook Pro (M4), macOS 26")
	require.Empty(t, Readiness(c, Scan{}))
	roundup := Content{Type: "roundup", CoverAssetID: ptr(int64(5))}
	require.Equal(t, []string{ProblemTitle, ProblemSummary, ProblemBody, ProblemRawHTML, ProblemSources, ProblemCoverAlt},
		Readiness(roundup, Scan{Problems: []string{ProblemRawHTML}}))
}
