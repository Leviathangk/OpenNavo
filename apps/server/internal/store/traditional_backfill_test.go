package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPacerSpacesEventsWithoutBursts(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	var slept []time.Duration
	now := func() time.Time { return clock }
	sleep := func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		clock = clock.Add(d)
		return nil
	}
	objects := pacer{interval: rateInterval(time.Second, 20)}
	for range 3 {
		require.NoError(t, objects.wait(ctx, now, sleep))
	}
	clock = clock.Add(10 * time.Millisecond) // time spent working counts toward the interval
	require.NoError(t, objects.wait(ctx, now, sleep))
	clock = clock.Add(time.Minute) // an idle minute is not saved up for a burst
	for range 2 {
		require.NoError(t, objects.wait(ctx, now, sleep))
	}
	require.Equal(t, []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 40 * time.Millisecond, 50 * time.Millisecond}, slept)

	require.Equal(t, 2*time.Second, rateInterval(time.Minute, 30))
	require.Equal(t, 2*time.Minute, rateInterval(time.Minute, 0.5))
	require.Zero(t, rateInterval(time.Second, 0))
	require.Zero(t, rateInterval(time.Second, -1))
	unlimited := pacer{}
	for range 3 {
		require.NoError(t, unlimited.wait(ctx, now, sleep))
	}
	require.Len(t, slept, 4)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, unlimited.wait(canceled, now, sleep), context.Canceled)
	start := time.Now()
	require.ErrorIs(t, sleepContext(canceled, time.Hour), context.Canceled)
	require.Less(t, time.Since(start), time.Second)
}

func TestTraditionalCandidatesDerivedAgain(t *testing.T) {
	for _, tc := range []struct {
		candidate traditionalCandidate
		pending   bool
	}{
		{traditionalCandidate{Derivable: true}, true},                                // never derived, or a receipt from another converter
		{traditionalCandidate{Derivable: true, Derived: true}, true},                 // derived by another converter
		{traditionalCandidate{Derivable: true, Derived: true, Current: true}, false}, // done; only checked
		{traditionalCandidate{Derived: true}, true},                                  // Traditional text to withdraw
		{traditionalCandidate{Derived: true, Current: true}, true},
		{traditionalCandidate{}, false}, // Simplified text not ready and nothing derived
	} {
		require.Equal(t, tc.pending, tc.candidate.pending(), "%+v", tc.candidate)
	}
}
