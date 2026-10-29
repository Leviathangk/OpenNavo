package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/stretchr/testify/require"
)

func TestOpenSourceFactOmitsMissingGitHubSignals(t *testing.T) {
	raw, err := json.Marshal(publicapi.OpenSourceFact{
		Repo: "owner/app", Url: "https://github.com/owner/app", License: "MIT", CheckedAt: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"repo":"owner/app","url":"https://github.com/owner/app","license":"MIT","archived":false,"checkedAt":"2026-10-30T00:00:00Z"}`, string(raw))
}
