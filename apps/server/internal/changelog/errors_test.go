package changelog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFailureCategorizesSourceProblems(t *testing.T) {
	for kind, want := range map[string]string{
		"invalid_source_config":   "invalid_source_config",
		"unsupported_source_type": "unsupported_source_type",
		"source_not_fetchable":    "source_not_fetchable",
		"response_read_failed":    "response_read_failed",
	} {
		err := &Error{Kind: kind}
		category, status := Failure(err)
		require.Equal(t, want, category)
		require.Zero(t, status)
		require.NotEmpty(t, err.Error())
	}
}
