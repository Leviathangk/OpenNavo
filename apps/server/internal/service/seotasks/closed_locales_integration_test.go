//go:build integration

package seotasks_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestClaimKeepsWebsiteTranslationsInClosedLocales(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	insert := func(kind, entity, locale string) int64 {
		t.Helper()
		var id int64
		require.NoError(t, st.DB.Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,rule_version,state,priority) VALUES(?,?,?,?,'fixture','open',1) RETURNING id`,
			kind, entity, locale, kind+":"+entity+":"+locale).Scan(&id).Error)
		return id
	}
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US","zh-CN","ja-JP","ru-RU","es-ES"]}'::jsonb)`).Error)
	open := insert("translate", "package:1", "ja-JP")
	closed := insert("translate", "package:2", "de-DE")
	article := insert("translate", "article:3", "pt-BR")
	// Client-only content keeps every client language; other task kinds ignore the list.
	release := insert("translate", "desktop_release:4", "de-DE")
	announcement := insert("translate", "announcement:desktop.announcement", "fr-FR")
	maintenance := insert("maintenance", "pkg:5", "it-IT")
	filter := seotasks.ClaimFilter{Limit: 10}
	svc := &seotasks.Service{DB: st.DB}
	claimed, err := svc.ClaimWith(ctx, "translator", filter)
	require.NoError(t, err)
	ids := []int64{}
	for _, task := range claimed {
		ids = append(ids, task.ID)
	}
	require.ElementsMatch(t, []int64{open, closed, article, release, announcement, maintenance}, ids)

}

func TestClaimKeepsSimplifiedChineseForTraditionalChinese(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	task, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "translate", EntityKey: "article:1", Locale: "zh-CN", DedupeKey: "traditional-dependency", RuleVersion: "fixture"})
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US"]}'::jsonb)`).Error)
	claimed, err := svc.ClaimWith(ctx, "translator", seotasks.ClaimFilter{Limit: 1, IDs: []int64{task.ID}})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, task.ID, claimed[0].ID)
}

func TestClosedSearchTasksPauseAndResumeWithoutStoppingTranslations(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US"]}'::jsonb)`).Error)
	for _, kind := range []string{"discover_fix", "snippet_template", "translate", "fix_fact", "maintenance", "alias", "tech_alert"} {
		_, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: kind, EntityKey: "fixture:" + kind, Locale: "de-DE", DedupeKey: kind, RuleVersion: "fixture"})
		require.NoError(t, err)
	}
	tasks, err := svc.Claim(ctx, "fixture", 20, nil)
	require.NoError(t, err)
	require.Len(t, tasks, 5)
	for _, kind := range []string{"discover_fix", "snippet_template"} {
		var task seotasks.Task
		require.NoError(t, st.DB.Where("kind=?", kind).First(&task).Error)
		require.Equal(t, "deferred", task.State)
		require.Zero(t, task.Attempts)
		require.Contains(t, string(task.Result), "locale_closed")
	}
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages,openLocales}','["en-US","de-DE"]')`).Error)
	tasks, err = svc.Claim(ctx, "fixture-reopened", 20, []string{"discover_fix", "snippet_template"})
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	for _, task := range tasks {
		require.Equal(t, 1, task.Attempts)
		require.NotContains(t, string(task.Result), "localePolicyPause")
	}
}
