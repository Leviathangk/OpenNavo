//go:build integration

package management

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
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
	for _, writer := range []string{"agent", "admin"} {
		t.Run(writer, func(t *testing.T) {
			token := "revision-" + writer
			link := map[string]any{"token": token, "name": token, "homepage": "https://vendor.example.test"}
			created, err := svc.Execute(ctx, "AddLinkPackage", Input{Body: link})
			require.NoError(t, err)
			id := valueID(created.(map[string]any)["id"])
			readRevision := func() int64 {
				t.Helper()
				app, err := st.LinkApp(ctx, id)
				require.NoError(t, err)
				view := jsonBody(t, app)
				require.Contains(t, view, "revision")
				require.NoError(t, spec.Components.Schemas["LinkPackage"].Value.VisitJSON(view))
				return app.Revision
			}
			before := readRevision()
			_, err = agentWrite(id, before, true, map[string]any{"name": "Preview only"})
			require.NoError(t, err)
			require.Equal(t, before, readRevision(), "previews must not consume the revision")
			firstURL := "https://downloads.example.test/first.dmg"
			patch := map[string]any{"downloadUrl": firstURL}
			switch writer {
			case "agent":
				_, err = agentWrite(id, before, false, patch)
			case "admin":
				_, err = svc.Execute(ctx, "UpdateLinkPackage", Input{ID: id, Body: patch})
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
			require.Equal(t, "rejected: revision_conflict:"+strconv.FormatInt(after, 10), conflict.Msg)
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
				// Reads report the revision the next write checks.
				revision, err := svc.CurrentRevision(ctx, "UpdateLinkPackage", Input{ID: id})
				require.NoError(t, err)
				require.Equal(t, readRevision(), revision)
			}
		})
	}
}
