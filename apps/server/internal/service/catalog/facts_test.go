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

func TestRepositoryFactCarriesGitHubSignals(t *testing.T) {
	checked := time.Date(2026, 10, 30, 3, 35, 0, 0, time.UTC)
	pushed := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	stars := int64(1234)
	raw, err := json.Marshal(publicapi.RepositoryFact{
		Url: "https://github.com/owner/app", Stars: &stars, PushedAt: &pushed, Archived: true, SignalsCheckedAt: &checked,
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"url":"https://github.com/owner/app","stars":1234,"pushedAt":"2026-10-29T08:00:00Z","archived":true,"signalsCheckedAt":"2026-10-30T03:35:00Z"}`, string(raw))
}
