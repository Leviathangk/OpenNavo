//go:build integration

package management

import (
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestClaimSeoTasksKeepsTranslationsWhenIndexingClosed(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := hermesContext("seo:task:handle")
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US"]}'::jsonb)`).Error)
	svc := &Service{Store: st}
	tasks := &seotasks.Service{DB: st.DB}
	for _, locale := range []string{"de-DE", "fr-FR", "it-IT", "pt-BR", "zh-CN"} {
		task, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "translate", EntityKey: "category:1", Locale: locale, DedupeKey: "closed-translation:" + locale, RuleVersion: "fixture"})
		require.NoError(t, err)
		result, err := svc.seoTaskControl(ctx, "ClaimSeoTasks", Input{Body: map[string]any{"taskIds": []int64{task.ID}, "limit": 1}})
		require.NoError(t, err)
		claimed := result.(map[string]any)["records"].([]seotasks.Task)
		require.Len(t, claimed, 1, locale)
		require.Equal(t, task.ID, claimed[0].ID)
		_, err = tasks.Complete(ctx, task.ID, claimed[0].Fencing, seotasks.Owner(1, 2), "skipped", "fixture complete")
		require.NoError(t, err)
	}
}
