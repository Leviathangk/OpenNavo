package seo

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExperimentSampleAndDecisions(t *testing.T) {
	require.InDelta(t, 7750, ImpressionsPerArm(.01, .015), 20)
	for _, pair := range [][2]float64{{0, .01}, {.01, .01}, {.1, 1}, {math.NaN(), .02}} {
		require.Zero(t, ImpressionsPerArm(pair[0], pair[1]))
	}
	require.Equal(t, "continue", ExperimentOutcome(10, 500, 20, 500, 1000))
	require.Equal(t, "won", ExperimentOutcome(100, 10000, 200, 10000, 1000))
	require.Equal(t, "lost", ExperimentOutcome(200, 10000, 100, 10000, 1000))
	require.Equal(t, "continue", ExperimentOutcome(100, 10000, 105, 10000, 1000))
	require.Equal(t, ExperimentHash("fixture", "en-US:1:desktop", 42), ExperimentHash("fixture", "en-US:1:desktop", 42))
	require.NotEqual(t, ExperimentHash("fixture", "en-US:1:desktop", 42), ExperimentHash("fixture", "ja-JP:1:desktop", 42))
}
