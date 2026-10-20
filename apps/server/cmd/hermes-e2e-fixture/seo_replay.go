package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// replaySEO runs exclusively on the caller's validated E2E connection. Even a
// successful replay rolls all fixture business data back; no provider is called.
func replaySEO(ctx context.Context, db *gorm.DB) (map[string]any, error) {
	report := map[string]any{"provider": "fixture", "paidCalls": 0, "checks": []string{}}
	checks := []string{}
	rollback := errors.New("successful isolated replay")
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		st := &store.Store{DB: tx}
		svc := &management.Service{Store: st, Config: config.Config{WebBaseURL: "https://example.com"}}
		request := func(ctx context.Context, perms ...string) context.Context {
			return domain.WithOperation(ctx, &domain.Operation{RequestID: uuid.NewString(), AgentClientID: 1, AgentTokenID: 2, Permissions: perms, RequiredPermissions: perms, AllowDelete: true})
		}
		exec := request(ctx, "translation:review", "catalog:category:edit")
		tuner := request(ctx, "seo:automation:tune")
		view, err := svc.Execute(tuner, "GetSeoAutomation", management.Input{})
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(view)
		var configView map[string]any
		_ = json.Unmarshal(raw, &configView)
		if _, err := svc.Execute(exec, "UpdateSeoAutomation", management.Input{Body: map[string]any{}}); err == nil {
			return errors.New("execution token tuned automation")
		}
		checks = append(checks, "execution_cannot_tune")
		settings := configView["automation"].(map[string]any)
		settings["actions"].(map[string]any)["translation"].(map[string]any)["mode"] = "proposal"
		_, err = svc.Execute(tuner, "UpdateSeoAutomation", management.Input{Body: map[string]any{"automation": settings, "expectedRevision": configView["revision"], "idempotencyKey": uuid.NewString(), "reason": "isolated replay"}})
		if err != nil {
			return err
		}
		var id int64
		if err := tx.Raw("INSERT INTO categories(slug,icon,source_locale) VALUES(?,'lucide:box','en-US') RETURNING id", "hermes-replay-"+uuid.NewString()).Scan(&id).Error; err != nil {
			return err
		}
		ref := content.Ref{Entity: "category", ID: id}
		entity := "category:" + ref.Key()
		if err := st.WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File manager"}, true); err != nil {
			return err
		}
		var taskCount int64
		if err := tx.Table("seo_tasks").Where("entity_key=?", entity).Count(&taskCount).Error; err != nil {
			return err
		}
		if taskCount != 9 {
			return fmt.Errorf("translation tasks: got %d", taskCount)
		}
		checks = append(checks, "nine_translation_tasks")
		tasks := &seotasks.Service{DB: tx}
		// Claim a fixture-only kind, never another E2E runner's queued work.
		if err := tx.Exec("UPDATE seo_tasks SET kind='fixture_replay' WHERE entity_key=?", entity).Error; err != nil {
			return err
		}
		leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"fixture_replay"})
		if err != nil {
			return err
		}
		if len(leased) != 1 {
			return errors.New("fixture lease missing")
		}
		source, err := st.ReadContentSource(ctx, ref)
		if err != nil {
			return err
		}
		item := map[string]any{"locale": "zh-CN", "sourceHash": source.Hash, "expectedRevision": seotasks.Hash([]byte("null")), "idempotencyKey": uuid.NewString(), "fields": map[string]any{"name": "文件管理工具"}}
		body := map[string]any{"ref": ref, "model": "fixture", "taskId": leased[0].ID, "fencing": leased[0].Fencing, "translations": []any{item}}
		v, err := svc.Execute(exec, "WriteTranslations", management.Input{Body: body})
		if err != nil {
			return err
		}
		raw, _ = json.Marshal(v)
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		if result["results"].([]any)[0].(map[string]any)["status"] != "proposal" {
			return fmt.Errorf("expected proposal: %s", raw)
		}
		value, err := st.TranslationValue(ctx, ref, "zh-CN")
		if err != nil {
			return err
		}
		if value.Status != "missing" {
			return errors.New("proposal changed content")
		}
		checks = append(checks, "proposal_no_business_write")
		settings["actions"].(map[string]any)["translation"].(map[string]any)["mode"] = "live"
		_, err = svc.Execute(tuner, "UpdateSeoAutomation", management.Input{Body: map[string]any{"automation": settings, "expectedRevision": configView["revision"].(float64) + 1, "idempotencyKey": uuid.NewString(), "reason": "isolated live revalidation"}})
		if err != nil {
			return err
		}
		value, err = st.TranslationValue(ctx, ref, "zh-CN")
		if err != nil {
			return err
		}
		if value.Status != "missing" {
			return errors.New("mode switch applied proposal")
		}
		item["idempotencyKey"] = uuid.NewString()
		item["sourceHash"] = "obsolete"
		v, err = svc.Execute(exec, "WriteTranslations", management.Input{Body: body})
		if err != nil {
			return err
		}
		raw, _ = json.Marshal(v)
		_ = json.Unmarshal(raw, &result)
		if result["results"].([]any)[0].(map[string]any)["status"] != "failed" {
			return errors.New("stale proposal accepted")
		}
		checks = append(checks, "live_revalidates_source")
		item["sourceHash"] = source.Hash
		item["idempotencyKey"] = uuid.NewString()
		if _, err = svc.Execute(exec, "WriteTranslations", management.Input{Body: body}); err != nil {
			return err
		}
		value, err = st.TranslationValue(ctx, ref, "zh-CN")
		if err != nil {
			return err
		}
		if value.Status != "machine" {
			return errors.New("live translation missing")
		}
		checks = append(checks, "live_machine_provenance")
		var actionID, revision int64
		if err := tx.Raw("SELECT id FROM seo_actions WHERE task_id=? AND mode='live' ORDER BY id DESC LIMIT 1", leased[0].ID).Scan(&actionID).Error; err != nil {
			return err
		}
		if err := tasks.Defer(ctx, leased[0].ID, leased[0].Fencing, seotasks.Owner(1, 2), "fixture compensation", time.Now().Add(time.Hour)); err != nil {
			return err
		}
		if _, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "fixture_compensate", EntityKey: entity, DedupeKey: uuid.NewString(), RuleVersion: "fixture"}); err != nil {
			return err
		}
		compensation, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"fixture_compensate"})
		if err != nil {
			return err
		}
		if len(compensation) != 1 {
			return errors.New("compensation lease missing")
		}
		var evidenceID int64
		if err := tx.Raw(`INSERT INTO seo_evidence_snapshots(entity_key,source_url,final_url,body_text,excerpt,body_hash,fetched_at,valid_until,authority,fetch_status,created_by,capture_key,request_hash)
   VALUES(?,'https://example.com','https://example.com','Fixture Mac reference','Fixture Mac reference',?,clock_timestamp(),clock_timestamp()+interval '1 day','reference',200,'fixture',?,?) RETURNING id`, entity, seotasks.Hash([]byte("fixture")), uuid.NewString(), seotasks.Hash([]byte("fixture"))).Scan(&evidenceID).Error; err != nil {
			return err
		}
		if err := tx.Raw("SELECT max(id) FROM content_revisions WHERE entity='category' AND object_key=?", ref.Key()).Scan(&revision).Error; err != nil {
			return err
		}
		compctx := request(ctx, "seo:task:handle", "content:revision:restore", "catalog:category:edit")
		domain.CurrentOperation(compctx).Name = "CompensateSeoAction"
		domain.CurrentOperation(compctx).Execution = &domain.AgentExecution{TaskID: compensation[0].ID, Fencing: compensation[0].Fencing, IdempotencyKey: uuid.NewString(), ExpectedRevision: revision, EvidenceIDs: []int64{evidenceID}}
		if _, err := svc.Execute(compctx, "CompensateSeoAction", management.Input{ID: actionID, Body: map[string]any{"reason": "implementation_error"}}); err != nil {
			return err
		}
		value, err = st.TranslationValue(ctx, ref, "zh-CN")
		if err != nil {
			return err
		}
		if value.Status != "missing" {
			return errors.New("compensation did not restore prior state")
		}
		checks = append(checks, "compensation_restores_exact_prior_state")
		if err := agentgate.CheckCompensation(map[string]any{"v": 1}, map[string]any{"v": 2}, map[string]any{"v": 3}, []string{"/v"}, nil, "implementation_error"); err == nil {
			return errors.New("stale compensation accepted")
		}
		checks = append(checks, "stale_compensation_rejected")
		// Fake OpenSEO invoice; Record reconciles it without any network dependency.
		receipt := seobudget.Receipt{VendorTaskID: "fixture-" + uuid.NewString(), CostUSD: "20", IncurredAt: time.Now().UTC()}
		if err := seobudget.Record(ctx, tx, "example.com", receipt); err != nil {
			return err
		}
		if !errors.Is(seobudget.Check(ctx, tx, "example.com", "research", true), seobudget.ErrFrozen) {
			return errors.New("paid research not stopped")
		}
		if err := seobudget.Check(ctx, tx, "example.com", "fact_fix", false); err != nil {
			return err
		}
		checks = append(checks, "mock_budget_preserves_fix_channel")
		report["checks"] = checks
		return rollback
	})
	if errors.Is(err, rollback) {
		return report, nil
	}
	return nil, fmt.Errorf("SEO replay after %v: %w", checks, err)
}
