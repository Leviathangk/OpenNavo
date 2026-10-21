package seede2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProductionRejectsBeforeConfigurationOrConnections(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DATABASE_URL", "invalid")
	require.ErrorContains(t, run(), "forbidden in prod")
}
