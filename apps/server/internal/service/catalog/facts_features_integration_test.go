//go:build integration

package catalog_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestFactsFeaturesPrivateFeedbackVersionsAndIntegrationGate(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC()
	add := func(version, min string) {
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"feature-example","name":["Example"],"version":"`+version+`","homepage":"https://vendor.example","depends_on":{"macos":{">=":["`+min+`"]}},"artifacts":[{"app":["Example.app"]}]}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	add("1.0", "13")
	add("2.0", "14")
	var id int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='feature-example'").Scan(&id).Error)
	old, err := st.VersionRequirements(ctx, id, "1.0", now)
	require.NoError(t, err)
	require.NotEmpty(t, old)
	require.NotNil(t, old[0].Minimum)
	require.Equal(t, "13", *old[0].Minimum)
	unknown, err := st.VersionRequirements(ctx, id, "0.5", now)
	require.NoError(t, err)
	require.Empty(t, unknown)
	proof := facts.NewProof("https://vendor.example/support", now, time.Hour, "verified", now)
	issue := facts.Record{Kind: "issue", Key: "import-en", Evidence: proof, Value: json.RawMessage(`{"locale":"en-US","title":"Import stalls","symptom":"Import stops","appVersions":["2.0"],"macosVersions":["15.0"],"steps":["Restart and retry"],"communityLinks":["https://vendor.example/support/import"]}`)}
	integration := facts.Record{Kind: "integration", Key: "connector", Evidence: proof, Value: json.RawMessage(`{"kind":"mcp","name":"Example connector","repositoryUrl":"https://github.com/example/connector","version":"1.0","installation":{"method":"npm","package":"@example/connector"},"clients":["codex"],"permissions":{"files":[{"path":"~/Documents/Example","access":"read"}],"network":[]},"appleSilicon":"unknown","versions":[{"version":"1.0","releasedAt":"2026-01-01T00:00:00Z","sourceUrl":"https://github.com/example/connector/releases/tag/1.0"}]}`)}
	compatibility := facts.Record{Kind: "compatibility", Key: "v2", Evidence: proof, Value: json.RawMessage(`{"appVersion":"2.0","minimumMacOS":"14","maximumMacOS":"15.0","appleSilicon":"yes"}`)}
	require.NoError(t, st.UpsertFactRecords(ctx, id, []facts.Record{issue, integration, compatibility}, now))
	require.Error(t, st.UpsertFactRecords(ctx, 999999, []facts.Record{integration}, now))
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	detail, err := svc.Detail(ctx, "cask", "feature-example", "en-US")
	require.NoError(t, err)
	require.Len(t, *detail.Facts.Issues, 1)
	require.Empty(t, *detail.Facts.Integrations)
	require.NotNil(t, detail.LatestRelease)
	require.NotNil(t, detail.LatestRelease.Compatibility)
	require.Equal(t, "yes", string(detail.LatestRelease.Compatibility.AppleSilicon))
	require.NoError(t, st.DB.Exec(`UPDATE app_config SET value='true' WHERE key='web.appIntegrationsEnabled'`).Error)
	detail, err = svc.Detail(ctx, "cask", "feature-example", "en-US")
	require.NoError(t, err)
	require.Len(t, *detail.Facts.Integrations, 1)
	require.NoError(t, st.DB.Exec(`UPDATE app_config SET value='false' WHERE key='web.appIntegrationsEnabled'`).Error)
	detail, err = svc.Detail(ctx, "cask", "feature-example", "en-US")
	require.NoError(t, err)
	require.Empty(t, *detail.Facts.Integrations)
	now = now.Add(time.Hour)
	detail, err = svc.Detail(ctx, "cask", "feature-example", "en-US")
	require.NoError(t, err)
	require.Empty(t, *detail.Facts.Issues)
	require.Equal(t, "unknown", string(detail.LatestRelease.Compatibility.AppleSilicon))
	require.Nil(t, detail.LatestRelease.Compatibility.MaximumMacOS)
	version, osVersion, symptom, log, contact := "2.0", "15.0", "<b>Crashes</b>", "Authorization: Bearer fixture-secret\n/Users/example-person/Library/logs\nContact person@example.test", "contact@example.test"
	req := publicapi.FeedbackCreateRequest{Type: "install_failed", Platform: "web", Content: "<p>Example crashes on import</p><script>unsafe()</script>", PackageVersion: &version, MacosVersion: &osVersion, Symptom: &symptom, DiagnosticLog: &log, Contact: &contact}
	feedbackID, err := svc.Feedback(ctx, req)
	require.NoError(t, err)
	require.Positive(t, feedbackID)
	var saved struct{ Content, DiagnosticLog, Visibility, PackageVersion, MacosVersion, Symptom, Contact string }
	require.NoError(t, st.DB.Raw("SELECT content,diagnostic_log,visibility,package_version,macos_version,symptom,contact FROM feedback WHERE id=?", feedbackID).Scan(&saved).Error)
	require.Equal(t, "private", saved.Visibility)
	require.Equal(t, "2.0", saved.PackageVersion)
	require.Equal(t, "15.0", saved.MacosVersion)
	require.Equal(t, "Crashes", saved.Symptom)
	require.Equal(t, contact, saved.Contact)
	require.NotContains(t, saved.Content, "unsafe")
	require.NotContains(t, saved.DiagnosticLog, "fixture-secret")
	require.NotContains(t, saved.DiagnosticLog, "example-person")
	require.NotContains(t, saved.DiagnosticLog, "person@example")
	attachments := []string{"https://vendor.example/log.txt"}
	req.Attachments = &attachments
	_, err = svc.Feedback(ctx, req)
	require.Error(t, err)
	req.Attachments = nil
	huge := strings.Repeat("x", 16385)
	req.DiagnosticLog = &huge
	_, err = svc.Feedback(ctx, req)
	require.Error(t, err)
	links := []string{"https://vendor.example/?token=fixture-value"}
	req.DiagnosticLog = nil
	req.Links = &links
	_, err = svc.Feedback(ctx, req)
	require.Error(t, err)
	links = []string{"https://vendor.example/support", "https://vendor.example/docs", "https://vendor.example/download"}
	req.Content = "Downloads fail: http://vendor.example/file?build=2#arm64 https://vendor.example/file?build=3#intel http://localhost/test https://a.example https://b.example\x01<script>unsafe()</script>"
	feedbackID, err = svc.Feedback(ctx, req)
	require.NoError(t, err)
	var plain struct{ Content, Visibility, Links string }
	require.NoError(t, st.DB.Raw("SELECT content,visibility,links::text FROM feedback WHERE id=?", feedbackID).Scan(&plain).Error)
	require.Equal(t, "private", plain.Visibility)
	require.Contains(t, plain.Content, "http://vendor.example/file?build=2#arm64")
	require.Contains(t, plain.Content, "https://vendor.example/file?build=3#intel")
	require.NotContains(t, plain.Content, "unsafe")
	require.NotContains(t, plain.Content, "\x01")
	require.NotContains(t, plain.Content, "<a")
	require.JSONEq(t, `["https://vendor.example/support","https://vendor.example/docs","https://vendor.example/download"]`, plain.Links)
	links = append(links, "https://vendor.example/fourth")
	_, err = svc.Feedback(ctx, req)
	require.Error(t, err)
}

func TestFactsComparisonsAndCollectionExport(t *testing.T) {
	ctx := domain.WithLinks(context.Background(), true)
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC()
	ids := map[string]int64{}
	for _, token := range []string{"compare-one", "compare-two"} {
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","name":["Example"],"version":"1.0","homepage":"https://vendor.example","artifacts":[{"app":["Example.app"]}]}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
		var id int64
		require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token=?", token).Scan(&id).Error)
		ids[token] = id
	}
	homepage := "https://external.example"
	linkID, err := st.WriteLinkApp(ctx, 0, domain.LinkInput{Token: "external-example", Name: "External Example", SourceLocale: "en-US", Homepage: &homepage, Pricing: "unknown", BundleIDs: []string{}})
	require.NoError(t, err)
	proof := facts.NewProof("https://vendor.example/docs", now, time.Hour, "verified", now)
	guide := facts.Migration{TargetPackageID: ids["compare-two"], Locale: "en-US", SourceVersion: "1.0", TargetVersion: "1.0", Steps: []facts.MigrationStep{{Action: "backup", Text: "Back up the source data before exporting.", Evidence: facts.NewProof("https://docs.vendor.example/export", now, 2*time.Hour, "verified", now)}}}
	raw, err := json.Marshal(guide)
	require.NoError(t, err)
	record := facts.Record{Kind: "migration", Key: "to-two", Value: raw, Evidence: proof}
	require.Error(t, st.UpsertFactRecords(ctx, ids["compare-one"], []facts.Record{record}, now), "migration requires an existing alternative relationship")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(?,?,0,ARRAY['offline','free'],'admin')`, ids["compare-one"], ids["compare-two"]).Error)
	guide.Steps[0].Evidence.SourceURL = "https://unrelated.example/export"
	raw, _ = json.Marshal(guide)
	record.Value = raw
	require.Error(t, st.UpsertFactRecords(ctx, ids["compare-one"], []facts.Record{record}, now))
	guide.Steps[0].Evidence.SourceURL = "https://docs.vendor.example/export"
	raw, _ = json.Marshal(guide)
	record.Value = raw
	require.NoError(t, st.UpsertFactRecords(ctx, ids["compare-one"], []facts.Record{record}, now))
	attribute := facts.Record{Kind: "attribute", Key: "offline", Value: json.RawMessage(`{"name":"offline","booleanValue":true}`), Evidence: proof}
	price := facts.Record{Kind: "price", Key: "us", Value: json.RawMessage(`{"region":"US","edition":"Standard","period":"one_time","usage":"paid","downloadFree":true,"amount":"0.10","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor"}`), Evidence: proof}
	require.NoError(t, st.UpsertFactRecords(ctx, ids["compare-two"], []facts.Record{attribute, price}, now))
	price.Value = json.RawMessage(strings.Replace(string(price.Value), "0.10", "0.20", 1))
	require.NoError(t, st.UpsertFactRecords(ctx, ids["compare-one"], []facts.Record{price}, now))
	for _, query := range []string{`INSERT INTO collections(slug,status) VALUES('fact-setup','published')`, `INSERT INTO collection_i18n(collection_id,locale,title) SELECT id,'en-US','Example setup' FROM collections WHERE slug='fact-setup'`, `INSERT INTO collection_items(collection_id,package_id,sort) SELECT c.id,p.id,0 FROM collections c CROSS JOIN packages p WHERE c.slug='fact-setup'`} {
		require.NoError(t, st.DB.Exec(query).Error)
	}
	var collectionID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM collections WHERE slug='fact-setup'").Scan(&collectionID).Error)
	require.NoError(t, st.SaveCollectionCriteria(ctx, collectionID, store.CollectionCriteria{Locale: "en-US", Criteria: "Chosen for local document editing and an available official download.", Evidence: proof}))
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	detail, err := svc.Detail(ctx, "cask", "compare-one", "en-US")
	require.NoError(t, err)
	require.Len(t, detail.Facts.Alternatives, 1)
	alt := detail.Facts.Alternatives[0]
	require.Equal(t, []publicapi.PackageAlternativeReasons{"offline"}, alt.Reasons)
	require.NotNil(t, alt.Migration)
	collection, err := svc.Collection(ctx, "fact-setup", "en-US")
	require.NoError(t, err)
	require.NotNil(t, collection.Setup)
	require.ElementsMatch(t, []string{"compare-one", "compare-two"}, collection.Setup.InstallableTokens)
	require.Len(t, collection.Setup.ExternalApps, 1)
	require.Equal(t, "external-example", collection.Setup.ExternalApps[0].Token)
	require.Equal(t, "0.3", collection.Setup.PriceTotals[0].Amount)
	require.Equal(t, 1, collection.Setup.PriceUnknownCount)
	brewfile, err := svc.Brewfile(ctx, "fact-setup")
	require.NoError(t, err)
	require.Equal(t, collection.Setup.Brewfile, brewfile)
	require.NotContains(t, brewfile, "external-example")
	records, err := st.FactRecords(ctx, linkID)
	require.NoError(t, err)
	require.Empty(t, records)
	now = now.Add(time.Hour)
	detail, err = svc.Detail(ctx, "cask", "compare-one", "en-US")
	require.NoError(t, err)
	require.Nil(t, detail.Facts.Alternatives[0].Migration, "parent evidence expiration also withdraws cached migration")
	require.Empty(t, detail.Facts.Alternatives[0].Reasons)
}

func TestFactsManagementDryRunAuditAndAtomicValidation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC()
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"management-example","name":["Example"],"version":"1.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	row, err := st.CatalogRow(ctx, "cask", "management-example")
	require.NoError(t, err)
	observation := facts.Record{Kind: "price", Key: "us-pro", Value: json.RawMessage(`{"region":"US","edition":"Pro","period":"one_time","usage":"paid","downloadFree":true,"amount":"19.99","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor"}`), Evidence: facts.NewProof("https://vendor.example/pricing", now, time.Hour, "verified", now)}
	raw, _ := json.Marshal(map[string]any{"items": []facts.Record{observation}})
	body := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &body))
	svc := &management.Service{Store: st, Now: func() time.Time { return now }}
	_, err = svc.Execute(ctx, "SetPackageFactRecords", management.Input{ID: row.ID, Body: body, Params: map[string]any{"dryrun": true}})
	require.NoError(t, err)
	records, err := st.FactRecords(ctx, row.ID)
	require.NoError(t, err)
	require.Empty(t, records)
	_, err = svc.Execute(ctx, "SetPackageFactRecords", management.Input{ID: row.ID, Body: body})
	require.NoError(t, err)
	records, err = st.FactRecords(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, records, 1)
	var revisions int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_revisions WHERE entity='package' AND object_key=?", strconv.FormatInt(row.ID, 10)).Scan(&revisions).Error)
	require.EqualValues(t, 1, revisions)
	body["items"].([]any)[0].(map[string]any)["value"].(map[string]any)["unsafeCommand"] = "fixture-invalid"
	_, err = svc.Execute(ctx, "SetPackageFactRecords", management.Input{ID: row.ID, Body: body})
	require.Error(t, err)
	records, err = st.FactRecords(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.NotContains(t, string(records[0].Value), "unsafeCommand")
}
