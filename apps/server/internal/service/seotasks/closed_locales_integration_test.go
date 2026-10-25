//go:build integration

package seotasks_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestClaimSkipsWebsiteTranslationsInClosedLocales(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	insert := func(kind, entity, locale string) int64 {
		t.Helper()
		var id int64
		require.NoError(t, st.DB.Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,rule_version,state,priority) VALUES(?,?,?,?,'fixture','open',1) RETURNING id`,
			kind, entity, locale, kind+":"+entity+":"+locale).Scan(&id).Error)
		return id
	}
	open := insert("translate", "package:1", "ja-JP")
	closed := insert("translate", "package:2", "de-DE")
	article := insert("translate", "article:3", "pt-BR")
	// Client-only content keeps every client language; other task kinds ignore the list.
	release := insert("translate", "desktop_release:4", "de-DE")
	announcement := insert("translate", "announcement:desktop.announcement", "fr-FR")
	maintenance := insert("maintenance", "pkg:5", "it-IT")
	filter := seotasks.ClaimFilter{Limit: 10, ClosedLocales: []string{"pt-BR", "de-DE", "fr-FR", "it-IT", "zh-Hant"}}
	svc := &seotasks.Service{DB: st.DB}
	claimed, err := svc.ClaimWith(ctx, "translator", filter)
	require.NoError(t, err)
	ids := []int64{}
	for _, task := range claimed {
		ids = append(ids, task.ID)
	}
	require.ElementsMatch(t, []int64{open, release, announcement, maintenance}, ids)

	// Asked for by ID they are still not handed out, and they stay queued as they were.
	filter.Limit, filter.IDs = 2, []int64{closed, article}
	claimed, err = svc.ClaimWith(ctx, "translator", filter)
	require.NoError(t, err)
	require.Empty(t, claimed)
	var states []string
	require.NoError(t, st.DB.Raw("SELECT state FROM seo_tasks WHERE id IN (?,?)", closed, article).Scan(&states).Error)
	require.Equal(t, []string{"open", "open"}, states)
	// Once the languages open again the same tasks are claimable.
	claimed, err = svc.ClaimWith(ctx, "translator", seotasks.ClaimFilter{Limit: 2, IDs: []int64{closed, article}})
	require.NoError(t, err)
	require.Len(t, claimed, 2)
}

func TestClaimKeepsSimplifiedChineseForTraditionalChinese(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &seotasks.Service{DB: st.DB}
	task, err := svc.Enqueue(ctx, seotasks.NewTask{Kind: "translate", EntityKey: "article:1", Locale: "zh-CN", DedupeKey: "traditional-dependency", RuleVersion: "fixture"})
	require.NoError(t, err)
	claimed, err := svc.ClaimWith(ctx, "translator", seotasks.ClaimFilter{Limit: 1, ClosedLocales: []string{"zh-CN", "zh-Hant"}})
	require.NoError(t, err)
	require.Empty(t, claimed)
	claimed, err = svc.ClaimWith(ctx, "translator", seotasks.ClaimFilter{Limit: 1, ClosedLocales: []string{"zh-CN"}})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, task.ID, claimed[0].ID)
}
