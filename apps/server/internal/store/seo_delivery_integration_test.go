//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntentTargetsEvidenceAndCollectionInvalidation(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	home := "https://vendor.example/download"
	input := domain.LinkInput{Token: "example", Name: "Example", Homepage: &home, SourceLocale: "zh-Hant"}
	require.NoError(t, input.Validate())
	require.Equal(t, "unknown", input.Pricing)
	var id int64
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = (&store.Store{DB: tx}).WriteLinkApp(ctx, 0, input)
		return err
	}))
	var version string
	require.NoError(t, st.DB.Raw("SELECT version FROM packages WHERE id=?", id).Scan(&version).Error)
	require.Empty(t, version)
	cards, err := st.LiveCaskIDs(ctx, []string{"example"})
	require.NoError(t, err)
	require.Equal(t, id, cards["example"])
	require.NoError(t, st.DB.Exec(`UPDATE packages SET source_locale='en-US',desc_en='An independently useful application',version='1' WHERE id=?`, id).Error)
	for n := 0; n < 3; n++ {
		var target int64
		require.NoError(t, st.DB.Raw(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES('cask',?,?,'homebrew/cask','Alternative','1','1','{}','fixture') RETURNING id`, fmt.Sprint("alt-", n), fmt.Sprint("alt-", n)).Scan(&target).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(?,?,?,ARRAY['open_source'],'admin')`, id, target, n).Error)
		proof := facts.NewProof("https://vendor.example/docs", time.Now().Add(-time.Hour), 24*time.Hour, "verified", time.Now())
		raw, _ := json.Marshal(proof)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,'attribute','open-source','{"name":"open_source","booleanValue":true}',?::jsonb)`, target, string(raw)).Error)
	}
	var keyword int64
	require.NoError(t, st.DB.Raw(`INSERT INTO seo_keywords(keyword,normalized,source,engine,country,language,device,period,search_volume,volume_state,observed_at) VALUES('example alternative','example alternative','site_search','site','US','en-US','all','2026-10',10,'known',now()) RETURNING id`).Scan(&keyword).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES(?,?,'alternative',1,'confirmed')`, keyword, id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_targets(entity_key,intent,locale,target_url) VALUES(?,'alternative','en-US','https://site.example/apps/example/alternatives')`, fmt.Sprint("pkg:", id)).Error)
	p := seo.Policy{Mode: "shadow", IntentPagesEnabled: true}
	decide := func() seo.PageDecision {
		row, ok, err := st.IndexRowByID(ctx, id)
		require.NoError(t, err)
		require.True(t, ok)
		rows := []store.IndexRow{row}
		require.NoError(t, st.PopulateIntentEligibility(ctx, rows, p, "https://site.example"))
		return seo.DecidePage(rows[0].Facts(), seo.PageRef{Kind: seo.PageAlternatives, Locale: "en-US"}, p)
	}
	require.True(t, decide().InSitemap)
	require.NoError(t, st.DB.Exec(`UPDATE package_fact_records SET evidence=jsonb_set(evidence,'{status}','"needs_review"') WHERE package_id=(SELECT alternative_id FROM package_alternatives WHERE package_id=? ORDER BY sort LIMIT 1)`, id).Error)
	require.False(t, decide().InSitemap)
	require.NoError(t, st.DB.Exec(`UPDATE package_fact_records SET evidence=jsonb_set(evidence,'{status}','"verified"')`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_targets SET target_url='https://site.example/articles/other'`).Error)
	require.False(t, decide().InSitemap)
	var cid int64
	require.NoError(t, st.DB.Raw(`INSERT INTO collections(slug,status) VALUES('old','published') RETURNING id`).Scan(&cid).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO collection_items(collection_id,package_id) VALUES(?,?)`, cid, id).Error)
	require.NoError(t, st.DB.Exec("DELETE FROM job_outbox WHERE job_type='cache:invalidate'").Error)
	require.NoError(t, st.DB.Exec("DELETE FROM content_url_changes").Error)
	require.NoError(t, st.DB.Exec("UPDATE collections SET slug='new' WHERE id=?", cid).Error)
	var paths string
	require.NoError(t, st.DB.Raw("SELECT string_agg(array_to_string(paths,','),',') FROM content_url_changes").Scan(&paths).Error)
	require.Contains(t, paths, "/collections/old")
	require.Contains(t, paths, "/collections/new")
	var patterns string
	require.NoError(t, st.DB.Raw(`SELECT string_agg(payload::text,',') FROM job_outbox WHERE job_type='cache:invalidate'`).Scan(&patterns).Error)
	require.Contains(t, patterns, "c:pkg:*:link:example:*")
	require.NotContains(t, patterns, `"c:pkg:*"`)
	require.NoError(t, st.DB.Exec("DELETE FROM content_url_changes").Error)
	require.NoError(t, st.DB.Exec("UPDATE collections SET updated_at=now() WHERE id=?", cid).Error)
	var unchanged int64
	require.NoError(t, st.DB.Table("content_url_changes").Count(&unchanged).Error)
	require.Zero(t, unchanged)
	require.NoError(t, st.DB.Exec("DELETE FROM content_url_changes").Error)
	require.Error(t, st.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM collections WHERE id=?", cid).Error; err != nil {
			return err
		}
		return errors.New("rollback")
	}))
	var count int64
	require.NoError(t, st.DB.Table("content_url_changes").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Exec("DELETE FROM collections WHERE id=?", cid).Error)
	require.NoError(t, st.DB.Raw("SELECT string_agg(array_to_string(paths,','),',') FROM content_url_changes").Scan(&paths).Error)
	require.True(t, strings.Contains(paths, "/collections/new"))
}
