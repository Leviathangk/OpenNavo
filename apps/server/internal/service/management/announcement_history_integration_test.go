//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestAnnouncementHistoryPreservesLegacySourceLocale(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO app_config(key,value) VALUES('desktop.announcement','{}')`).Error)
	svc := &Service{Store: st}
	ref := content.Ref{Entity: "announcement"}
	for _, operation := range []string{"RestoreRevision", "RevertRequest"} {
		for _, locale := range []string{"missing", "null", "", "zh-CN", "en-US"} {
			t.Run(operation+"/"+locale, func(t *testing.T) {
				sourceLocale := "zh-CN"
				if locale == "en-US" {
					sourceLocale = locale
				}
				require.NoError(t, st.WriteContentText(ctx, ref, sourceLocale, map[string]any{"title": "这款应用可以管理文件"}, true))
				legacy, err := st.HistorySnapshot(ctx, ref)
				require.NoError(t, err)
				value := legacy["app_config"][0]["value"].(map[string]any)
				switch locale {
				case "missing":
					delete(value, "sourceLocale")
				case "null":
					value["sourceLocale"] = nil
				default:
					value["sourceLocale"] = locale
				}
				legacyHash := store.HistoryHash(legacy)
				require.NoError(t, st.WriteContentText(ctx, ref, "en-US", map[string]any{"title": "Updated announcement"}, true))
				current, err := st.HistorySnapshot(ctx, ref)
				require.NoError(t, err)
				op := &domain.Operation{RequestID: uuid.NewString(), Name: "UpdateAnnouncement", RequiredPermissions: []string{"content:announcement:edit"}}
				before, after, action := current, legacy, "update"
				if operation == "RevertRequest" {
					before, after = legacy, current
				}
				id, err := st.RecordRevision(ctx, ref, before, after, action, op)
				require.NoError(t, err)
				result, err := svc.Execute(ctx, operation, Input{ID: id, RequestID: op.RequestID, Body: map[string]any{}})
				require.NoError(t, err)
				require.Equal(t, "pending", result.(map[string]any)["translation"].(map[string]any)["status"])
				source, err := st.ReadContentSource(ctx, ref)
				require.NoError(t, err)
				require.Equal(t, sourceLocale, source.SourceLocale)
				require.Equal(t, "这款应用可以管理文件", source.Fields["title"])
				require.Len(t, source.Targets, translationTargetCount(ref))

				var saved json.RawMessage
				column := "after_data"
				if operation != "RestoreRevision" {
					column = "before_data"
				}
				require.NoError(t, st.DB.Raw("SELECT "+column+" FROM content_revisions WHERE request_id=?", op.RequestID).Row().Scan(&saved))
				original, err := revisionState(saved)
				require.NoError(t, err)
				require.Equal(t, legacyHash, store.HistoryHash(original))
			})
		}
	}
}
