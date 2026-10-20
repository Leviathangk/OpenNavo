//go:build integration

package management

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/opennavo/opennavo/server/internal/about"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestAboutConfigSaveWithoutAutomaticTranslationAndRejectsUnsafeLinks(t *testing.T) {
	st := testutil.NewFactsStore(t)
	svc := Service{Store: st}
	raw, err := os.ReadFile("../../../seeds/about.json")
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(raw, &value))
	_, err = svc.Execute(context.Background(), "UpdateAppConfig", Input{Key: about.Key, Body: map[string]any{"value": value, "description": "about"}})
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='translate:content'").Count(&count).Error)
	require.Zero(t, count, "S8 removes background translation of About configuration")
	value["modules"].([]any)[0].(map[string]any)["url"] = "javascript:alert(1)"
	_, err = svc.Execute(context.Background(), "UpdateAppConfig", Input{Key: about.Key, Body: map[string]any{"value": value}})
	require.Error(t, err)
	var saved string
	require.NoError(t, st.DB.Raw("SELECT value::text FROM app_config WHERE key=?", about.Key).Scan(&saved).Error)
	_, err = about.Parse([]byte(saved))
	require.NoError(t, err)
}
