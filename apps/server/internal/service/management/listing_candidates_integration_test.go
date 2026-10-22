//go:build integration

package management

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/stretchr/testify/require"
)

func listingEvidence(t *testing.T, st *store.Store, entity, body, link, authority string) agentgate.Evidence {
	t.Helper()
	e := agentgate.Evidence{EntityKey: entity, SourceURL: link, FinalURL: link, BodyText: body, Excerpt: body, BodyHash: seotasks.Hash([]byte(body)), FetchedAt: time.Now().UTC(), ValidUntil: time.Now().Add(time.Hour), Authority: authority, FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: seotasks.Hash([]byte("fixture"))}
	require.NoError(t, st.DB.Create(&e).Error)
	return e
}
func listingBody(t *testing.T, in any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var b map[string]any
	require.NoError(t, json.Unmarshal(raw, &b))
	return b
}

func TestListingWorkspaceIsolationAndDirectPublication(t *testing.T) {
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st}
	actor := int64(1)
	ctx := domain.WithOperation(context.Background(), &domain.Operation{ActorID: &actor, Permissions: []string{"*"}, RequestID: uuid.NewString()})
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'listing-admin','isolated-test');
 INSERT INTO packages(id,kind,token,full_token,tap,name,homepage,version,version_base,raw,raw_hash) VALUES(80,'cask','baseline','baseline','','Baseline','https://vendor.example.test','1','1','{}','');
 INSERT INTO categories(id,slug,icon,applies_to) VALUES(80,'listing-editors','lucide:box','cask');
 INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(80,'icon','listing-icon','https://assets.example.test/icon.png',100,100,1,'hash','image/png')`).Error)
	body := "Example Software EXAMPL1234 Example App An editor for macOS."
	baselineEvidence := listingEvidence(t, st, "pkg:80", body, "https://vendor.example.test/identity", "official")
	proof := `{"sourceUrl":"https://vendor.example.test/identity","checkedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","validUntil":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `","status":"verified"}`
	require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(id,package_id,kind,key,value,evidence) VALUES(80,80,'security','developer','{"developer":"Example Software","teamId":"EXAMPL1234","channels":["vendor"],"openSource":"unknown"}',?::jsonb)`, proof).Error)
	baselineInput := Input{ID: 80, Body: map[string]any{"factId": float64(80), "evidenceId": baselineEvidence.ID, "domainOwnershipConfirmed": true, "suspicious": false}}
	_, err := s.Execute(ctx, "LockSeoListingBaseline", baselineInput)
	require.NoError(t, err)
	// Neither a credential with an admin ActorID nor a baseline fact can self-attest as Agent.
	agentCtx := domain.WithOperation(ctx, &domain.Operation{ActorID: &actor, AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"*"}})
	_, err = s.lockListingBaseline(agentCtx, baselineInput)
	require.Error(t, err)
	homepage, download, team := "https://vendor.example.test", "https://vendor.example.test/download", "EXAMPL1234"
	e := listingEvidence(t, st, "listing:example-app", body, download, "reference")
	req := store.ListingInput{Action: "save", Link: domain.LinkInput{Token: "example-app", Name: "Example App", Homepage: &homepage, DownloadURL: &download, TeamID: &team, Pricing: "unknown", SourceLocale: "en-US", BundleIDs: []string{"test.example.App"}}, Summary: "An editor for macOS.", Description: "An editor for macOS.", CategoryID: 80, IconAssetID: 80, BaselinePackageID: 80, DownloadMode: "official_page", EvidenceID: e.ID}
	write := func() (any, error) {
		return s.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: req.Link.Token, Body: listingBody(t, req)})
	}
	_, err = write()
	require.NoError(t, err)
	// Structural isolation: every public exporter is package-rooted.
	var count int64
	require.NoError(t, st.DB.Table("packages").Where("token=?", req.Link.Token).Count(&count).Error)
	require.Zero(t, count)
	_, found, err := st.IndexRowByToken(ctx, req.Link.Token)
	require.NoError(t, err)
	require.False(t, found)
	links, err := st.LinkApps(ctx, true)
	require.NoError(t, err)
	require.Empty(t, links)
	req.ExpectedRevision = 1
	req.Action = "publish"
	// An installer URL, a different download host and a suspicious optional
	// baseline no longer block an editor's publication decision.
	download = "https://downloads.example.test/App.dmg"
	req.Link.DownloadURL = &download
	require.NoError(t, st.DB.Exec("UPDATE seo_listing_baselines SET suspicious=true WHERE package_id=80").Error)
	value, err := write()
	require.NoError(t, err)
	view := value.(map[string]any)
	require.Equal(t, "published", view["state"])
	require.Equal(t, false, view["canDirectDownload"])
	require.NotNil(t, view["indexing"])
	c, err := st.ListingCandidate(ctx, "example-app")
	require.NoError(t, err)
	require.NotNil(t, c.PackageID)
	require.NoError(t, st.DB.Exec("UPDATE seo_listing_baselines SET suspicious=false WHERE package_id=80").Error)
	verified, err := s.listingView(ctx, st, req.Link.Token)
	require.NoError(t, err)
	require.Equal(t, true, verified["identityVerified"])
	require.NoError(t, st.DB.Exec("UPDATE link_apps SET team_id='CHANGED123' WHERE package_id=?", *c.PackageID).Error)
	changed, err := s.listingView(ctx, st, req.Link.Token)
	require.NoError(t, err)
	require.Equal(t, false, changed["identityVerified"])
	app, err := st.LinkApp(ctx, *c.PackageID)
	require.NoError(t, err)
	require.Equal(t, download, *app.DownloadURL)
	for _, fetchError := range []error{context.DeadlineExceeded, errors.New("HTTP 429")} {
		require.NoError(t, st.LinkSyncResult(ctx, *app, nil, "", "", "", time.Now(), fetchError))
		after, err := st.LinkApp(ctx, app.ID)
		require.NoError(t, err)
		require.False(t, after.Removed)
	}
	// All writers can update published candidate identity and download fields.
	require.NoError(t, st.DB.Exec("UPDATE link_apps SET download_url='https://vendor.example.test/App.pkg' WHERE package_id=?", app.ID).Error)
	require.NoError(t, st.DB.Exec("UPDATE link_apps SET bundle_ids=ARRAY['test.rogue.App'] WHERE package_id=?", app.ID).Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET name='Unverified developer app' WHERE id=?", app.ID).Error)
	_, err = write()
	require.Error(t, err) // An explicitly stale revision still conflicts.
	req.Link.Token = "other-app"
	req.Link.Name = "Other App"
	req.ExpectedRevision = 0
	req.DownloadMode = "not_applicable"
	req.Link.DownloadURL = nil
	req.EvidenceID = listingEvidence(t, st, "listing:other-app", "Example Software EXAMPL1234 Other App An editor for macOS.", homepage, "reference").ID
	_, err = write()
	require.NoError(t, err) // Shared bundle aliases no longer veto listing.
	req.ExpectedRevision = 1
	req.Link.BundleIDs = []string{"test.example.Other"}
	_, err = write()
	require.NoError(t, err) // Store/purchase-only does not prevent listing.
}

func TestListingAgentLiveOverrideReplayAndRevision(t *testing.T) {
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st}
	ctx := context.Background()
	tasks := &seotasks.Service{DB: st.DB}
	home := "https://vendor.example.test"
	req := store.ListingInput{Action: "save", Link: domain.LinkInput{Token: "lease-app", Name: "Lease App", Homepage: &home, SourceLocale: "en-US", Pricing: "unknown"}, DownloadMode: "not_applicable"}
	e := listingEvidence(t, st, "listing:lease-app", "Lease App supports macOS.", home, "reference")
	req.EvidenceID = e.ID
	task, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "listing", EntityKey: "listing:lease-app", DedupeKey: uuid.NewString(), RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	require.NotZero(t, task.ID)
	claimed, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"listing"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	op := &domain.Operation{Name: "upsertSeoListingCandidate", RequestID: uuid.NewString(), AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, Execution: &domain.AgentExecution{TaskID: claimed[0].ID, Fencing: claimed[0].Fencing, IdempotencyKey: uuid.NewString(), EvidenceIDs: []int64{e.ID}}}
	in := Input{Key: "lease-app", Body: listingBody(t, req)}
	value, err := s.Execute(domain.WithOperation(ctx, op), "UpsertSeoListingCandidate", in)
	require.NoError(t, err)
	require.Equal(t, "candidate", value.(map[string]any)["state"])
	candidate, err := st.ListingCandidate(ctx, in.Key)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	_, err = s.Execute(domain.WithOperation(ctx, op), "UpsertSeoListingCandidate", in)
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("seo_actions").Count(&count).Error)
	require.Zero(t, count)
	_, err = tasks.Complete(ctx, op.Execution.TaskID, op.Execution.Fencing, seotasks.Owner(1, 2), "done", "")
	require.NoError(t, err)
	// Live override works even with proposal configured. Further writes check revisions.
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET automation=jsonb_set(automation,'{actions,listing,mode}','"live"')`).Error)
	_, err = tasks.Enqueue(ctx, seotasks.NewTask{Kind: "listing", EntityKey: "listing:lease-app", DedupeKey: uuid.NewString(), RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	claimed, err = tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"listing"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	op.Execution.TaskID = claimed[0].ID
	op.Execution.Fencing = claimed[0].Fencing
	op.Execution.IdempotencyKey = uuid.NewString()
	op.RequestID = uuid.NewString()
	op.Execution.ExpectedRevision = 1
	in.Body["expectedRevision"] = float64(1)
	_, err = s.Execute(domain.WithOperation(ctx, op), "UpsertSeoListingCandidate", in)
	require.NoError(t, err)
	candidate, err = st.ListingCandidate(ctx, in.Key)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	require.EqualValues(t, 2, candidate.Revision)
	_, err = s.Execute(domain.WithOperation(ctx, op), "UpsertSeoListingCandidate", in)
	require.NoError(t, err)
	in.Body["summary"] = "changed payload"
	_, err = s.Execute(domain.WithOperation(ctx, op), "UpsertSeoListingCandidate", in)
	require.Error(t, err)

	_, err = tasks.Complete(ctx, op.Execution.TaskID, op.Execution.Fencing, seotasks.Owner(1, 2), "done", "")
	require.NoError(t, err)
	_, err = tasks.Enqueue(ctx, seotasks.NewTask{Kind: "listing", EntityKey: "listing:lease-app", DedupeKey: uuid.NewString(), RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	claimed, err = tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"listing"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	op.Execution.TaskID = claimed[0].ID
	op.Execution.Fencing = claimed[0].Fencing
	op.Execution.IdempotencyKey = uuid.NewString()
	_, err = s.Execute(domain.WithOperation(ctx, op), "UpsertSeoListingCandidate", in)
	require.Error(t, err)
	stillLeased, err := tasks.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, "leased", stillLeased.State)
	op.Execution.ExpectedRevision = 2
	in.Body["expectedRevision"] = float64(2)
	_, err = s.Execute(domain.WithOperation(ctx, op), "UpsertSeoListingCandidate", in)
	require.NoError(t, err)
	_, err = s.linkOperation(domain.WithOperation(ctx, op), "AddLinkPackage", Input{})
	require.Error(t, err, "ordinary required link fields still apply")
}

func TestPublicationMigrationsUpAndDown(t *testing.T) {
	st := testutil.NewSEOStore(t)
	names := []string{"00098_direct_agent_writes.sql", "00056_article_evidence_artifacts.sql", "00055_seo_listing_candidates.sql"}
	for _, name := range names {
		raw, err := migrations.Files.ReadFile(name)
		require.NoError(t, err)
		parts := strings.Split(string(raw), "-- +goose Down")
		require.Len(t, parts, 2)
		require.NoError(t, st.DB.Exec(parts[1]).Error, name+" down")
	}
	for i := len(names) - 1; i >= 0; i-- {
		raw, err := migrations.Files.ReadFile(names[i])
		require.NoError(t, err)
		require.NoError(t, st.DB.Exec(strings.Split(string(raw), "-- +goose Down")[0]).Error, names[i]+" up")
	}

}

func TestListingPublicationWithoutHumanBaseline(t *testing.T) {
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st}
	actor := int64(1)
	ctx := domain.WithOperation(context.Background(), &domain.Operation{ActorID: &actor, Permissions: []string{"*"}, RequestID: uuid.NewString()})
	require.NoError(t, st.DB.Exec(`INSERT INTO admin_users(id,user_name,password_hash) VALUES(1,'editor','fixture');
 INSERT INTO categories(id,slug,icon,applies_to) VALUES(80,'editors','lucide:box','cask');
 INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(80,'icon','icon','https://assets.example.test/icon.png',100,100,1,'hash','image/png')`).Error)
	homepage := "https://vendor.example.test"
	e := listingEvidence(t, st, "listing:independent-app", "Independent App is an editor for macOS.", homepage, "reference")
	req := store.ListingInput{Action: "publish", Link: domain.LinkInput{Token: "independent-app", Name: "Independent App", BundleIDs: []string{"test.example.Independent"}, Homepage: &homepage, Pricing: "unknown", SourceLocale: "en-US"}, Summary: "A paraphrased description of the editor.", CategoryID: 80, IconAssetID: 80, DownloadMode: "not_applicable", EvidenceID: e.ID}
	result, err := s.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: req.Link.Token, Body: listingBody(t, req)})
	require.NoError(t, err)
	view := result.(map[string]any)
	require.Equal(t, "published", view["state"])
	require.Equal(t, false, view["identityVerified"])
	var count int64
	require.NoError(t, st.DB.Table("package_meta").Where("package_id=?", view["packageId"]).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Published candidates retain their package ID and can be corrected in place.
	originalID := view["packageId"]
	require.NoError(t, st.DB.Exec("UPDATE package_meta SET developer='Independent Vendor' WHERE package_id=?", originalID).Error)
	req.ExpectedRevision = 1
	req.Link.Name = "Independent App Renamed"
	homepage = "https://new-vendor.example.test"
	req.EvidenceID = listingEvidence(t, st, "listing:independent-app", "New vendor details", homepage, "reference").ID
	result, err = s.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: req.Link.Token, Body: listingBody(t, req)})
	require.NoError(t, err)
	view = result.(map[string]any)
	require.EqualValues(t, 2, view["revision"])
	require.Equal(t, originalID, view["packageId"])
	var saved string
	require.NoError(t, st.DB.Raw("SELECT name FROM packages WHERE id=?", originalID).Scan(&saved).Error)
	require.Equal(t, req.Link.Name, saved)
	require.NoError(t, st.DB.Raw("SELECT developer FROM package_meta WHERE package_id=?", originalID).Scan(&saved).Error)
	require.Equal(t, "Independent Vendor", saved, "publishing without a baseline keeps the known developer")
	// A stale editor cannot replace the newly published snapshot.
	_, err = s.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: req.Link.Token, Body: listingBody(t, req)})
	require.Error(t, err)

	// A late metadata failure rolls back both the identity and candidate snapshot.
	before, err := st.ListingCandidate(ctx, req.Link.Token)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(81,'icon','rollback-icon','https://assets.example.test/rollback.png',100,100,1,'rollback','image/png'); ALTER TABLE package_meta ADD CONSTRAINT fixture_reject_icon CHECK(icon_asset_id<>81)`).Error)
	req.ExpectedRevision = 2
	req.Link.Name = "This name must roll back"
	req.IconAssetID = 81
	_, err = s.Execute(ctx, "UpsertSeoListingCandidate", Input{Key: req.Link.Token, Body: listingBody(t, req)})
	require.Error(t, err)
	after, err := st.ListingCandidate(ctx, req.Link.Token)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.JSONEq(t, string(before.Payload), string(after.Payload))
	require.NoError(t, st.DB.Raw("SELECT name FROM packages WHERE id=?", originalID).Scan(&saved).Error)
	require.Equal(t, "Independent App Renamed", saved)

}
