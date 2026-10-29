package urlnotify

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentGatePage(t *testing.T) {
	for _, tc := range []struct {
		path, token, kind string
	}{
		{"/apps/x", "x", "overview"},
		{"/ja/apps/x/versions", "x", "versions"},
		{"/apps/x@2/versions", "x@2", "versions"},
		{"/zh-Hant/apps/x", "", ""}, // Locale URL prefixes come from the site registry.
		{"/apps/x/alternatives", "x", "overview"},
		{"/apps/x/uninstall", "x", "overview"},
		{"/apps/x/troubleshooting", "x", "overview"},
		{"/apps/x/details", "", ""},
		{"/apps/x/dependencies", "", ""},
		{"/apps/x/overview", "", ""},
		{"/apps/x/versions/2", "", ""},
		{"/apps/x/", "", ""},
		{"/apps/", "", ""},
		{"/articles/x", "", ""},
		{"apps/x", "", ""},
		{"//apps/x", "", ""},
		{"/xx/apps/x", "", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			token, kind, ok := contentGatePage(tc.path)
			require.Equal(t, tc.token != "", ok)
			require.Equal(t, tc.token, token)
			require.Equal(t, tc.kind, kind)
		})
	}
}
