//go:build integration

package management

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestLinkUpdatesRejectStaleRevision(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	agentWrite := func(id, revision int64, dry bool, body map[string]any) (any, error) {
		op := &domain.Operation{RequestID: uuid.NewString(), AgentClientID: 1, AgentTokenID: 2,
			Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, DryRun: dry,
			Execution: &domain.AgentExecution{ExpectedRevisionSet: true, ExpectedRevision: revision}}
		return svc.Execute(domain.WithOperation(ctx, op), "UpdateLinkPackage", Input{ID: id, Body: body})
	}
	for _, writer := range []string{"agent", "admin", "listing"} {
		t.Run(writer, func(t *testing.T) {
			token := "revision-" + writer
			link := map[string]any{"token": token, "name": token, "homepage": "https://vendor.example.test"}
			var id int64
			if writer == "listing" {
				created, err := svc.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: token, Body: map[string]any{"action": "publish", "link": link}})
				require.NoError(t, err)
				id = *created.(map[string]any)["packageId"].(*int64)
			} else {
				created, err := svc.Execute(ctx, "AddLinkPackage", Input{Body: link})
				require.NoError(t, err)
				id = valueID(created.(map[string]any)["id"])
			}
			readRevision := func() int64 {
				t.Helper()
				app, err := st.LinkApp(ctx, id)
				require.NoError(t, err)
				view := listingBody(t, app)
				require.Contains(t, view, "revision")
				require.NoError(t, spec.Components.Schemas["LinkPackage"].Value.VisitJSON(view))
				return app.Revision
			}
			before := readRevision()
			_, err := agentWrite(id, before, true, map[string]any{"name": "Preview only"})
			require.NoError(t, err)
			require.Equal(t, before, readRevision(), "previews must not consume the revision")
			firstURL := "https://downloads.example.test/first.dmg"
			patch := map[string]any{"downloadUrl": firstURL}
			switch writer {
			case "agent":
				_, err = agentWrite(id, before, false, patch)
			case "admin":
				_, err = svc.Execute(ctx, "UpdateLinkPackage", Input{ID: id, Body: patch})
			case "listing":
				link["downloadUrl"] = firstURL
				_, err = svc.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: token, Body: map[string]any{"action": "publish", "link": link}})
			}
			require.NoError(t, err)
			_, err = agentWrite(id, before, false, map[string]any{"downloadUrl": "https://downloads.example.test/stale.dmg"})
			var conflict *domain.AppError
			require.ErrorAs(t, err, &conflict)
			require.Equal(t, domain.CodeConflict, conflict.Code)
			app, err := st.LinkApp(ctx, id)
			require.NoError(t, err)
			require.Equal(t, firstURL, *app.DownloadURL)
			after := readRevision()
			require.Greater(t, after, before)
			_, err = agentWrite(id, after, false, map[string]any{"name": "Current revision accepted"})
			require.NoError(t, err)
			require.Greater(t, readRevision(), after, "name-only edits also advance the Link revision")
			if writer == "agent" {
				current := readRevision()
				start, results := make(chan struct{}), make(chan error, 2)
				for _, name := range []string{"Concurrent A", "Concurrent B"} {
					go func() {
						<-start
						_, err := agentWrite(id, current, false, map[string]any{"name": name})
						results <- err
					}()
				}
				close(start)
				first, second := <-results, <-results
				if first == nil {
					first, second = second, first
				}
				require.NoError(t, second, "exactly one concurrent writer succeeds")
				require.ErrorAs(t, first, &conflict)
				require.Equal(t, domain.CodeConflict, conflict.Code)
				task, err := (&seotasks.Service{DB: st.DB}).Enqueue(ctx, seotasks.NewTask{Kind: "fixture", EntityKey: "pkg:" + strconv.FormatInt(id, 10), DedupeKey: token, RuleVersion: "fixture", Evidence: map[string]any{}})
				require.NoError(t, err)
				trace, err := svc.seoTaskTrace(ctx, task.ID)
				require.NoError(t, err)
				require.Equal(t, readRevision(), trace.(map[string]any)["revisions"].(map[string]int64)["link:"+strconv.FormatInt(id, 10)])
			}
		})
	}
}

func TestMinimalListingResponseMatchesContract(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	ctx := domain.WithOperation(context.Background(), &domain.Operation{AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"*"}, RequiredPermissions: []string{"catalog:package:edit"}})
	for _, action := range []string{"save", "publish", "reject"} {
		t.Run(action, func(t *testing.T) {
			token := "minimal-" + action
			body := map[string]any{"action": action, "link": map[string]any{"token": token, "name": token, "pricing": "unknown", "homepage": "https://vendor.example.test"}}
			require.NoError(t, spec.Components.Schemas["SeoListingWrite"].Value.VisitJSON(body))
			result, err := svc.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: token, Body: body})
			require.NoError(t, err)
			require.NoError(t, spec.Components.Schemas["SeoListingCandidate"].Value.VisitJSON(listingBody(t, result)))
			candidate, err := st.ListingCandidate(ctx, token)
			require.NoError(t, err)
			require.NotContains(t, string(candidate.Payload), "downloadMode", "omitted modes must not be persisted as invalid empty values")
			// Previously saved empty modes must also produce a valid response.
			require.NoError(t, st.DB.Exec(`UPDATE seo_listing_candidates SET payload=jsonb_set(payload,'{downloadMode}','""') WHERE token=?`, token).Error)
			read, err := svc.Execute(ctx, "GetSeoListingCandidate", Input{Key: token})
			require.NoError(t, err)
			require.NoError(t, spec.Components.Schemas["SeoListingCandidate"].Value.VisitJSON(listingBody(t, read)))
			for _, mode := range []string{"official_page", "not_applicable", "direct"} {
				body["downloadMode"] = mode
				result, err = svc.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: token, Body: body})
				require.NoError(t, err)
				view := listingBody(t, result)
				require.NoError(t, spec.Components.Schemas["SeoListingCandidate"].Value.VisitJSON(view))
				require.Equal(t, mode, view["input"].(map[string]any)["downloadMode"])
			}
			for _, mode := range []string{"", "invalid"} {
				body["downloadMode"] = mode
				_, err = svc.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: token, Body: body})
				var invalid *domain.AppError
				require.ErrorAs(t, err, &invalid)
				require.Equal(t, domain.CodeValidation, invalid.Code)
			}
		})
	}
}
