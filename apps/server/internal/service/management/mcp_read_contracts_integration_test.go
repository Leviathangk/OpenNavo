//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/desktop"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestMCPReadContracts(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://audit.example"}, Now: func() time.Time { return time.Now().UTC() }}
	require.NoError(t, st.DB.Exec("INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(90001,'link','audit-link','audit-link','','Audit Link','1.0','1.0','{}','audit')").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_versions(package_id,version,version_base,first_seen_at) VALUES(90001,'1.0','1.0',now())").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO sync_runs(job_type,trigger,status) VALUES('catalog:sync','schedule','succeeded')").Error)
	t.Run("schema_valid_job_type_filter", func(t *testing.T) {
		all, err := svc.read(ctx, "ListJobRuns", Input{Params: map[string]any{}})
		require.NoError(t, err)
		filtered, err := svc.read(ctx, "ListJobRuns", Input{Params: map[string]any{"jobtype": "catalog_sync"}})
		require.NoError(t, err)
		raw, err := svc.read(ctx, "ListJobRuns", Input{Params: map[string]any{"jobtype": "catalog:sync"}})
		require.NoError(t, err)
		t.Logf("unfiltered=%v allowed_enum_filter=%v database_spelling_filter=%v", hermesMap(all)["total"], hermesMap(filtered)["total"], hermesMap(raw)["total"])
		require.EqualValues(t, 1, hermesMap(filtered)["total"], "the advertised catalog_sync value must match its stored catalog:sync run")
		records := hermesMap(filtered)["records"].([]any)
		require.Equal(t, "catalog_sync", records[0].(map[string]any)["jobType"])
	})
	t.Run("all_versions_includes_link_versions", func(t *testing.T) {
		byPackage, err := svc.versions(ctx, Input{ID: 90001, Params: map[string]any{}})
		require.NoError(t, err)
		all, err := svc.adminVersions(ctx, Input{Params: map[string]any{"packageid": float64(90001)}})
		require.NoError(t, err)
		t.Logf("package_versions_total=%v releases_list_versions_total=%v", hermesMap(byPackage)["total"], hermesMap(all)["total"])
		require.EqualValues(t, 1, hermesMap(all)["total"], "the all-recorded-versions query must not lose recorded link versions")
	})
}

func TestMCPOrdinaryReadRevisions(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	svc := &Service{Store: st, Desktop: &desktop.Service{Store: st}}
	require.NoError(t, st.DB.Exec(`
INSERT INTO desktop_releases(id,version,artifacts,source_locale) VALUES(777,'1.0','[]','en-US');
INSERT INTO i18n_glossary(id,term,translations) VALUES(777,'revision-fixture','{}');
INSERT INTO search_synonyms(id,terms,enabled) VALUES(777,ARRAY['first','second'],true);
INSERT INTO feedback(id,type,content,platform) VALUES(777,'other','Fixture','web');
INSERT INTO article_authors(id,slug,name,source_locale) VALUES(777,'revision-author','Author','en-US');
INSERT INTO collections(id,slug,source_locale) VALUES(777,'revision-collection','en-US')`).Error)
	for _, mutation := range []struct {
		name string
		body map[string]any
	}{
		{"UpdateDesktopReleaseNotes", map[string]any{"sourceLocale": "en-US", "notes": "Read revision fixture"}},
		{"UpsertGlossaryTerm", map[string]any{"term": "revision-fixture", "translations": map[string]any{"de-DE": "Arbeitsbereich"}, "doNotTranslate": false}},
		{"UpdateSynonym", map[string]any{"terms": []any{"first", "second", "third"}, "enabled": true}},
		{"UpdateFeedback", map[string]any{"status": "open", "handlerNote": "Read revision fixture"}},
		{"UpdateArticleAuthor", map[string]any{"slug": "revision-author", "name": "Updated author", "sourceLocale": "en-US", "links": []any{}}},
	} {
		_, err := svc.Execute(ctx, mutation.name, Input{ID: 777, Body: mutation.body})
		require.NoError(t, err, mutation.name)
	}
	for _, tc := range []struct{ read, write string }{
		{"GetDesktopRelease", "UpdateDesktopReleaseNotes"}, {"ListDesktopReleases", "UpdateDesktopReleaseNotes"},
		{"ListGlossary", "DeleteGlossaryTerm"}, {"ListSynonyms", "UpdateSynonym"},
		{"GetFeedback", "UpdateFeedback"}, {"ListFeedback", "UpdateFeedback"},
		{"ListArticleAuthors", "UpdateArticleAuthor"},
	} {
		t.Run(tc.read, func(t *testing.T) {
			result, err := svc.Execute(ctx, tc.read, Input{ID: 777})
			require.NoError(t, err)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			var decoded any
			require.NoError(t, json.Unmarshal(raw, &decoded))
			var record map[string]any
			if tc.read == "ListArticleAuthors" {
				record = decoded.([]any)[0].(map[string]any)
			} else {
				record = decoded.(map[string]any)
				if strings.HasPrefix(tc.read, "List") {
					record = record["records"].([]any)[0].(map[string]any)
				}
			}
			expected, err := svc.CurrentRevision(ctx, tc.write, Input{ID: 777})
			require.NoError(t, err)
			require.Positive(t, expected)
			require.EqualValues(t, expected, record["revision"])
		})
	}
	now := time.Now().UTC()
	criteria := map[string]any{"locale": "zh-CN", "criteria": strings.Repeat("字", 10000), "evidence": map[string]any{"sourceUrl": "https://vendor.example/docs", "checkedAt": now.Format(time.RFC3339), "validUntil": now.Add(time.Hour).Format(time.RFC3339), "status": "verified"}}
	_, err := svc.Execute(ctx, "SetCollectionSetupCriteria", Input{ID: 777, Body: criteria})
	require.NoError(t, err)
	criteria["criteria"] = strings.Repeat("字", 10001)
	_, err = svc.Execute(ctx, "SetCollectionSetupCriteria", Input{ID: 777, Body: criteria})
	require.ErrorContains(t, err, "field_too_long:criteria")
}

func TestCatalogReadScopesAndSharedReleaseBase(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://audit.example"}, Now: func() time.Time { return time.Now().UTC() }}
	require.NoError(t, st.DB.Exec("INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(90002,'cask','audit-build','audit-build','homebrew/cask','Audit Build','2.0,42','2.0','{}','audit')").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_versions(package_id,version,version_base,first_seen_at) VALUES(90002,'2.0,42','2.0',now())").Error)
	versions, err := svc.versions(ctx, Input{ID: 90002, Params: map[string]any{}})
	require.NoError(t, err)
	target, found, err := st.ResolveReleaseNotesTarget(ctx, 90002, "2.0")
	require.NoError(t, err)
	require.True(t, found)
	t.Logf("base_version_not_listed_but_accepted: package_versions=%v target_base=%v build_id=%v", hermesMap(versions)["records"], target.Base, target.PackageVersion)
	require.NoError(t, st.AppendContentChange(ctx, 90002))
	events, err := svc.catalogActivity(ctx, Input{Params: map[string]any{"afterseq": float64(0)}})
	require.NoError(t, err)
	var contentChanges int64
	require.NoError(t, st.DB.Table("catalog_changes").Where("reason='content'").Count(&contentChanges).Error)
	t.Logf("editorial_edit_history_check: content_changes_in_database=%v catalog_changes_visible_total=%v", contentChanges, hermesMap(events)["total"])
	require.EqualValues(t, 1, contentChanges)
	require.EqualValues(t, 0, hermesMap(events)["total"])
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE id=90002").Error)
	filtered, err := svc.packages(ctx, Input{Params: map[string]any{"disabled": false, "hidden": false, "isfont": false, "removed": false, "gaps": []string{"latestEditorial"}}})
	require.NoError(t, err)
	require.EqualValues(t, 0, hermesMap(filtered)["total"], "ordinary worklists exclude removed apps")
	removed, err := svc.packages(ctx, Input{Params: map[string]any{"removed": true, "gaps": []string{"latestEditorial"}}})
	require.NoError(t, err)
	require.EqualValues(t, 1, hermesMap(removed)["total"], "removed apps remain explicitly queryable")
}
