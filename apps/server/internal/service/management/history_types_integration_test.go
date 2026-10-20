//go:build integration

package management

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestEveryContentTypeRestoreSchedulesHermesTasks(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(100,'cask','history-types','history-types','homebrew/cask','History Types','1','1','{}','hash');
INSERT INTO categories(id,slug,icon,source_locale) VALUES(100,'history-types','lucide:box','zh-CN');
INSERT INTO collections(id,slug,source_locale) VALUES(100,'history-types','zh-CN');
INSERT INTO features(id,placement,target_type,url,source_locale) VALUES(100,'home_hero','url','https://example.com','zh-CN');
INSERT INTO releases(id,package_id,source,source_key,version,source_locale) VALUES(100,100,'editorial','history-types','1','zh-CN');
INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(100,'screenshot','history-types','https://example.com/a.png',100,100,1,'hash','image/png');
INSERT INTO package_screenshots(id,package_id,asset_id,source_locale) VALUES(100,100,100,'zh-CN');
INSERT INTO collection_items(collection_id,package_id,source_locale) VALUES(100,100,'zh-CN');
INSERT INTO desktop_releases(id,version,artifacts,source_locale) VALUES(100,'1','[]','zh-CN');
INSERT INTO mirrors(id,key,probe_url,source_locale) VALUES(100,'history-types','https://example.com','zh-CN');
INSERT INTO app_config(key,value) VALUES('desktop.announcement','{"sourceLocale":"zh-CN"}');
INSERT INTO article_authors(id,slug,name,source_locale) VALUES(100,'history-types','History Types','zh-CN');
INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at,content_updated_at) VALUES(100,'history-types','published',100,100,now(),now());
INSERT INTO article_revisions(id,article_id,number,source_locale,type,published_at) VALUES(100,100,1,'zh-CN','tutorial',now());`).Error)
	svc := &Service{Store: st}

	fields := map[string]string{"package": "summary", "release": "summary", "category": "name", "collection": "title", "feature": "title", "screenshot": "caption", "collection_item": "note", "desktop_release": "notes", "mirror": "name", "announcement": "title", "article": "title", "article_author": "bio"}
	for _, entity := range content.Names() {
		t.Run(entity, func(t *testing.T) {
			ref := content.Ref{Entity: entity, ID: 100}
			if entity == "collection_item" {
				ref.SecondaryID = 100
			}
			write := func(value string) {
				t.Helper()
				op := &domain.Operation{RequestID: uuid.NewString(), Name: "testContentWrite", Permissions: []string{"*"}, RequiredPermissions: []string{entityPermission(entity)}, AllowDelete: true}
				request := domain.WithOperation(ctx, op)
				in := Input{Body: map[string]any{"ref": ref, "sourceLocale": "zh-CN"}}
				_, err := svc.write(request, in, "TestContentWrite", "translation", func(tx *store.Store) (any, error) {
					return nil, tx.WriteContentText(request, ref, "zh-CN", map[string]any{fields[entity]: value}, true)
				})
				require.NoError(t, err)
			}
			write("这款应用可以管理文件")
			var revision int64
			require.NoError(t, st.DB.Raw("SELECT max(id) FROM content_revisions WHERE entity=? AND object_key=?", entity, ref.Key()).Scan(&revision).Error)
			require.Positive(t, revision)
			write("这款应用可以整理照片")
			_, err := svc.Execute(ctx, "RestoreRevision", Input{ID: revision, Body: map[string]any{}})
			require.NoError(t, err)
			source, err := st.ReadContentSource(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, "这款应用可以管理文件", source.Fields[fields[entity]])
			require.Len(t, source.Targets, targetCount(ref))
			view, err := st.ContentView(ctx, ref)
			require.NoError(t, err)
			require.Len(t, view["i18n"], targetCount(ref)+1)
			deleteOps := map[string]string{"category": "DeleteCategory", "collection": "DeleteCollection", "feature": "DeleteFeature", "screenshot": "DeletePackageScreenshot"}
			if op := deleteOps[entity]; op != "" {
				_, err := svc.Execute(ctx, op, Input{ID: 100, ScreenshotID: 100})
				require.NoError(t, err)
				var trashID int64
				require.NoError(t, st.DB.Raw("SELECT max(id) FROM content_trash WHERE entity=? AND object_key=?", entity, ref.Key()).Scan(&trashID).Error)
				require.Positive(t, trashID)
				_, err = svc.Execute(ctx, "RestoreTrash", Input{ID: trashID, Body: map[string]any{}})
				require.NoError(t, err)
				source, err = st.ReadContentSource(ctx, ref)
				require.NoError(t, err)
				require.Len(t, source.Targets, targetCount(ref))
			}
		})
	}
}

func targetCount(ref content.Ref) int {
	if ref.Entity == "desktop_release" {
		return 5
	}
	return 9
}
