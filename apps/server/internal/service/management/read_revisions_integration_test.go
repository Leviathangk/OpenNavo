//go:build integration

package management

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReadRevisionUsesContentSnapshot(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	svc := &Service{Store: st}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(703,'snapshot-category','lucide:box','en-US')").Error)
	before, err := svc.CurrentRevision(ctx, "UpdateCategory", Input{ID: 703})
	require.NoError(t, err)
	var wrote bool
	// Commit a second connection's write after the content SELECT has acquired its snapshot,
	// but before the service reads the revision. No timing or sleeps are needed.
	require.NoError(t, st.DB.Callback().Row().After("gorm:row").Register("test:concurrent-category-write", func(db *gorm.DB) {
		if wrote || !strings.Contains(db.Statement.SQL.String(), "FROM categories") {
			return
		}
		wrote = true
		_, err := svc.Execute(ctx, "UpdateCategory", Input{ID: 703, Body: map[string]any{"icon": "lucide:star", "sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{"name": "Snapshot category"}}}})
		require.NoError(t, err)
	}))
	t.Cleanup(func() { _ = st.DB.Callback().Row().Remove("test:concurrent-category-write") })
	result, err := svc.Execute(ctx, "GetCategoryTree", Input{})
	require.NoError(t, err)
	require.True(t, wrote)
	nodes := result.([]map[string]any)
	require.Len(t, nodes, 1)
	require.Equal(t, "lucide:box", nodes[0]["icon"])
	require.Equal(t, before, nodes[0]["revision"])
	current, err := svc.CurrentRevision(ctx, "UpdateCategory", Input{ID: 703})
	require.NoError(t, err)
	require.Greater(t, current, before)
	op := &domain.Operation{AgentClientID: 1, AgentTokenID: 2,
		Permissions: []string{"catalog:category:edit"}, RequiredPermissions: []string{"catalog:category:edit"},
		Execution: &domain.AgentExecution{ExpectedRevisionSet: true, ExpectedRevision: before}}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", Input{ID: 703, Body: map[string]any{"icon": "lucide:folder"}})
	var appErr *domain.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, domain.CodeConflict, appErr.Code)
}

func TestPackagePageReadsUseContentSnapshot(t *testing.T) {
	for _, tc := range []struct {
		read, write, table, field string
		old, next                 map[string]any
	}{
		{"GetPackageSeo", "SetPackageSeo", "package_seo_overrides", "title",
			map[string]any{"locale": "en-US", "title": "Original title"},
			map[string]any{"locale": "en-US", "title": "Concurrent title"}},
		{"GetPackageIntentPages", "SetPackageIntentPages", "package_intent_pages", "enabled",
			map[string]any{"items": []any{map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": false}}},
			map[string]any{"items": []any{map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": true}}}},
	} {
		t.Run(tc.read, func(t *testing.T) {
			st := testutil.NewSEOStore(t)
			ctx := context.Background()
			svc := &Service{Store: st}
			require.NoError(t, st.DB.Exec("INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(601,'cask','snapshot-pages','snapshot-pages','homebrew/cask','Snapshot Pages','1','1','{}','fixture')").Error)
			_, err := svc.Execute(ctx, tc.write, Input{ID: 601, Body: tc.old})
			require.NoError(t, err)
			before, err := svc.CurrentRevision(ctx, tc.write, Input{ID: 601})
			require.NoError(t, err)
			wrote := false
			callback := "test:concurrent-page-write"
			require.NoError(t, st.DB.Callback().Row().After("gorm:row").Register(callback, func(db *gorm.DB) {
				if wrote || !strings.Contains(db.Statement.SQL.String(), "FROM "+tc.table) {
					return
				}
				wrote = true
				_, err := svc.Execute(ctx, tc.write, Input{ID: 601, Body: tc.next})
				require.NoError(t, err)
			}))
			t.Cleanup(func() { _ = st.DB.Callback().Row().Remove(callback) })
			result, err := svc.Execute(ctx, tc.read, Input{ID: 601})
			require.NoError(t, err)
			require.True(t, wrote)
			view := hermesMap(result)
			item := view["items"].([]any)[0].(map[string]any)
			oldValue := tc.old["title"]
			if tc.field == "enabled" {
				oldValue = false
			}
			require.Equal(t, oldValue, item[tc.field])
			require.EqualValues(t, before, view["revision"], "content and revision must share a snapshot")
			op := &domain.Operation{AgentClientID: 1, AgentTokenID: 2,
				Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"},
				Execution: &domain.AgentExecution{ExpectedRevisionSet: true, ExpectedRevision: valueID(view["revision"])}}
			_, err = svc.Execute(domain.WithOperation(ctx, op), tc.write, Input{ID: 601, Body: tc.old})
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, domain.CodeConflict, app.Code)
			require.Contains(t, app.Msg, "revision_conflict")
			current, err := svc.Execute(ctx, tc.read, Input{ID: 601})
			require.NoError(t, err)
			latest := hermesMap(current)["items"].([]any)[0].(map[string]any)
			nextValue := tc.next["title"]
			if tc.field == "enabled" {
				nextValue = true
			}
			require.Equal(t, nextValue, latest[tc.field])
		})
	}
}
