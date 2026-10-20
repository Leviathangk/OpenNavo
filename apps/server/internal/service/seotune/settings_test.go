package seotune

import (
	"encoding/json"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAutomationRejectsUnknownActionsAndUnsafeThresholds(t *testing.T) {
	settings := Settings{Version: 1, Actions: map[string]agentgate.Policy{}, DailyEntities: map[string]int{}, ResearchDailyMicros: 15000000, ResearchMonthlyMicros: 100000000, BusinessDropRatio: 0.5}
	for _, action := range Actions {
		settings.Actions[action] = agentgate.Policy{Enabled: true, Mode: "proposal"}
		settings.DailyEntities[action] = 10
	}
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	_, err = Decode(raw)
	require.NoError(t, err)
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) {
			v["actions"].(map[string]any)["unregistered_action"] = map[string]any{"enabled": true, "mode": "live"}
		},
		func(v map[string]any) { v["businessDropRatio"] = 0 },
		func(v map[string]any) { v["researchMonthlyMicros"] = 1 },
		func(v map[string]any) { v["dailyEntities"].(map[string]any)["listing"] = -1 },
		func(v map[string]any) { v["bypassEvidence"] = true },
	} {
		var v map[string]any
		require.NoError(t, json.Unmarshal(raw, &v))
		mutate(v)
		invalid, _ := json.Marshal(v)
		_, err = Decode(invalid)
		require.Error(t, err)
	}
}
