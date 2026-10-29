//go:build integration

package taps_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTapCustomSiteToken(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	github := newFakeGitHub()
	github.repository = "stablyai/homebrew-orca"
	source, err := os.ReadFile("../../homebrew/testdata/casks/orca.rb")
	require.NoError(t, err)
	day := time.Now().UTC().Add(-48 * time.Hour)
	github.commit("Casks/orca.rb", sha(0), string(source), day)
	reader := &taps.Service{Store: st, GitHub: github}
	admin := &management.Service{Store: st, Taps: reader, Queue: &queue{}}
	official := `{"token":"orca","tap":"homebrew/cask","version":"1.3.1","name":["Plotly Orca"],"url":"https://example.com/plotly.dmg","artifacts":[{"app":["Plotly.app"]}]}`
	officialSync := &catalog.Service{Store: st, Source: officialSource{casks: []string{official}}, Queue: &queue{}}
	_, err = officialSync.Run(ctx)
	require.NoError(t, err)
	body := map[string]any{"cask": "stablyai/orca/orca"}
	_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: body})
	require.Equal(t, domain.CodeConflict, appCode(t, err))
	_, err = admin.Execute(ctx, "AddLinkPackage", management.Input{Body: map[string]any{"token": "existing-link", "name": "Existing Link", "homepage": "https://example.com"}})
	require.NoError(t, err)
	for _, dry := range []bool{false, true} {
		_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "stablyai/orca/orca", "siteToken": "existing-link"}, Params: map[string]any{"dryrun": dry}})
		require.Equal(t, domain.CodeConflict, appCode(t, err), "site tokens also protect link listings")
	}
	body["siteToken"] = "stablyai-orca"
	preview, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: body, Params: map[string]any{"dryrun": true}})
	require.NoError(t, err)
	encoded, err := json.Marshal(preview)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"token":"stablyai-orca"`)
	require.Contains(t, string(encoded), `"fullToken":"stablyai/orca/orca"`)
	var count int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM packages WHERE token='stablyai-orca'").Scan(&count).Error)
	require.Zero(t, count)
	// Exercise the Agent write path, which prepares the candidate before its catalog transaction.
	agent := &domain.Operation{RequestID: uuid.NewString(), Name: "addTapPackage", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, DryRun: true, Execution: &domain.AgentExecution{IdempotencyKey: uuid.NewString()}}
	agentPreview, err := admin.Execute(domain.WithOperation(ctx, agent), "AddTapPackage", management.Input{Body: body})
	require.NoError(t, err)
	encoded, err = json.Marshal(agentPreview)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"token":"stablyai-orca"`)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM tap_casks").Scan(&count).Error)
	require.Zero(t, count)
	added, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: body})
	require.NoError(t, err)
	view := added.(map[string]any)
	id := view["id"].(int64)
	require.Equal(t, "stablyai-orca", view["token"])
	require.Equal(t, "stablyai/orca/orca", view["fullToken"])
	for _, dry := range []bool{false, true} {
		_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: body, Params: map[string]any{"dryrun": dry}})
		require.Equal(t, domain.CodeConflict, appCode(t, err))
		_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "stablyai/orca/orca", "siteToken": "another-orca"}, Params: map[string]any{"dryrun": dry}})
		require.Equal(t, domain.CodeConflict, appCode(t, err), "one upstream registration")
		_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "stablyai/orca/orca", "siteToken": "orca"}, Params: map[string]any{"dryrun": dry}})
		require.Equal(t, domain.CodeConflict, appCode(t, err), "official site identity stays protected")
	}
	_, err = officialSync.Run(ctx)
	require.NoError(t, err)
	read := func() {
		t.Helper()
		p, err := st.PublicPackage(ctx, "cask", "stablyai-orca", "en-US")
		require.NoError(t, err)
		require.Equal(t, id, p.ID)
		require.Equal(t, "stablyai/orca/orca", p.FullToken)
		detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "stablyai-orca", "en-US")
		require.NoError(t, err)
		require.Equal(t, "brew install --cask stablyai/orca/orca", detail.InstallCommand)
		tracked, err := st.TapCask(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "orca", tracked.CaskToken())
		require.Equal(t, "stablyai-orca", tracked.Token)
		plotly, err := st.PublicPackage(ctx, "cask", "orca", "en-US")
		require.NoError(t, err)
		require.Equal(t, "Plotly Orca", plotly.Name)
	}
	read()
	changes, err := (&snapshot.Service{Store: st}).Changes(ctx, 0, 100)
	require.NoError(t, err)
	var found bool
	for _, change := range changes.Changes {
		if change.Token == "stablyai-orca" && change.Item != nil {
			found = true
			require.Equal(t, "orca", *change.Item.BrewToken)
			require.Equal(t, "stablyai/orca", *change.Item.Tap)
		}
	}
	require.True(t, found)
	stats, err := reader.Run(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, stats["unchanged"])
	// The source moves to a sharded path and gains a version. Both lookups keep the upstream token.
	github.commit("Casks/orca.rb", sha(1), "", day.Add(time.Hour))
	github.commit("Casks/o/orca.rb", sha(2), strings.ReplaceAll(string(source), "1.4.244", "1.4.245"), day.Add(2*time.Hour))
	stats, err = reader.Run(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, stats["updated"])
	read()
	tracked, err := st.TapCask(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "1.4.245", tracked.Version)
	require.Equal(t, "Casks/o/orca.rb", tracked.SourcePath)
	_, err = admin.Execute(ctx, "RemoveTapPackage", management.Input{ID: id})
	require.NoError(t, err)
	_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: body})
	require.Equal(t, domain.CodeConflict, appCode(t, err), "explicit site tokens cannot replace removed entries")
	// A live Agent write and its retry keep the same site/upstream separation.
	agent.DryRun, agent.RequestID = false, uuid.NewString()
	agent.Execution.IdempotencyKey = uuid.NewString()
	body = map[string]any{"cask": "stablyai/orca/orca", "siteToken": "orca-agent"}
	result, err := admin.Execute(domain.WithOperation(ctx, agent), "AddTapPackage", management.Input{Body: body})
	require.NoError(t, err)
	encoded, err = json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"token":"orca-agent"`)
	_, err = admin.Execute(domain.WithOperation(ctx, agent), "AddTapPackage", management.Input{Body: body})
	require.NoError(t, err, "identical retries replay the successful add")
	owner, err := st.CaskOwner(ctx, "orca-agent")
	require.NoError(t, err)
	tracked, err = st.TapCask(ctx, owner.ID)
	require.NoError(t, err)
	require.Equal(t, "orca", tracked.CaskToken())
}

func TestTapSiteTokenConcurrentRegistration(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	github := newFakeGitHub()
	github.commit("Casks/swiftvm.rb", sha(0), swiftvm("1", ""), time.Now().UTC().Add(-time.Hour))
	admin := &management.Service{Store: st, Taps: &taps.Service{Store: st, GitHub: github}, Queue: &queue{}}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, site := range []string{"alias-one", "alias-two"} {
		wg.Go(func() {
			_, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm", "siteToken": site}})
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	successes := 0
	for err := range errors {
		if err == nil {
			successes++
		} else {
			require.Equal(t, domain.CodeConflict, appCode(t, err))
		}
	}
	require.Equal(t, 1, successes)
	var count int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM tap_casks").Scan(&count).Error)
	require.EqualValues(t, 1, count)
}
