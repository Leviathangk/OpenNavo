package seo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRobotsIntent(t *testing.T) {
	indexable := RobotsIntent{Index: true, Follow: true}
	cases := []struct {
		name    string
		meta    map[string]string
		headers []string
		want    RobotsIntent
	}{
		{name: "no directives", want: indexable},
		// The @nuxtjs/robots default on indexable article pages.
		{name: "preview controls", meta: map[string]string{"robots": "index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1"}, want: indexable},
		{name: "meta noindex", meta: map[string]string{"robots": "noindex, follow"}, want: RobotsIntent{Follow: true}},
		{name: "none", meta: map[string]string{"robots": "none"}, want: RobotsIntent{}},
		{name: "case and spacing", meta: map[string]string{"ROBOTS": " NoIndex ,NOFOLLOW "}, want: RobotsIntent{}},
		{name: "googlebot meta", meta: map[string]string{"robots": "index, follow", "googlebot": "noindex"}, want: RobotsIntent{Follow: true}},
		{name: "other crawler meta", meta: map[string]string{"otherbot": "noindex, nofollow"}, want: indexable},
		{name: "index does not undo noindex", meta: map[string]string{"robots": "noindex"}, headers: []string{"index, follow, all"}, want: RobotsIntent{Follow: true}},
		{name: "header noindex", meta: map[string]string{"robots": "index, follow"}, headers: []string{"noindex"}, want: RobotsIntent{Follow: true}},
		{name: "header bingbot scope", headers: []string{"bingbot: noindex, nofollow"}, want: RobotsIntent{}},
		{name: "header other scope", headers: []string{"otherbot: noindex, nofollow"}, want: indexable},
		{name: "scope switches", headers: []string{"otherbot: noindex, googlebot: nofollow"}, want: RobotsIntent{Index: true}},
		{name: "valued directive is no scope", headers: []string{"unavailable_after: Wednesday, 25-Jun-2030 15:00:00 PST, max-snippet: 20"}, want: indexable},
		{name: "valued directive keeps generic scope", headers: []string{"max-image-preview: large, noindex"}, want: RobotsIntent{Follow: true}},
		{name: "date fragment keeps generic scope", headers: []string{"unavailable_after: Wednesday, 25-Jun-2030 15:00:00 PST, noindex"}, want: RobotsIntent{Follow: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r Robots
			for name, content := range tc.meta {
				r.Meta(name, content)
			}
			for _, value := range tc.headers {
				r.Header(value)
			}
			require.Equal(t, tc.want, r.Intent())
		})
	}
}

func TestDecisionRobots(t *testing.T) {
	require.Equal(t, RobotsIntent{Index: true, Follow: true}, DecisionRobots(IndexableRobots))
	require.Equal(t, RobotsIntent{Index: true, Follow: true}, DecisionRobots("index, follow"))
	require.Equal(t, RobotsIntent{Follow: true}, DecisionRobots("noindex, follow"))
	require.Equal(t, "noindex, follow", DecisionRobots("noindex,follow").String())
	require.Equal(t, "index, nofollow", RobotsIntent{Index: true}.String())
}
