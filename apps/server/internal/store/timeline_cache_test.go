package store

import (
	"fmt"
	"sync"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/stretchr/testify/require"
)

func TestNoInformationCacheBoundsRevisionsAndEvictsOldHashes(t *testing.T) {
	noInformationSeen.Purge()
	t.Cleanup(noInformationSeen.Purge)
	placeholder := map[string]any{"summary": "Official release notes are unavailable."}
	original := content.Hash("en-US", placeholder)
	require.True(t, cachedNoInformation(original, placeholder))
	for revision := 0; revision < noInformationCacheCapacity; revision++ {
		source := map[string]any{"summary": fmt.Sprintf("Fixed crash %d on launch.", revision)}
		require.False(t, cachedNoInformation(content.Hash("en-US", source), source))
	}
	require.Equal(t, noInformationCacheCapacity, noInformationSeen.Len())
	require.False(t, noInformationSeen.Contains(original), "old revisions must be evicted")
	// An evicted source can be classified again, without exceeding the bound.
	require.True(t, cachedNoInformation(original, placeholder))
	for revision := 0; revision < noInformationCacheCapacity; revision++ {
		require.True(t, cachedNoInformation(original, placeholder))
		source := map[string]any{"summary": fmt.Sprintf("Added export format %d.", revision)}
		require.False(t, cachedNoInformation(content.Hash("en-US", source), source))
	}
	require.True(t, noInformationSeen.Contains(original), "frequently read sources stay cached")
	require.Equal(t, noInformationCacheCapacity, noInformationSeen.Len())
}

func TestNoInformationCacheConcurrentReadsAndRevisions(t *testing.T) {
	noInformationSeen.Purge()
	t.Cleanup(noInformationSeen.Purge)
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for revision := 0; revision < 600; revision++ {
				source := map[string]any{"summary": fmt.Sprintf("Fixed crash %d in module %d.", revision, worker)}
				if cachedNoInformation(content.Hash("en-US", source), source) {
					t.Error("substantive notes classified as unavailable")
				}
			}
		}()
	}
	workers.Wait()
	require.Equal(t, noInformationCacheCapacity, noInformationSeen.Len())
}
