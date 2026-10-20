//go:build integration

package store

// Test the fact-specific portion independently of other teams' package relationships.
var MergeFactRelationshipsForTest = mergeFactRelationships
