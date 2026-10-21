package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

type agentTransactionKey struct{}

func agentTransaction(ctx context.Context) bool {
	v, _ := ctx.Value(agentTransactionKey{}).(bool)
	return v
}

// All management writes, including legacy article and restoration paths, use
// this transaction. The nested business transactions are savepoints on it.
func (s *Service) executeAgent(ctx context.Context, name string, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	for _, permission := range op.RequiredPermissions {
		if !op.Allowed(permission) {
			return nil, fail(domain.CodeForbidden)
		}
	}
	if op.Execution == nil {
		return nil, domain.Validation()
	}
	rule, registered := agentgate.RuleFor(name)
	if !registered {
		return nil, fail(domain.CodeForbidden)
	}
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		b, err := beginAgentAction(ctx, st, in)
		if err != nil {
			return err
		}
		if b.Replayed {
			result, err = replayAction(b)
			return err
		}
		entity, refs, version, err := agentScope(ctx, st, name, rule.Object, in, b.Task)
		if err != nil {
			return err
		}
		if rule.Object == "listing" {
			revision, err := ListingAgentRevision(ctx, st, in.Key)
			if err != nil {
				return err
			}
			if revision != b.Write.ExpectedRevision {
				return fail(domain.CodeConflict)
			}
		} else if rule.Object == "article" && in.ID > 0 {
			var revision int64
			if err := tx.Raw("SELECT revision FROM articles WHERE id=? FOR UPDATE", in.ID).Scan(&revision).Error; err != nil {
				return err
			}
			if revision != b.Write.ExpectedRevision {
				return fail(domain.CodeConflict)
			}
		} else if nonHistorySEOObject(rule.Object) {
			if err := tx.Exec("INSERT INTO seo_entity_revisions(entity_key,revision) VALUES(?,0) ON CONFLICT DO NOTHING", entity).Error; err != nil {
				return err
			}
			var revision int64
			if err := tx.Raw("SELECT revision FROM seo_entity_revisions WHERE entity_key=? FOR UPDATE", entity).Scan(&revision).Error; err != nil {
				return err
			}
			if revision != b.Write.ExpectedRevision {
				return fail(domain.CodeConflict)
			}
		} else if err := checkActionRevision(ctx, st, b, refs); err != nil {
			return err
		}
		// Own the request copy: provenance normalization must not alter the caller's
		// payload or change the idempotency hash on a retry.
		in.Body = cloneAgentBody(in.Body)
		if err := guardInlineTranslations(ctx, st, name, refs, in); err != nil {
			return err
		}
		d, err := agentgate.Check(ctx, tx, agentgate.Write{Site: seobudget.Site(s.Config.WebBaseURL), Operation: name, Entity: entity, Version: version, Body: in.Body, Task: b.Task, EvidenceIDs: b.Write.EvidenceIDs})
		if err != nil {
			return err
		}
		local := *s
		local.Store = st
		before, err := local.agentSnapshot(ctx, name, rule.Object, in, refs)
		if err != nil {
			return err
		}
		if err := tx.SavePoint("agent_business").Error; err != nil {
			return err
		}
		budgetEntity := ""
		if d.Policy.Mode == "live" {
			budgetEntity, err = seotasks.CanonicalEntity(ctx, tx, entity)
			if err != nil {
				return err
			}
			if err := seobudget.Reserve(ctx, tx, seobudget.Site(s.Config.WebBaseURL), d.Rule.Action, budgetEntity, op.RequestID); err != nil {
				return err
			}
		}
		copy := *op
		copy.DryRun = false
		business := context.WithValue(domain.WithOperation(ctx, &copy), agentTransactionKey{}, true)
		result, err = local.execute(business, name, in)
		if err != nil {
			return err
		}
		afterInput := in
		if view, ok := result.(map[string]any); ok && afterInput.ID == 0 {
			afterInput.ID = valueID(view["id"])
		}
		afterRefs, err := historyRefs(ctx, st, name, rule.Object, afterInput, result)
		if err != nil {
			return err
		}
		if rule.Object == "history" || rule.Object == "translation" {
			afterRefs = refs
		}
		after, err := local.agentSnapshot(ctx, name, rule.Object, afterInput, afterRefs)
		if err != nil {
			return err
		}
		if op.DryRun || d.Policy.Mode == "proposal" {
			urls, err := local.agentAffectedURLs(ctx, rule.Object, afterInput, afterRefs, before, after, result)
			if err != nil {
				return err
			}
			if err := tx.RollbackTo("agent_business").Error; err != nil {
				return err
			}
			result = map[string]any{"dryRun": true, "requestId": op.RequestID, "changes": []any{map[string]any{"object": map[string]any{"entity": rule.Object, "objectKey": entity}, "action": "update", "before": before, "after": after}}, "affectedUrls": urls, "validation": "passed"}
			result.(map[string]any)["mode"] = "proposal"
			if op.DryRun {
				result.(map[string]any)["mode"] = "preview"
			}
			if op.DryRun {
				return agentgate.Log(ctx, tx, op.RequestID, b.Write.Owner, name, "preview", "validated", b.Task.ID, b.Write.EvidenceIDs)
			}
		}
		if d.Policy.Mode == "live" && strings.Contains(entity, ":new:") && afterInput.ID > 0 {
			stable := rule.Object + ":" + strconv.FormatInt(afterInput.ID, 10)
			if rule.Object == "package" {
				stable = "pkg:" + strconv.FormatInt(afterInput.ID, 10)
			}
			if err := seotasks.Rebind(ctx, tx, b.Task, b.Write.Owner, stable); err != nil {
				return err
			}
			if err := seobudget.Rebind(ctx, tx, seobudget.Site(s.Config.WebBaseURL), budgetEntity, stable); err != nil {
				return err
			}
			if err := tx.Exec("UPDATE seo_evidence_snapshots SET entity_key=? WHERE entity_key=? AND created_by=?", stable, entity, b.Write.Owner).Error; err != nil {
				return err
			}
			entity = stable
		}
		fields := agentgate.ChangedFields(before, after)
		var afterRevision *int64
		if len(afterRefs) > 0 {
			revision, err := actionRevision(ctx, st, afterRefs)
			if err != nil {
				return err
			}
			afterRevision = &revision
		}
		var experimentID *int64
		if rule.Object == "experiment" && d.Policy.Mode == "live" {
			experimentID = &afterInput.ID
		}
		_, err = seotasks.CommitPartial(ctx, tx, *b, seotasks.Mutation{Mode: d.Policy.Mode, Object: rule.Object, ObjectID: entity, Fields: fields, Before: before, After: after, Result: result, RuleVersion: agentgate.RuleVersion, PlaybookVersion: agentgate.PlaybookVersion, ExperimentID: experimentID, AfterRevision: afterRevision})
		if err != nil {
			return err
		}
		if d.Policy.Mode == "live" {
			if err := queueAgentInvalidation(ctx, st, entity, afterRefs); err != nil {
				return err
			}
		}
		return agentgate.Log(ctx, tx, op.RequestID, b.Write.Owner, name, d.Policy.Mode, "validated", b.Task.ID, b.Write.EvidenceIDs)
	})
	if err != nil {
		logctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		reason := "write_rejected"
		var violation *agentgate.Violation
		if errors.As(err, &violation) {
			reason = violation.Reason
		}
		if logerr := agentgate.Log(logctx, s.Store.DB, op.RequestID, seotasks.Owner(op.AgentClientID, op.AgentTokenID), name, "rejected", reason, op.Execution.TaskID, op.Execution.EvidenceIDs); logerr != nil {
			return nil, logerr
		}
		if errors.Is(err, seobudget.ErrLimit) || errors.Is(err, seobudget.ErrFrozen) {
			return nil, fail(domain.CodeRateLimited)
		}
		if violation != nil {
			return nil, &domain.AppError{Code: domain.CodeValidation, HTTPStatus: 422, Msg: "rejected: " + violation.Reason}
		}
		return nil, store.MapAdminError(mapTaskError(err))
	}
	return result, nil
}

func agentScope(ctx context.Context, st *store.Store, name, object string, in Input, task seotasks.Task) (string, []content.Ref, string, error) {
	entity := object + ":" + strconv.FormatInt(in.ID, 10)
	refs, err := historyRefs(ctx, st, name, object, in, nil)
	if err != nil {
		return "", nil, "", err
	}
	switch {
	case name == "CompensateSeoAction":
		row, err := loadCompensation(ctx, st, in.ID)
		if err != nil {
			return "", nil, "", err
		}
		_, actionRefs, err := compensationStates(row.AfterData)
		if err != nil {
			return "", nil, "", err
		}
		entity, refs = row.ObjectID, actionRefs
	case object == "experiment":
		entity = "template:" + text(in.Body, "pageKind") + ":" + text(in.Body, "locale")
	case object == "listing":
		entity = "listing:" + in.Key
	case object == "translation":
		ref, err := contentRef(in.Body["ref"])
		if err != nil {
			return "", nil, "", err
		}
		entity = ref.Entity + ":" + ref.Key()
		locale := text(in.Body, "locale")
		if task.Locale != nil && *task.Locale != locale {
			return "", nil, "", fail(domain.CodeConflict)
		}
	case name == "UpsertReleaseNotes", name == "ResyncPackage":
		entity = fmt.Sprintf("pkg:%d", in.ID)
	case object == "history":
		table, where, arg := "content_revisions", "id=?", any(in.ID)
		if name == "RestoreTrash" {
			table = "content_trash"
		}
		if name == "RevertRequest" {
			where, arg = "request_id=?", in.RequestID
		}
		var rows []struct{ Entity, ObjectKey string }
		if err := st.DB.WithContext(ctx).Table(table).Select("entity,object_key").Where(where, arg).Find(&rows).Error; err != nil {
			return "", nil, "", err
		}
		refs = nil
		for _, row := range rows {
			ref, err := store.HistoryRef(row.Entity, row.ObjectKey)
			if err != nil {
				return "", nil, "", err
			}
			key := ref.Entity + ":" + ref.Key()
			if len(refs) > 0 && entity != key {
				return "", nil, "", fail(domain.CodeConflict)
			}
			entity = key
			refs = append(refs, ref)
		}
		if len(refs) == 0 {
			return "", nil, "", fail(domain.CodeNotFound)
		}
	case object == "announcement":
		entity = "announcement:desktop.announcement"
	case object == "keywords":
		entity = "site:keywords"
	case object == "update_feed":
		entity = "update_feed:" + text(in.Body, "source") + ":" + text(in.Body, "bundleId")
	case name == "SetBackgroundItem":
		entity = "background:" + in.Key
		refs = nil
	case in.ID == 0:
		key := text(in.Body, "slug")
		if key == "" {
			key = text(in.Body, "token")
		}
		if key == "" {
			key = text(in.Body, "term")
		}
		if key == "" {
			key = in.Key
		}
		if key == "" {
			key = in.JobType
		}
		if key == "" {
			return "", nil, "", domain.Validation()
		}
		entity = object + ":new:" + key
	}
	if strings.HasPrefix(entity, "package:") {
		entity = "pkg:" + strings.TrimPrefix(entity, "package:")
	}
	version := in.Version
	if object == "release" && in.ID > 0 && version == "" {
		if err := st.DB.WithContext(ctx).Raw("SELECT coalesce(version,'') FROM releases WHERE id=?", in.ID).Scan(&version).Error; err != nil {
			return "", nil, "", err
		}
	}
	if strings.HasPrefix(entity, "pkg:") && version == "" {
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(entity, "pkg:"), 10, 64)
		if parseErr == nil && id > 0 {
			if err := st.DB.WithContext(ctx).Raw("SELECT coalesce(version,'') FROM packages WHERE id=?", id).Scan(&version).Error; err != nil {
				return "", nil, "", err
			}
		}
	}
	return entity, refs, version, nil
}

func (s *Service) agentSnapshot(ctx context.Context, name, object string, in Input, refs []content.Ref) (any, error) {
	if nonHistorySEOObject(object) {
		return s.nonHistorySnapshot(ctx, object, in)
	}
	// Release notes are addressed by package and version, so in.ID is the package.
	// A version without editorial notes yet has an empty state, never release #in.ID.
	if name == "UpsertReleaseNotes" {
		return captureHistory(ctx, s.Store, refs)
	}
	if object == "experiment" {
		if in.ID == 0 {
			return nil, nil
		}
		var row struct{ Data json.RawMessage }
		if err := s.Store.DB.WithContext(ctx).Raw("SELECT to_jsonb(e)||jsonb_build_object('pages',(SELECT jsonb_agg(to_jsonb(p) ORDER BY url_id) FROM seo_experiment_pages p WHERE p.experiment_id=e.id)) data FROM seo_experiments e WHERE id=?", in.ID).Scan(&row).Error; err != nil {
			return nil, err
		}
		var value any
		err := json.Unmarshal(row.Data, &value)
		return value, err
	}
	if object == "article" {
		if in.ID == 0 {
			return nil, nil
		}
		return s.articleView(ctx, s.Store, in.ID)
	}
	if len(refs) > 0 {
		return captureHistory(ctx, s.Store, refs)
	}
	return snapshotEntity(ctx, s.Store, object, in)
}

func guardInlineTranslations(ctx context.Context, st *store.Store, name string, refs []content.Ref, in Input) error {
	if name == "FixTranslation" {
		return nil
	}
	if name == "ApproveTranslations" {
		return fail(domain.CodeForbidden)
	}
	source := text(in.Body, "sourceLocale")
	if len(refs) == 1 {
		var locale string
		if err := st.DB.WithContext(ctx).Raw("SELECT source_locale FROM content_translation_sources WHERE entity=? AND object_key=?", refs[0].Entity, refs[0].Key()).Scan(&locale).Error; err != nil {
			return err
		}
		if source == "" {
			source = locale
		}
	}
	if translated, ok := in.Body["i18n"].(map[string]any); ok {
		for locale := range translated {
			if locale != source || source == "" {
				return fail(domain.CodeForbidden)
			}
		}
	}
	if strings.HasSuffix(name, "I18n") && (in.Locale == "" || in.Locale != source) {
		return fail(domain.CodeForbidden)
	}
	return nil
}

// Preserve decoded integer types (notably downloadSize) and isolate nested proof
// normalization from the caller and the idempotency payload.
func cloneAgentBody(body map[string]any) map[string]any {
	var clone func(any) any
	clone = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			for key, item := range v {
				out[key] = clone(item)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, item := range v {
				out[i] = clone(item)
			}
			return out
		default:
			return value
		}
	}
	if body == nil {
		return nil
	}
	return clone(body).(map[string]any)
}
