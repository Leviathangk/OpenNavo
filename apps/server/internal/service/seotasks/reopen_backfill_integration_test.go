//go:build integration

package seotasks_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestReopenBackfilledTranslation(t *testing.T) {
	for _, outcome := range []string{"quarantined", "skipped"} {
		for _, person := range []bool{false, true} {
			for _, change := range []string{"unchanged", "changed", "deleted", "stale_snapshot"} {
				t.Run(fmt.Sprintf("%s/person=%t/%s", outcome, person, change), func(t *testing.T) {
					ctx := context.Background()
					st := testutil.NewSEOStore(t)
					require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(991,'reopen-backfill','lucide:box','en-US');
INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(991,'en-US','Editors','en-US','source')`).Error)
					_, _, err := st.BackfillTranslationTasksForLocales(ctx)
					require.NoError(t, err)
					var task seotasks.Task
					// A language open for indexing: closed ones get no new translation work.
					require.NoError(t, st.DB.Where("entity_key=? AND locale=?", "category:991", "ja-JP").First(&task).Error)
					source, err := st.ReadContentSource(ctx, content.Ref{Entity: "category", ID: 991})
					require.NoError(t, err)
					require.Equal(t, source.Hash, task.EvidenceHash)
					var snapshots int64
					require.NoError(t, st.DB.Table("content_translation_sources").Where("entity=? AND object_key=?", "category", "991").Count(&snapshots).Error)
					require.Zero(t, snapshots, "backfill creates valid tasks without source snapshots")
					svc := &seotasks.Service{DB: st.DB}
					claimed := claimOne(t, svc, "agent:1:2", task.ID)
					_, err = svc.Complete(ctx, task.ID, claimed.Fencing, "agent:1:2", outcome, "correctable translation validation error")
					require.NoError(t, err)
					switch change {
					case "stale_snapshot":
						require.NoError(t, st.DB.Exec(`INSERT INTO content_translation_sources(entity,object_key,ref,source_locale,source_hash,fields) VALUES('category','991','{"entity":"category","id":991}','en-US',?,'{}')`, source.Hash).Error)
						fallthrough
					case "changed":
						require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='New editors' WHERE category_id=991 AND locale='en-US'").Error)
					case "deleted":
						require.NoError(t, st.DB.Exec("DELETE FROM categories WHERE id=991").Error)
					}
					owner := "agent:1:2"
					if person {
						owner = "admin:1"
					}
					reopened, err := svc.Reopen(ctx, task.ID, owner, "corrected translation", person)
					require.NoError(t, err)
					require.Equal(t, "open", reopened.State)
					require.Equal(t, 1, reopened.ReopenCount)
					again := claimOne(t, svc, owner, task.ID)
					require.Greater(t, again.Fencing, claimed.Fencing)

				})
			}
		}
	}
}
