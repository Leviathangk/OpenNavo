package management

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTapSiteTokenValidation(t *testing.T) {
	for _, token := range []any{"", "UPPER", "two words", "../orca", "a/b", "--flag", "a\n", "海豚", strings.Repeat("a", 129), nil, true, 42} {
		_, err := (&Service{}).readTapCandidate(context.Background(), Input{Body: map[string]any{"cask": "stablyai/orca/orca", "siteToken": token}})
		require.ErrorContains(t, err, "tap_site_token_invalid")
	}
}
