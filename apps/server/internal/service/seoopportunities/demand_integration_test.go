//go:build integration

package seoopportunities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/seokeywords"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDemandRepairPreservesLeasesAndDecisions(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	queries := []string{"programme deinstallieren mac", "désinstaller application mac", "mac 卸载软件", "video editor mac", "antivirus mac", "download exampleproduct mac"}
	for _, q := range queries {
		require.NoError(t, st.DB.Exec(`INSERT INTO seo_keywords(keyword,normalized,source,engine,country,language,device,period,volume_state,observed_at)
 VALUES(?,?,'gsc','google','US','en-US','desktop','fixture','unknown',now())`, q, seokeywords.Normalize(q)).Error)
	}
	old := func(query, state string) int64 {
		slug := legacySlugPattern.ReplaceAllString(query, "-")
		evidence, _ := json.Marshal(map[string]any{"source": "keywords", "keyword": strings.ReplaceAll(slug, "-", " "), "targetSlug": slug})
		var id int64
		require.NoError(t, st.DB.Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version,state,lease_owner,lease_until)
 VALUES('listing',?,'en-US',?,10,?::jsonb,?,'opportunities-v1',?,CASE WHEN ?='leased' THEN 'fixture' END,CASE WHEN ?='leased' THEN now()+interval '20 minutes' END) RETURNING id`, "listing:"+slug, "demand:listing:en-US:"+slug, string(evidence), seotasks.Hash(evidence), state, state, state).Scan(&id).Error)
		return id
	}
	wrong := old(queries[0], "open")
	accented := old(queries[1], "deferred")
	done := old(queries[3], "done")
	held := old(queries[4], "leased")
	count, err := svc.RepairDemand(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	var before seotasks.Task
	require.NoError(t, st.DB.First(&before, wrong).Error)
	require.Equal(t, "listing", before.Kind)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	for _, id := range []int64{wrong, accented} {
		var task seotasks.Task
		require.NoError(t, st.DB.First(&task, id).Error)
		require.Equal(t, "topic", task.Kind)
		require.Contains(t, string(task.Result), "demand_reclassified")
		if id == accented {
			require.Contains(t, string(task.Evidence), "désinstaller")
			require.Equal(t, "article:new:desinstaller-application-mac", task.EntityKey)
		}
	}
	for _, id := range []int64{done, held} {
		var task seotasks.Task
		require.NoError(t, st.DB.First(&task, id).Error)
		require.Equal(t, "listing", task.Kind)
		require.Equal(t, "opportunities-v1", task.RuleVersion)
	}
	var cjk seotasks.Task
	require.NoError(t, st.DB.Where("entity_key=?", "article:new:"+demandSlug(queries[2])).First(&cjk).Error)
	require.Contains(t, string(cjk.Evidence), queries[2])
	var first, second int64
	require.NoError(t, st.DB.Table("seo_tasks").Count(&first).Error)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_tasks").Count(&second).Error)
	require.Equal(t, first, second)
	// A lease that ends later is repaired even without a new keyword watermark.
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET lease_until=now()-interval '1 second' WHERE id=?", held).Error)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	var repaired seotasks.Task
	require.NoError(t, st.DB.First(&repaired, held).Error)
	require.Equal(t, "topic", repaired.Kind)
	require.Equal(t, "open", repaired.State)
}

func TestDemandRepairKeepsOneDuplicateAndHonorsCompletedWork(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	for _, state := range []string{"open", "done"} {
		slug := "best-mac-" + state
		keyword := strings.ReplaceAll(slug, "-", " ")
		var survivor int64
		require.NoError(t, st.DB.Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,state,evidence,evidence_hash,rule_version)
 VALUES('topic',?,'en-US',?,?,jsonb_build_object('source','keywords','keyword',?::text,'targetSlug',?::text),'fixture','opportunities-v1') RETURNING id`, "article:new:"+slug, "demand:original:"+slug, state, keyword, slug).Scan(&survivor).Error)
		for _, kind := range []string{"topic", "topic", "listing"} {
			var duplicate int64
			prefix := "article:new:"
			if kind == "listing" {
				prefix = "listing:"
			}
			require.NoError(t, st.DB.Raw(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,evidence,evidence_hash,rule_version)
 SELECT ?,?,'en-US','demand:duplicate:'||?||':'||count(*),jsonb_build_object('source','keywords','keyword',?::text,'targetSlug',?::text),'fixture','opportunities-v1' FROM seo_tasks RETURNING id`, kind, prefix+slug, slug, keyword, slug).Scan(&duplicate).Error)
		}
		_, err := svc.RepairDemand(ctx, true)
		require.NoError(t, err)
		var kept []seotasks.Task
		require.NoError(t, st.DB.Where("evidence->>'targetSlug'=? AND state<>'expired'", slug).Find(&kept).Error)
		require.Len(t, kept, 1)
		require.Equal(t, survivor, kept[0].ID)
		require.Equal(t, state, kept[0].State)
		var replacements []int64
		require.NoError(t, st.DB.Raw("SELECT (result->>'replacementTaskId')::bigint FROM seo_tasks WHERE evidence->>'targetSlug'=? AND state='expired'", slug).Scan(&replacements).Error)
		require.Equal(t, []int64{survivor, survivor, survivor}, replacements)
	}
	count, err := svc.RepairDemand(ctx, true)
	require.NoError(t, err)
	require.Zero(t, count)
}
