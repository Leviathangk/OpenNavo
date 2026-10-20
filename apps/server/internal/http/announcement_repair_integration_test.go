//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestAnnouncementRepairsNonObjectWithoutAutomaticTranslation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	manager := &management.Service{Store: st}
	pub := &catalog.PublicService{Store: st}
	app, err := httpserver.New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Public: &public.Handler{Catalog: pub}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	readPublic := func(t *testing.T, enabled bool, title string) {
		t.Helper()
		for _, platform := range []string{"web", "desktop"} {
			req := httptest.NewRequest("GET", "/api/v1/config/client?platform="+platform+"&version=1.0.0&locale=zh-CN", nil)
			reply := httptest.NewRecorder()
			app.Engine.ServeHTTP(reply, req)
			require.Equal(t, 200, reply.Code, reply.Body.String())
			testutil.ValidateResponse(t, spec, "/api/v1", req, reply)
			var body struct{ Data publicapi.ClientConfig }
			require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &body))
			if !enabled {
				require.Nil(t, body.Data.Announcement)
				continue
			}
			require.NotNil(t, body.Data.Announcement)
			require.Equal(t, title, body.Data.Announcement.Title)
			require.Equal(t, "desktop.announcement", body.Data.Announcement.Id)
			require.Equal(t, publicapi.AnnouncementLevel("info"), body.Data.Announcement.Level)
		}
	}
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"sql-null", nil}, {"json-null", "null"}, {"array", `[null,{"enabled":false},{"enabled":true,"sourceLocale":"zh-CN","title":"坏数据"}]`},
		{"string", `"legacy"`}, {"number", "42"}, {"boolean", "true"}, {"invalid-enabled", `{"enabled":"invalid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Only the SQL NULL case changes the isolated fixture; all other cases preserve production NOT NULL constraints.
			if tc.raw == nil {
				require.NoError(t, st.DB.Exec("ALTER TABLE app_config ALTER COLUMN value DROP NOT NULL").Error)
				defer func() {
					require.NoError(t, st.DB.Exec("UPDATE app_config SET value='null'::jsonb WHERE value IS NULL; ALTER TABLE app_config ALTER COLUMN value SET NOT NULL").Error)
				}()
			}
			require.NoError(t, st.DB.Exec("DELETE FROM announcement_i18n; DELETE FROM content_translation_sources WHERE entity='announcement'; DELETE FROM job_outbox WHERE unique_key='translate:content:announcement:desktop.announcement'").Error)
			require.NoError(t, st.DB.Exec("INSERT INTO app_config(key,value) VALUES('desktop.announcement',?::jsonb) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value", tc.raw).Error)
			require.NoError(t, st.Redis.FlushDB(ctx).Err())
			before, readErr := manager.Execute(ctx, "GetAnnouncement", management.Input{})
			require.NoError(t, readErr)
			require.Equal(t, false, before.(map[string]any)["enabled"])
			require.NoError(t, st.ScheduleAnnouncement(ctx))
			readPublic(t, false, "")
			save := func(enabled bool, title string) any {
				t.Helper()
				result, writeErr := manager.Execute(ctx, "UpdateAnnouncement", management.Input{Body: map[string]any{"enabled": enabled, "sourceLocale": "zh-CN", "title": title, "body": "公告正文"}})
				require.NoError(t, writeErr)
				return result
			}
			// Undoing the first repair restores the historical value; reading/restoring must not return 500, and a subsequent save can repair it again.
			repaired := save(true, "首次修复").(map[string]any)
			_, err = manager.Execute(ctx, "RevertRequest", management.Input{RequestID: repaired["requestId"].(string)})
			require.NoError(t, err)
			_, err = manager.Execute(ctx, "GetAnnouncement", management.Input{})
			require.NoError(t, err)
			readPublic(t, false, "")
			for index, enabled := range []bool{true, false} {
				title := []string{"第一次保存", "第二次保存"}[index]
				save(enabled, title)
				var kind string
				require.NoError(t, st.DB.Raw("SELECT jsonb_typeof(value) FROM app_config WHERE key='desktop.announcement'").Scan(&kind).Error)
				require.Equal(t, "object", kind)
				result, readErr := manager.Execute(ctx, "GetAnnouncement", management.Input{})
				require.NoError(t, readErr)
				view := result.(map[string]any)
				require.Equal(t, enabled, view["enabled"])
				locales := view["i18n"].(map[string]any)
				require.Len(t, locales, len(i18n.SiteLocales))
				for locale, row := range locales {
					expected := "missing"
					if locale == "zh-CN" {
						expected = "source"
					}
					require.Equal(t, expected, row.(map[string]any)["status"], locale)
				}
				var counts struct{ Sources, Jobs int64 }
				require.NoError(t, st.DB.Raw("SELECT (SELECT count(*) FROM content_translation_sources WHERE entity='announcement') sources,(SELECT count(*) FROM job_outbox WHERE unique_key='translate:content:announcement:desktop.announcement' AND job_type='translate:content') jobs").Scan(&counts).Error)
				require.EqualValues(t, 1, counts.Sources)
				require.Zero(t, counts.Jobs)
				readPublic(t, enabled, title)
			}
		})
	}
}
