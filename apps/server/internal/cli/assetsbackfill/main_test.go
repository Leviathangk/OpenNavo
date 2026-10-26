package assetsbackfill

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackfillArguments(t *testing.T) {
	defaults, err := parseOptions(nil, io.Discard)
	require.NoError(t, err)
	require.False(t, defaults.Apply)
	require.Equal(t, float64(2), defaults.Rate)
	require.Equal(t, 1, defaults.Concurrency)
	for _, args := range [][]string{{"--apply"}, {"--dry-run"}, {"--apply", "--dry-run=false", "--kind", "icon", "--limit", "10", "--rate", "0.5", "--concurrency", "2"}} {
		_, err := parseOptions(args, io.Discard)
		require.NoError(t, err)
	}
	for _, args := range [][]string{{"--dry-run=false"}, {"--apply", "--dry-run"}, {"--kind", "cover"}, {"--limit", "-1"}, {"--rate", "0"}, {"--rate", "NaN"}, {"--rate", "+Inf"}, {"--concurrency", "0"}, {"--concurrency", "9"}, {"extra"}} {
		_, err := parseOptions(args, io.Discard)
		require.Error(t, err, "%v", args)
	}
	var output bytes.Buffer
	require.Equal(t, 0, run([]string{"--help"}, &output, &output))
	require.Contains(t, output.String(), "dry-run")
	require.Equal(t, 2, run([]string{"--kind", "cover"}, &output, &output))
}
