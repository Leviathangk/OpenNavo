//go:build integration

package management

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestArticleFixTranslationPinsAgentRevision(t *testing.T) {
	for _, republishBeforeLock := range []bool{true, false} {
		name := "republish between resolution and write is blocked"
		if republishBeforeLock {
			name = "republish before locked resolution rejects stale expected revision"
		}
		t.Run(name, func(t *testing.T) {
			st := testutil.NewSEOStore(t)
			svc := &Service{Store: st}
			ctx := hermesContext("translation:review", "content:article:edit")
			require.NoError(t, st.DB.Exec(`INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at)
 VALUES(31,'translation-concurrency','published',402,401,now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type,published_at)
 VALUES(401,31,1,'en-US','news',now()),(402,31,2,'en-US','news',now());
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status)
 VALUES(401,'en-US','First source','en-US','source'),(402,'en-US','Second source','en-US','source'),
 (401,'de-DE','First translation','en-US','manual'),(402,'de-DE','Second translation','en-US','manual')`).Error)
			refs := []content.Ref{{Entity: "article", ID: 401}, {Entity: "article", ID: 402}}
			states := make([]store.HistoryState, len(refs))
			revisions := make([]int64, len(refs))
			for i, ref := range refs {
				var err error
				states[i], err = st.HistorySnapshot(ctx, ref)
				require.NoError(t, err)
				revisions[i], err = st.RecordRevision(ctx, ref, nil, states[i], "create", nil)
				require.NoError(t, err)
			}
			require.NotEqual(t, revisions[0], revisions[1])
			// The caller addresses only the article identity and supplies the
			// optimistic revision obtained while revision 401 was published.
			in := Input{Body: map[string]any{
				"ref": map[string]any{"entity": "article"}, "articleId": 31,
				"locale": "de-DE", "fields": map[string]any{"title": "Corrected translation"},
			}}
			current, err := svc.CurrentRevision(ctx, "FixTranslation", in)
			require.NoError(t, err)
			require.Equal(t, revisions[0], current)
			op := *domain.CurrentOperation(ctx)
			op.Name = "fixTranslation"
			op.Execution = &domain.AgentExecution{IdempotencyKey: uuid.NewString(), ExpectedRevision: current, ExpectedRevisionSet: true}
			ctx = domain.WithOperation(ctx, &op)
			var attempted bool
			var publicationErr error
			callback := "test:republish-during-agent-translation-fix"
			require.NoError(t, st.DB.Callback().Row().Before("gorm:row").Register(callback, func(db *gorm.DB) {
				query := db.Statement.SQL.String()
				if attempted {
					return
				}
				if republishBeforeLock {
					if !strings.Contains(query, "FROM articles") || !strings.Contains(query, "FOR UPDATE") {
						return
					}
				} else if !strings.HasPrefix(query, "SELECT coalesce(max(id),0) FROM content_revisions") {
					return
				}
				attempted = true
				// A second connection models publication's article lock. NOWAIT
				// proves serialization without depending on goroutine timing.
				publicationErr = st.WithTx(ctx, func(tx *gorm.DB) error {
					var id int64
					if err := tx.Raw("SELECT id FROM articles WHERE id=31 FOR UPDATE NOWAIT").Scan(&id).Error; err != nil {
						return err
					}
					return tx.Exec("UPDATE articles SET published_revision_id=402 WHERE id=31").Error
				})
			}))
			t.Cleanup(func() { _ = st.DB.Callback().Row().Remove(callback) })
			fixed, err := svc.Execute(ctx, "FixTranslation", in)
			require.True(t, attempted)
			if republishBeforeLock {
				require.NoError(t, publicationErr)
				var conflict *domain.AppError
				require.ErrorAs(t, err, &conflict)
				require.Equal(t, domain.CodeConflict, conflict.Code)
				require.Equal(t, "rejected: revision_conflict:"+strconv.FormatInt(revisions[1], 10), conflict.Msg)
				require.Nil(t, fixed)
			} else {
				var locked *pgconn.PgError
				require.ErrorAs(t, publicationErr, &locked)
				require.Equal(t, "55P03", locked.Code, "the article must already be locked before validating the agent revision")
				require.NoError(t, err)
				require.EqualValues(t, 401, hermesMap(fixed)["ref"].(map[string]any)["id"])
			}
			require.NoError(t, st.DB.Callback().Row().Remove(callback))
			for i, ref := range refs {
				after, err := st.HistorySnapshot(ctx, ref)
				require.NoError(t, err)
				if republishBeforeLock || i == 1 {
					require.Equal(t, store.HistoryHash(states[i]), store.HistoryHash(after), "rejection and unrelated revisions must preserve all stored fields")
				} else {
					value, err := st.TranslationValue(ctx, ref, "de-DE")
					require.NoError(t, err)
					require.Equal(t, "Corrected translation", value.Fields["title"])
				}
			}
			var objects []string
			require.NoError(t, st.DB.Table("content_revisions").Where("request_id=?", op.RequestID).Pluck("object_key", &objects).Error)
			var events int64
			require.NoError(t, st.DB.Table("agent_write_events").Where("request_id=? AND result='live'", op.RequestID).Count(&events).Error)
			var audits []struct{ BeforeID, AfterID int64 }
			require.NoError(t, st.DB.Raw(`SELECT (before#>>'{article_revisions,0,id}')::bigint AS before_id,
 (after#>>'{article_revisions,0,id}')::bigint AS after_id FROM audit_logs WHERE request_id=? AND action='FixTranslation'`, op.RequestID).Scan(&audits).Error)
			if republishBeforeLock {
				require.Empty(t, objects)
				require.Zero(t, events)
				require.Empty(t, audits)
			} else {
				require.Equal(t, []string{"401"}, objects, "history must name the pinned revision")
				require.EqualValues(t, 1, events, "change tracking must observe the written revision")
				require.Len(t, audits, 1)
				require.EqualValues(t, 401, audits[0].BeforeID)
				require.EqualValues(t, 401, audits[0].AfterID)
				// Publication can proceed after the fix releases the article lock.
				require.NoError(t, st.WithTx(context.Background(), func(tx *gorm.DB) error {
					return tx.Exec("UPDATE articles SET published_revision_id=402 WHERE id=31").Error
				}))
			}
			_, supplied := in.Body["ref"].(map[string]any)["id"]
			require.False(t, supplied, "pinning must not mutate the caller's request or idempotency payload")
		})
	}
}
