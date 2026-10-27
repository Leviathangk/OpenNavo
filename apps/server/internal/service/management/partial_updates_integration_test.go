//go:build integration

package management

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPartialUpdatesReplayBeforeMergingOrCheckingState(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ctx := context.Background()
	for _, entity := range []string{"Category", "Collection", "Feature"} {
		for _, guards := range []struct {
			revision, state bool
		}{{false, false}, {true, false}, {false, true}, {true, true}} {
			t.Run(fmt.Sprintf("%s/revision=%t/state=%t", entity, guards.revision, guards.state), func(t *testing.T) {
				label, secondary := "title", "subtitle"
				if entity == "Category" {
					label, secondary = "name", "description"
				}
				body := map[string]any{"sourceLocale": "en-US", "i18n": map[string]any{
					"en-US": map[string]any{label: "Source", secondary: "Keep source"},
					"de-DE": map[string]any{label: "German", secondary: "Keep German"},
				}}
				switch entity {
				case "Category":
					body["slug"], body["icon"], body["appliesTo"] = "partial-"+uuid.NewString(), "lucide:box", "cask"
					body["visible"], body["hiddenByDefault"] = true, false
				case "Collection":
					body["slug"], body["sort"] = "partial-"+uuid.NewString(), float64(0)
				case "Feature":
					body["placement"], body["targetType"], body["url"] = "home_hero", "url", "https://vendor.example/"
					body["status"], body["sort"] = "draft", float64(0)
				}
				created, err := svc.Execute(ctx, "Create"+entity, Input{Body: body})
				require.NoError(t, err)
				id := valueID(hermesMap(created)["id"])
				operation := "Update" + entity
				read := "Get" + entity
				if entity == "Collection" {
					read = "GetAdminCollection"
				} else if entity == "Category" {
					read = "GetCategoryTree"
				}
				view := func() map[string]any {
					t.Helper()
					result, err := svc.Execute(ctx, read, Input{ID: id})
					require.NoError(t, err)
					if entity == "Category" {
						return categoryNode(result.([]map[string]any), id)
					}
					return hermesMap(result)
				}
				// Legacy unrelated translations must never be replayed through
				// current authoring limits just to merge a different language.
				table, column := strings.ToLower(entity)+"_i18n", strings.ToLower(entity)+"_id"
				require.NoError(t, st.DB.Exec("INSERT INTO "+table+"("+column+",locale,"+label+",source_locale,status) VALUES(?,'fr-FR',?,'en-US','manual')", id, strings.Repeat("x", 200)).Error)
				initial := view()
				patch := map[string]any{"i18n": map[string]any{"de-DE": map[string]any{label: "Changed"}}}
				key := uuid.NewString()
				requestContext := func(retryKey string) context.Context {
					execution := &domain.AgentExecution{IdempotencyKey: retryKey}
					if guards.revision {
						execution.ExpectedRevisionSet, execution.ExpectedRevision = true, valueID(initial["revision"])
					}
					permission := "content:" + strings.ToLower(entity) + ":edit"
					if entity == "Category" {
						permission = "catalog:category:edit"
					}
					return domain.WithOperation(ctx, &domain.Operation{RequestID: uuid.NewString(), Name: operation,
						AgentClientID: 1, AgentTokenID: 2, Permissions: []string{permission}, RequiredPermissions: []string{permission}, Execution: execution})
				}
				if guards.state {
					patch["expectedState"], err = partialStateHash(initial)
					require.NoError(t, err)
				}
				// Treat the first successful response as lost. Recreate the service
				// on retry to prove replay does not depend on an adapter cache.
				_, err = svc.Execute(requestContext(key), operation, Input{ID: id, Body: patch})
				require.NoError(t, err)
				after := view()
				require.Greater(t, valueID(after["revision"]), valueID(initial["revision"]))
				locales := after["i18n"].(map[string]any)
				require.Equal(t, "Keep German", locales["de-DE"].(map[string]any)[secondary])
				require.Equal(t, "Source", locales["en-US"].(map[string]any)[label])
				require.Equal(t, strings.Repeat("x", 200), locales["fr-FR"].(map[string]any)[label])
				// A later independent write must survive the original retry.
				_, err = svc.Execute(ctx, operation, Input{ID: id, Body: map[string]any{"i18n": map[string]any{"de-DE": map[string]any{label: "Later"}}}})
				require.NoError(t, err)
				later := view()
				restarted := &Service{Store: st}
				_, err = restarted.Execute(requestContext(key), operation, Input{ID: id, Body: patch})
				require.NoError(t, err)
				require.Equal(t, later, view())
				changed := map[string]any{"i18n": map[string]any{"de-DE": map[string]any{label: "Different"}}}
				_, err = restarted.Execute(requestContext(key), operation, Input{ID: id, Body: changed})
				require.ErrorContains(t, err, "idempotency_key_reused")
				if guards.revision {
					_, err = restarted.Execute(requestContext(uuid.NewString()), operation, Input{ID: id, Body: patch})
					require.ErrorContains(t, err, "revision_conflict")
				}
				// A fresh request with an old digest fails in the transaction.
				stale := requestContext(uuid.NewString())
				domain.CurrentOperation(stale).Execution.ExpectedRevisionSet = false
				digest, err := partialStateHash(initial)
				require.NoError(t, err)
				_, err = restarted.Execute(stale, operation, Input{ID: id, Body: map[string]any{"expectedState": digest}})
				var app *domain.AppError
				require.ErrorAs(t, err, &app)
				require.Equal(t, domain.CodeConflict, app.Code)
				require.Equal(t, later, view())
				// Neither merging nor hashing may mutate the submitted patch.
				raw, err := json.Marshal(patch)
				require.NoError(t, err)
				require.Contains(t, string(raw), "Changed")
				require.NotContains(t, string(raw), "Later")
				_, err = svc.Execute(ctx, operation, Input{ID: id, Body: map[string]any{"i18n": map[string]any{"ja-JP": map[string]any{secondary: "Missing name/title"}}}})
				require.ErrorContains(t, err, "field_required")
				_, err = svc.Execute(ctx, operation, Input{ID: id, Body: map[string]any{"i18n": nil}})
				require.ErrorContains(t, err, "i18n_required")
				require.Equal(t, later, view())
				_, err = svc.Execute(ctx, operation, Input{ID: id, Body: map[string]any{"sourceLocale": "de-DE"}})
				require.NoError(t, err)
				require.Equal(t, "de-DE", view()["sourceLocale"])
				_, err = svc.Execute(ctx, operation, Input{ID: id, Body: map[string]any{"i18n": map[string]any{"en-US": nil}}})
				if entity == "Category" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.NotContains(t, view()["i18n"], "en-US")
				}
			})
		}
	}
}
