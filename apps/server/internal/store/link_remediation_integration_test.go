//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/i18n"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func remediationCask(t *testing.T, token string) homebrew.Package {
	t.Helper()
	p, err := homebrew.Normalize("cask", json.RawMessage(fmt.Sprintf(`{"token":%q,"name":["Fixture"],"version":"1","desc":"Fixture"}`, token)))
	require.NoError(t, err)
	return p
}
func remediationLink(t *testing.T, st *store.Store, token string) int64 {
	t.Helper()
	url := "https://example.org/" + token
	id, err := st.WriteLinkApp(context.Background(), 0, domain.LinkInput{Token: token, Name: token, Homepage: &url, Pricing: "free", SourceLocale: "en-US"})
	require.NoError(t, err)
	return id
}
func TestRemediationLinkAlternativesAndCacheCapabilities(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	caps := domain.WithLinks(ctx, true)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "source")}, {Package: remediationCask(t, "cask-target")}}))
	source, err := st.CatalogRow(ctx, "cask", "source")
	require.NoError(t, err)
	cask, err := st.CatalogRow(ctx, "cask", "cask-target")
	require.NoError(t, err)
	link := remediationLink(t, st, "link-target")
	require.NoError(t, st.ReplaceAlternatives(ctx, source.ID, []facts.Alternative{{PackageID: cask.ID, Reasons: []string{"open_source"}, Limits: []string{}}, {PackageID: link, Reasons: []string{"open_source"}, Limits: []string{}}}, "admin"))
	admin, err := st.Alternatives(ctx, source.ID, false)
	require.NoError(t, err)
	require.Len(t, admin, 2)
	require.True(t, admin[1].Available)
	public := catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	for _, order := range [][]context.Context{{caps, ctx}, {ctx, caps}} {
		require.NoError(t, st.Redis.FlushDB(ctx).Err())
		for _, request := range order {
			detail, err := public.Detail(request, "cask", "source", "en-US")
			require.NoError(t, err)
			raw, err := json.Marshal(detail)
			require.NoError(t, err)
			if domain.LinksAllowed(request) {
				require.Len(t, *detail.Alternatives, 2)
				require.Contains(t, string(raw), `"kind":"link"`)
				require.Contains(t, string(raw), `"link":{`)
			} else {
				require.Len(t, *detail.Alternatives, 1)
				require.NotContains(t, string(raw), "link-target")
			}
		}
	}
	linkSource := remediationLink(t, st, "link-source")
	require.NoError(t, st.ReplaceAlternatives(ctx, linkSource, []facts.Alternative{{PackageID: link, Reasons: []string{"open_source"}, Limits: []string{}}, {PackageID: cask.ID, Reasons: []string{"open_source"}, Limits: []string{}}}, "admin"))
	linkDetail, err := public.Detail(caps, "link", "link-source", "en-US")
	require.NoError(t, err)
	require.Len(t, *linkDetail.Alternatives, 2)
	require.Equal(t, "link", string((*linkDetail.Alternatives)[0].Package.Kind))
	for _, stmt := range []string{"UPDATE packages SET disabled=true WHERE id=?", "UPDATE packages SET disabled=false,stale_disabled=true WHERE id=?", "UPDATE packages SET stale_disabled=false,removed_at=now() WHERE id=?"} {
		require.NoError(t, st.DB.Exec(stmt, link).Error)
		rows, err := st.Alternatives(caps, source.ID, true)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		admin, err := st.Alternatives(ctx, source.ID, false)
		require.NoError(t, err)
		require.False(t, admin[1].Available)
	}
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=NULL WHERE id=?", link).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,hidden) VALUES(?,true)", link).Error)
	rows, err := st.Alternatives(caps, source.ID, true)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}
func TestRemediationLinkContentAndTranslation(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	id := remediationLink(t, st, "editable")
	svc := management.Service{Store: st}
	input := management.Input{ID: id, Body: map[string]any{"sourceLocale": "en-US", "summary": "Human source", "description": "Full source"}}
	input.Params = map[string]any{"dryrun": true}
	_, err := svc.Execute(ctx, "UpdatePackageText", input)
	require.NoError(t, err)
	var n int64
	require.NoError(t, st.DB.Table("package_i18n").Where("package_id=?", id).Count(&n).Error)
	require.Zero(t, n)
	input.Params = nil
	_, err = svc.Execute(ctx, "UpdatePackageText", input)
	require.NoError(t, err)
	ref := content.Ref{Entity: "package", ID: id}
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "Human source", source.Fields["summary"])
	_, err = st.ContentView(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`UPDATE i18n_settings SET enabled=true,enabled_locales='["en-US","zh-CN"]',content_types='["package"]'`).Error)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error { return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, ref) }))
	source, err = st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_tasks").Where("kind='translate' AND entity_key=? AND state='open'", fmt.Sprintf("package:%d", id)).Count(&n).Error)
	require.EqualValues(t, len(i18n.SiteLocales)-1, n)
	applied, err := st.WriteTranslationSnapshot(ctx, source, "zh-CN", map[string]string{"summary": "译文", "description": "正文"}, "fixture")
	require.NoError(t, err)
	require.True(t, applied)
	input.Body["summary"] = "Changed source"
	_, err = svc.Execute(ctx, "UpdatePackageText", input)
	require.NoError(t, err)
	applied, err = st.WriteTranslationSnapshot(ctx, source, "zh-CN", map[string]string{"summary": "late"}, "fixture")
	require.NoError(t, err)
	require.False(t, applied)

	denied := domain.WithOperation(ctx, &domain.Operation{AgentClientID: 1, Permissions: []string{"catalog:package:view"}})
	_, err = svc.Execute(denied, "UpdatePackageText", input)
	require.Error(t, err)
	var revisions int64
	require.NoError(t, st.DB.Table("content_revisions").Where("entity='package' AND object_key=?", ref.Key()).Count(&revisions).Error)
	require.Positive(t, revisions)
	require.NoError(t, st.DB.Exec("INSERT INTO collections(id,slug) VALUES(1001,'link-content')").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO collection_items(collection_id,package_id) VALUES(1001,?)", id).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO features(id,placement,target_type,package_id) VALUES(1001,'home_hero','package',?)", id).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO releases(id,package_id,source,source_key,version) VALUES(1001,?,'editorial','one','1')", id).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO assets(id,kind,storage_key,url,mime,bytes,sha256) VALUES(1001,'screenshot','one','https://example.org/one','image/png',1,'fixture')").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_screenshots(id,package_id,asset_id) VALUES(1001,?,1001)", id).Error)
	for entity, field := range map[string]string{"release": "summary", "screenshot": "caption", "collection_item": "note", "feature": "title"} {
		ref := content.Ref{Entity: entity, ID: 1001}
		if entity == "collection_item" {
			ref.SecondaryID = id
		}
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{field: "Link child text"}, true)
		}))
		source, err := st.ReadContentSource(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, "Link child text", source.Fields[field])
		result, err := svc.Execute(ctx, "ListTranslations", management.Input{Params: map[string]any{"entity": entity, "current": 1, "size": 100}})
		require.NoError(t, err)
		require.NotZero(t, result.(map[string]any)["total"])
	}
	result, err := svc.Execute(ctx, "ListTranslations", management.Input{Params: map[string]any{"entity": "package", "current": 1, "size": 100}})
	require.NoError(t, err)
	require.NotZero(t, result.(map[string]any)["total"])
	formula, err := homebrew.Normalize("formula", json.RawMessage(`{"name":"formula","version":"1","versions":{"stable":"1"}}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: formula}}))
	row, err := st.CatalogRow(ctx, "formula", "formula")
	require.NoError(t, err)
	input.ID = row.ID
	_, err = svc.Execute(ctx, "UpdatePackageText", input)
	require.Error(t, err)
	require.NoError(t, st.DB.Exec("UPDATE packages SET kind='formula' WHERE id=?", id).Error)
	for _, entity := range []string{"release", "screenshot", "collection_item", "feature"} {
		ref := content.Ref{Entity: entity, ID: 1001}
		if entity == "collection_item" {
			ref.SecondaryID = id
		}
		_, err := st.ReadContentSource(ctx, ref)
		require.Error(t, err)
	}
}
func TestRemediationRenameMergesAllRelationshipsAndRollsBack(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "old")}}))
	previous, err := st.CatalogRow(ctx, "cask", "old")
	require.NoError(t, err)
	to := previous.ID
	from := remediationLink(t, st, "new")
	late, err := st.LinkApp(ctx, from)
	require.NoError(t, err)
	exec := func(sql string, args ...any) { t.Helper(); require.NoError(t, st.DB.Exec(sql, args...).Error) }
	exec("INSERT INTO package_meta(package_id,developer,hidden,tags,notes) VALUES(?,'canonical',false,ARRAY['old'],'canonical notes'),(?,'source',true,ARRAY['new'],'source notes')", to, from)
	exec("INSERT INTO package_i18n(package_id,locale,summary,status,source) VALUES(?,'en-US','Canonical human','source','human'),(?,'en-US','Source human','source','human'),(?,'ja-JP','保持する','manual','human')", to, from, from)
	exec("INSERT INTO package_facts(package_id,price) VALUES(?,?::jsonb)", from, `{"type":"free","status":"verified","sourceUrl":"https://example.org","verifiedAt":"2026-10-17"}`)
	exec("INSERT INTO categories(id,slug,applies_to,icon) VALUES(1001,'one','cask','lucide:box'),(1002,'two','cask','lucide:box')")
	exec("INSERT INTO package_categories(package_id,category_id,is_primary,source) VALUES(?,1001,true,'human'),(?,1002,true,'human')", to, from)
	exec("INSERT INTO assets(id,url,kind,mime,sha256,storage_key,bytes) VALUES(1001,'https://example.org/image.png','screenshot','image/png','fixture','fixture.png',10)")
	exec("INSERT INTO package_screenshots(id,package_id,asset_id) VALUES(1001,?,1001)", from)
	exec("INSERT INTO screenshot_i18n(screenshot_id,locale,caption,status,source_locale) VALUES(1001,'en-US','Caption','manual','en-US')")
	exec("INSERT INTO package_versions(package_id,version,version_base) VALUES(?,'1','1'),(?,'2','2')", from, from)
	exec("INSERT INTO releases(id,package_id,source,source_key,version,body_markdown,package_version_id) SELECT 1001,?,'editorial','same','1','Canonical release',id FROM package_versions WHERE package_id=? AND version='1'", to, to)
	exec("INSERT INTO releases(id,package_id,source,source_key,version,body_markdown,package_version_id) SELECT 1002,?,'editorial','same','1','Link release',id FROM package_versions WHERE package_id=? AND version='1'", from, from)
	exec("INSERT INTO releases(id,package_id,source,source_key,version,body_markdown,package_version_id) SELECT 1003,?,'editorial','new-build','2','Build two',id FROM package_versions WHERE package_id=? AND version='2'", from, from)
	exec("INSERT INTO release_i18n(release_id,locale,summary,status,source_locale) VALUES(1001,'en-US','Canonical summary','source','en-US'),(1002,'en-US','Archived summary','source','en-US'),(1002,'ja-JP','追加','manual','en-US')")
	exec("INSERT INTO changelog_sources(package_id,type,resolved_by,config) VALUES(?,'sparkle','manual','{\"url\":\"https://example.org/manual\"}'),(?,'sparkle','auto','{}')", from, to)
	exec("INSERT INTO analytics_snapshots(package_id,snapshot_date,installs_30d,installs_90d,installs_365d) VALUES(?,'2026-10-17',1,2,3)", from)
	exec("INSERT INTO collections(id,slug) VALUES(1001,'fixture')")
	exec("INSERT INTO collection_items(collection_id,package_id,source_locale) VALUES(1001,?,'en-US'),(1001,?,'en-US')", to, from)
	exec("INSERT INTO collection_item_i18n(collection_id,package_id,locale,note,status,source_locale) VALUES(1001,?,'en-US','Canonical note','source','en-US'),(1001,?,'en-US','Source note','source','en-US'),(1001,?,'ja-JP','追加','manual','en-US')", to, from, from)
	exec("INSERT INTO features(placement,target_type,package_id) VALUES('home_hero','package',?)", from)
	exec("INSERT INTO feedback(type,package_id,content,platform) VALUES('other',?,'keep','web')", from)
	exec("INSERT INTO package_aliases(kind,token,package_id,source) VALUES('cask','earlier',?,'homebrew')", from)
	exec("INSERT INTO tap_casks(package_id,tap,source_path) VALUES(?,'fixture/tap','Casks/new.rb')", from)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO articles(id,slug,working_revision_id) VALUES(1001,'fixture',1001)").Error; err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO article_revisions(id,article_id,number,source_locale,type) VALUES(1001,1001,1,'en-US','review')").Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO article_revision_packages(revision_id,package_id,sort) VALUES(1001,?,0)", from).Error
	}))
	exec("INSERT INTO package_source_facts(package_id,source,input,status) VALUES(?,'github','fixture','none')", from)
	exec("INSERT INTO update_feeds(bundle_id,source,feed_url,package_id,origin) VALUES('org.fixture.App','sparkle','https://example.org/feed',?,'link')", from)
	others := []int64{}
	for i := 0; i < 9; i++ {
		others = append(others, remediationLink(t, st, fmt.Sprintf("alt-%d", i)))
	}
	for index, id := range others[:8] {
		exec("INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(?,?,?,ARRAY['open_source'],'admin')", to, id, index)
	}
	exec("INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(?,?,0,ARRAY['open_source'],'admin'),(?,?,1,ARRAY['open_source'],'admin'),(?,?,0,ARRAY['open_source'],'admin')", from, others[8], from, to, others[0], from)
	// Every newly registered FK is represented, so adding one without a policy
	// continues to fail this end-to-end merge fixture.
	exec("INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,'attribute','offline','{\"name\":\"offline\",\"booleanValue\":true}','{\"sourceUrl\":\"https://example.org/docs\",\"status\":\"verified\",\"checkedAt\":\"2026-01-01\",\"validUntil\":\"2027-01-01\"}')", from)
	exec("INSERT INTO app_store_price_snapshots VALUES(?,'2026-10-19','US','org.fixture',1,'USD','https://apps.apple.com/app/id1',now())", from)
	exec("INSERT INTO package_cve_checks VALUES(?,'fixture','1','[]',now(),now()+interval '1 day')", from)
	exec("INSERT INTO package_version_requirements VALUES(?,'1','[]','https://example.org/docs',now())", from)
	exec("INSERT INTO background_items VALUES('org.fixture.agent')")
	exec("INSERT INTO background_item_packages VALUES('org.fixture.agent',?,'launchctl','uninstall','https://example.org/docs',now())", from)
	exec("INSERT INTO package_page_state(package_id,locale,page_kind,page_number,content_hash,content_modified_at) VALUES(?,'en-US','overview',1,repeat('a',64),now())", from)
	exec("INSERT INTO seo_urls(url,page_kind,locale,package_id) VALUES('https://example.org/new','overview','en-US',?)", from)
	exec("INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,volume_state,observed_at) VALUES(901,'editor','editor','gsc','google','US','en-US','desktop','2026-10','unknown',now())")
	exec("INSERT INTO seo_keyword_links VALUES(901,?,'price',0.9,'confirmed')", from)
	exec("INSERT INTO admin_users(id,user_name,password_hash) VALUES(901,'merge-reviewer','fixture-not-a-password-hash')")
	exec("INSERT INTO article_test_artifacts(article_id,package_id,payload,sha256,registered_by) VALUES(1001,?,jsonb_build_object('packageId',?::bigint,'appVersion','1'),repeat('c',64),901)", from, from)
	exec("UPDATE article_revisions SET claims=jsonb_build_array(jsonb_build_object('packageId',?::bigint)),seo_target=jsonb_build_object('entityKey',?::text,'intent','price') WHERE id=1001", from, fmt.Sprintf("pkg:%d", from))
	exec("INSERT INTO seo_listing_baselines(package_id,domain,developer,team_id,fact_id,evidence_id,evidence_hash,verified_by,valid_until) VALUES(?,'example.org','Fixture','ABCDEFGHIJ',1,1,repeat('a',64),901,now()+interval '1 day')", from)
	exec("INSERT INTO seo_listing_candidates(token,state,payload,baseline_package_id,package_id) VALUES('new','published',jsonb_build_object('baselinePackageId',?::bigint),?,?)", from, from, from)
	exec("UPDATE seo_listing_candidates c SET payload=payload||jsonb_build_object('link',jsonb_build_object('homepage',l.homepage,'downloadUrl',l.download_url,'teamId',l.team_id)) FROM link_apps l WHERE c.package_id=l.package_id")
	var tables []string
	require.NoError(t, st.DB.Raw("SELECT DISTINCT conrelid::regclass::text FROM pg_constraint WHERE contype='f' AND confrelid='packages'::regclass").Scan(&tables).Error)
	for _, table := range tables {
		var n int64
		require.NoError(t, st.DB.Raw("SELECT count(*) FROM "+table+" WHERE package_id=?", from).Scan(&n).Error)
		require.Positive(t, n, table+" must be represented in the fixture")
	}

	exec("UPDATE package_i18n SET description='Source description' WHERE package_id=? AND locale='en-US'", from)
	exec(`UPDATE i18n_settings SET enabled=true,enabled_locales='["en-US","zh-CN"]',content_types='["package","collection_item","release"]'`)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		for _, ref := range []content.Ref{{Entity: "package", ID: from}, {Entity: "collection_item", ID: 1001, SecondaryID: from}, {Entity: "release", ID: 1002}} {
			if err := (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, ref); err != nil {
				return err
			}
		}
		return nil
	}))
	lateSource, err := st.ReadContentSource(ctx, content.Ref{Entity: "package", ID: from})
	require.NoError(t, err)
	mutation := store.CatalogMutation{Package: remediationCask(t, "new"), Previous: previous, RenamedFrom: "old"}
	// Force a failure after merges but before commit, and compare the archived rows.
	exec(`CREATE FUNCTION reject_rename() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.token='new' AND NEW.kind='cask' THEN RAISE EXCEPTION 'fixture rollback'; END IF; RETURN NEW; END $$`)
	exec("CREATE TRIGGER reject_rename BEFORE UPDATE ON packages FOR EACH ROW EXECUTE FUNCTION reject_rename()")
	require.Error(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{mutation}))
	var n int64
	require.NoError(t, st.DB.Table("packages").Where("id IN ?", []int64{from, to}).Count(&n).Error)
	require.EqualValues(t, 2, n)
	require.NoError(t, st.DB.Table("audit_logs").Where("action='link.adopt.rename'").Count(&n).Error)
	require.Zero(t, n)
	exec("DROP TRIGGER reject_rename ON packages")
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{mutation, {Package: remediationCask(t, "unrelated")}}))
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{mutation}))
	row, err := st.CatalogRow(ctx, "cask", "new")
	require.NoError(t, err)
	require.Equal(t, to, row.ID)
	require.NoError(t, st.DB.Table("packages").Where("id=?", from).Count(&n).Error)
	require.Zero(t, n)
	var audit string
	require.NoError(t, st.DB.Raw("SELECT before::text FROM audit_logs WHERE action='link.adopt.rename'").Scan(&audit).Error)
	require.Contains(t, audit, "Source human")
	require.Contains(t, audit, "Archived summary")
	require.Contains(t, audit, "source notes")
	require.Contains(t, audit, "translate:collection_item:")
	require.Contains(t, audit, "translate:release:1002")
	require.NoError(t, st.DB.Table("audit_logs").Where("action='link.adopt.rename'").Count(&n).Error)
	require.EqualValues(t, 1, n)
	var summary string
	require.NoError(t, st.DB.Raw("SELECT summary FROM package_i18n WHERE package_id=? AND locale='en-US'", to).Scan(&summary).Error)
	require.Equal(t, "Canonical human", summary)

	var description string
	require.NoError(t, st.DB.Raw("SELECT description FROM package_i18n WHERE package_id=? AND locale='en-US'", to).Scan(&description).Error)
	require.Equal(t, "Source description", description)
	applied, err := st.WriteTranslationSnapshot(ctx, lateSource, "zh-CN", map[string]string{"summary": "late"}, "fixture")
	require.NoError(t, err)
	require.False(t, applied)
	require.NoError(t, st.DB.Table("content_translation_sources").Where("entity='package' AND object_key=?", fmt.Sprint(from)).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, st.DB.Table("job_outbox").Where("unique_key=?", fmt.Sprintf("translate:content:package:%d", from)).Count(&n).Error)
	require.Zero(t, n)
	var selected string
	require.NoError(t, st.DB.Raw("SELECT config->>'url' FROM changelog_sources WHERE package_id=? AND type='sparkle'", to).Scan(&selected).Error)
	require.Equal(t, "https://example.org/manual", selected)
	var hidden bool
	require.NoError(t, st.DB.Raw("SELECT hidden FROM package_meta WHERE package_id=?", to).Scan(&hidden).Error)
	require.True(t, hidden)
	require.NoError(t, st.DB.Table("package_categories").Where("package_id=? AND is_primary", to).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, st.DB.Table("package_alternatives").Where("package_id=?", to).Count(&n).Error)
	require.EqualValues(t, 8, n)
	require.NoError(t, st.DB.Table("package_alternatives").Where("package_id=alternative_id OR package_id=? OR alternative_id=?", from, from).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, st.DB.Table("releases").Where("package_id=?", to).Count(&n).Error)
	require.EqualValues(t, 2, n)
	require.NoError(t, st.DB.Table("release_i18n").Where("release_id=1001 AND locale='ja-JP'").Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, st.DB.Table("catalog_changes").Where("package_id=? AND kind='link' AND op='delete'", from).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, st.DB.Table("package_aliases").Where("package_id=? AND token IN ('old','earlier')", to).Count(&n).Error)
	require.EqualValues(t, 2, n)
	require.NoError(t, st.LinkSyncResult(ctx, *late, &domain.GithubRelease{Version: "late", Tag: "late", PublishedAt: time.Now()}, "github_release", "late", "", time.Now(), nil))
	row, err = st.CatalogRow(ctx, "cask", "new")
	require.NoError(t, err)
	require.Equal(t, "1", row.Version)
}

func TestRemediationRenamePreservesOnlyNonemptySourceDocuments(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "empty-old")}}))
	previous, err := st.CatalogRow(ctx, "cask", "empty-old")
	require.NoError(t, err)
	to, from := previous.ID, remediationLink(t, st, "text-new")
	exec := func(sql string, args ...any) { t.Helper(); require.NoError(t, st.DB.Exec(sql, args...).Error) }
	exec("UPDATE packages SET source_locale='ja-JP' WHERE id=?", from)
	exec("INSERT INTO package_i18n(package_id,locale,summary,status,source) VALUES(?,'en-US','  ','source','human'),(?,'ja-JP','保持する','source','human')", to, from)
	exec("INSERT INTO collections(id,slug) VALUES(1001,'merge-source'),(1002,'unchanged-source')")
	exec("INSERT INTO collection_items(collection_id,package_id,source_locale) VALUES(1001,?,'en-US'),(1001,?,'ja-JP'),(1002,?,'en-US')", to, from, to)
	exec("INSERT INTO collection_item_i18n(collection_id,package_id,locale,note,status,source_locale,source_hash) VALUES(1001,?,'en-US','','source','en-US',NULL),(1001,?,'ja-JP','推薦文','source','ja-JP',NULL),(1002,?,'en-US','Untouched','source','en-US','unchanged')", to, from, to)
	exec("INSERT INTO releases(id,package_id,source,source_key,version,source_locale) VALUES(1001,?,'editorial','same','1','en-US'),(1002,?,'editorial','same','1','ja-JP')", to, from)
	exec("INSERT INTO release_i18n(release_id,locale,summary,status,source_locale) VALUES(1001,'en-US','','source','en-US'),(1002,'ja-JP','更新内容','source','ja-JP')")
	exec(`UPDATE i18n_settings SET enabled=true,enabled_locales='["en-US","ja-JP","zh-CN"]',content_types='["package","collection_item","release"]'`)
	oldRefs := []content.Ref{{Entity: "package", ID: from}, {Entity: "collection_item", ID: 1001, SecondaryID: from}, {Entity: "release", ID: 1002}}
	late := []store.ContentSource{}
	for _, ref := range oldRefs {
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error { return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, ref) }))
		source, err := st.ReadContentSource(ctx, ref)
		require.NoError(t, err)
		late = append(late, source)
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "text-new"), Previous: previous, RenamedFrom: "empty-old"}}))
	for _, check := range []struct {
		ref         content.Ref
		field, text string
	}{
		{content.Ref{Entity: "package", ID: to}, "summary", "保持する"},
		{content.Ref{Entity: "collection_item", ID: 1001, SecondaryID: to}, "note", "推薦文"},
		{content.Ref{Entity: "release", ID: 1001}, "summary", "更新内容"},
	} {
		source, err := st.ReadContentSource(ctx, check.ref)
		require.NoError(t, err)
		require.Equal(t, "ja-JP", source.SourceLocale)
		require.Equal(t, check.text, source.Fields[check.field])
		require.Contains(t, source.Targets, "zh-CN")
	}
	for _, source := range late {
		applied, err := st.WriteTranslationSnapshot(ctx, source, "zh-CN", map[string]string{"summary": "late", "note": "late"}, "fixture")
		require.NoError(t, err)
		require.False(t, applied)
		var n int64
		require.NoError(t, st.DB.Table("job_outbox").Where("unique_key=?", "translate:content:"+source.Ref.Entity+":"+source.Ref.Key()).Count(&n).Error)
		require.Zero(t, n)
	}
	var hash string
	require.NoError(t, st.DB.Raw("SELECT source_hash FROM collection_item_i18n WHERE collection_id=1002 AND package_id=?", to).Scan(&hash).Error)
	require.Equal(t, "unchanged", hash)
	lifecycle, found, err := st.PackageLifecycle(ctx, "cask", "empty-old")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, to, lifecycle.ID)
	require.Equal(t, "text-new", lifecycle.Token)
	matches, total, err := st.Search(domain.WithLinks(ctx, true), domain.SearchInput{Kind: "cask", Query: "保持する", Current: 1, Size: 20}, []string{"保持する"}, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, to, matches[0].ID)
	// Both kinds use the Web /apps/:token entry: its cask lookup now resolves
	// the former link token to the retained canonical application.
	public := catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	detail, err := public.Detail(domain.WithLinks(ctx, true), "cask", "text-new", "ja-JP")
	require.NoError(t, err)
	require.Equal(t, "cask", string(detail.Kind))
	changes, err := (&snapshot.Service{Store: st}).Changes(ctx, 0, 100)
	require.NoError(t, err)
	ops := map[string]string{}
	for _, change := range changes.Changes {
		ops[change.Token] = string(change.Op)
	}
	require.Equal(t, map[string]string{"empty-old": "delete", "text-new": "upsert"}, ops)

}
