//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestManagementLocalizedWritesPreserveNotificationScope(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(701,'locale-fixture','isolated-test-only');
 INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','locale-fixture','locale-fixture','homebrew/cask','Locale fixture','1','1','{}','fixture');
 INSERT INTO releases(id,package_id,version,source,source_key,source_locale,title,body_markdown) VALUES(702,701,'1','editorial','1','en-US','Changes','Improvements')`).Error)
	svc := &Service{Store: st}
	actorID := int64(701)
	actor := store.AuditActor{ID: &actorID}
	for _, tc := range []struct {
		operation string
		id        int64
	}{{"UpdatePackageI18n", 701}, {"UpdateReleaseI18n", 702}} {
		t.Run(tc.operation, func(t *testing.T) {
			for _, locale := range []string{"en-US", "ja-JP", "zh-CN"} {
				require.NoError(t, st.DB.Exec("DELETE FROM catalog_changes").Error)
				body := map[string]any{"summary": "Updated localized fixture", "sections": []any{}}
				if locale == "ja-JP" {
					body["summary"] = "開発者向けのテキストエディタ"
				}
				if locale == "zh-CN" {
					body["summary"] = "需要管理员权限"
				}
				_, err := svc.Execute(ctx, tc.operation, Input{ID: tc.id, Locale: locale, Body: body, Actor: actor})
				require.NoError(t, err)
				changes, err := st.PendingURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
				require.NoError(t, err)
				require.NotEmpty(t, changes)
				main := changes[len(changes)-1]
				if locale == "en-US" {
					require.Nil(t, main.Locales)
				} else {
					require.Equal(t, []string{locale}, main.Locales)
				}
				if locale == "zh-CN" {
					require.Len(t, changes, 2)
					require.Equal(t, []string{"zh-Hant"}, changes[0].Locales)
				}
			}
		})
	}
}
