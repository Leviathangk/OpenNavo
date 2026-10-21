package agentgate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"gorm.io/gorm"
)

type Policy struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

type Rule struct {
	Action, Object string
	Evidence       bool
	Official       bool
}

// Explicit registration fails closed when a new endpoint is added. HTTP
// allowlisting alone is not a business authorization boundary.
func RuleFor(operation string) (Rule, bool) {
	groups := []struct {
		r          Rule
		operations string
	}{
		{Rule{"translation", "translation", false, false}, "FixTranslation"},
		{Rule{"maintenance", "package", true, true}, "UpdatePackageText UpdatePackageMeta SetPackagePrice SetPackageMacosSupport SetPackageAlternatives UpdatePackageI18n SetPackageCategories SetPackageIcon DeletePackageIcon AddPackageScreenshot ReorderPackageScreenshots UpdatePackageScreenshot DeletePackageScreenshot UpdatePackageChangelogSettings"},
		{Rule{"maintenance", "release", true, true}, "UpsertReleaseNotes UpdateRelease UpdateReleaseI18n"},
		{Rule{"fact_fill", "package", true, true}, "SetPackageFactRecords"},
		{Rule{"fact_fill", "background", true, false}, "SetBackgroundItem"},
		{Rule{"maintenance", "asset", true, false}, "UploadAsset CreateAssetUploadTicket UploadAssetWithTicket"},
		{Rule{"content", "collection", true, false}, "CreateCollection UpdateCollection DeleteCollection SetCollectionItems PublishCollection UnpublishCollection SetCollectionSetupCriteria"},
		{Rule{"content", "category", true, false}, "CreateCategory UpdateCategory DeleteCategory ReorderCategories"},
		{Rule{"content", "feature", true, false}, "CreateFeature UpdateFeature DeleteFeature"},
		{Rule{"content", "announcement", true, false}, "UpdateAnnouncement"},
		{Rule{"maintenance", "desktop_release", true, false}, "UpdateDesktopReleaseNotes"},
		{Rule{"content", "glossary", true, false}, "UpsertGlossaryTerm DeleteGlossaryTerm"},
		{Rule{"listing", "package", true, false}, "AddLinkPackage UpdateLinkPackage AddTapPackage"},
		{Rule{"listing", "listing", true, false}, "UpsertSeoListingCandidate"},
		{Rule{"withdraw", "package", false, false}, "RemoveLinkPackage RemoveTapPackage"},
		{Rule{"article", "article", true, false}, "CreateArticle UpdateArticle SetArticlePackages SubmitArticleReview PublishArticle RestoreArticle RevertArticle SetArticleSlug SetArticleLocale"},
		{Rule{"withdraw", "article", false, false}, "WithdrawArticle"},
		{Rule{"article", "article_author", true, false}, "CreateArticleAuthor UpdateArticleAuthor"},
		{Rule{"compensate", "history", true, false}, "RestoreRevision RestoreTrash RevertRequest CompensateSeoAction"},
		{Rule{"research", "keywords", false, false}, "UpsertSeoKeywords"},
		{Rule{"snippet", "experiment", false, false}, "ProposeSnippetTemplate"},
		{Rule{"operations", "synonym", true, false}, "CreateSynonym UpdateSynonym DeleteSynonym"},
		{Rule{"operations", "feedback", false, false}, "UpdateFeedback"},
		{Rule{"operations", "update_feed", true, false}, "UpsertUpdateFeed DisableUpdateFeed"},
		{Rule{"operations", "job", false, false}, "ResyncPackage TriggerJob"},
	}
	for _, g := range groups {
		for _, name := range strings.Fields(g.operations) {
			if operation == name {
				return g.r, true
			}
		}
	}
	return Rule{}, false
}

type Write struct {
	Operation, Entity, Version, Site string
	Body                             map[string]any
	Task                             seotasks.Task
	EvidenceIDs                      []int64
	// Source is the text a translation renders. Testing or performance wording
	// the source already states may be translated ("faster" becomes "更快"); a
	// translation that adds such wording is rejected like any unsupported claim.
	Source string
}

type Decision struct {
	Rule     Rule
	Policy   Policy
	Evidence []Evidence
}

func Check(ctx context.Context, tx *gorm.DB, w Write) (Decision, error) {
	d := Decision{}
	var ok bool
	d.Rule, ok = RuleFor(w.Operation)
	if !ok {
		return d, reject("unregistered_write")
	}
	taskEntity, err := seotasks.CanonicalEntity(ctx, tx, w.Task.EntityKey)
	if err != nil {
		return d, err
	}
	writeEntity, err := seotasks.CanonicalEntity(ctx, tx, w.Entity)
	if err != nil {
		return d, err
	}
	if taskEntity != writeEntity {
		return d, reject("task_entity_mismatch")
	}
	// Fixes are a distinct server-owned task kind, never a flag supplied by the caller.
	if w.Task.Kind == "fix_fact" && (d.Rule.Action == "maintenance" || d.Rule.Action == "fact_fill") {
		d.Rule.Action = "fact_fix"
	}
	// Serialize policy reads with tuner updates through the shared site lock.
	if err := seobudget.Lock(ctx, tx, w.Site); err != nil {
		return d, err
	}
	var row struct{ Automation json.RawMessage }
	if err := tx.WithContext(ctx).Raw("SELECT automation FROM seo_settings LIMIT 1").Scan(&row).Error; err != nil {
		return d, err
	}
	var settings struct {
		Actions map[string]Policy `json:"actions"`
	}
	if json.Unmarshal(row.Automation, &settings) != nil {
		return d, reject("automation_unconfigured")
	}
	d.Policy, ok = settings.Actions[d.Rule.Action]
	if !ok || !d.Policy.Enabled || (d.Policy.Mode != "live" && d.Policy.Mode != "proposal") {
		return d, reject("automation_disabled")
	}
	if err := seobudget.Check(ctx, tx, w.Site, d.Rule.Action, false); err != nil {
		return d, err
	}
	if d.Rule.Evidence {
		var err error
		d.Evidence, err = LoadEvidence(ctx, tx, w.EvidenceIDs, w.Entity, d.Rule.Official)
		if err != nil {
			return d, err
		}
	} else if len(w.EvidenceIDs) > 0 {
		var err error
		d.Evidence, err = LoadEvidence(ctx, tx, w.EvidenceIDs, w.Entity, false)
		if err != nil {
			return d, err
		}
	}
	allowTesting := d.Rule.Object == "article" || d.Rule.Object == "translation" && testingClaim.MatchString(w.Source)
	if err := validateText(w.Body, w.Version, allowTesting); err != nil {
		return d, err
	}
	if w.Operation == "SetPackageFactRecords" {
		if err := normalizeFacts(w.Body, d.Evidence); err != nil {
			return d, err
		}
	}
	if w.Operation == "SetPackagePrice" || w.Operation == "SetPackageMacosSupport" {
		if err := normalizeLegacyFact(w.Body, d.Evidence); err != nil {
			return d, err
		}
	}
	if w.Operation == "SetPackageAlternatives" {
		if err := validateAlternatives(ctx, tx, w.Body, d.Evidence); err != nil {
			return d, err
		}
	}
	if err := normalizeNestedProofs(w.Body, d.Evidence); err != nil {
		return d, err
	}
	if d.Rule.Action == "listing" {
		mac := false
		for _, e := range d.Evidence {
			mac = mac || macPlatform.MatchString(e.Excerpt)
		}
		if !mac {
			return d, reject("mac_support_not_evidenced")
		}
	}
	return d, nil
}

var foreignSection = regexp.MustCompile(`(?im)^(?:\s{0,3}#{1,6}\s+|\s*(?:"?heading"?|"?title"?)\s*:\s*"?)([^\n]+)`)
var foreignPlatform = regexp.MustCompile(`(?i)\b(windows|android|linux|ios|iphone|ipad|ubuntu|debian)\b|安卓|윈도우`)
var macPlatform = regexp.MustCompile(`(?i)\b(mac|macos|os x)\b|苹果电脑|蘋果電腦`)
var currentVersion = regexp.MustCompile(`(?i)(?:\b(?:version|latest|current version|versión|versão|versione)\s*[:：]?\s*v?|(?:版本|最新版|バージョン)\s*[:：]?\s*v?)((?:[0-9]+\.)+[0-9]+(?:[a-z0-9+.-]*))`)
var testingClaim = regexp.MustCompile(`(?i)\b(?:we (?:tested|benchmarked)|our (?:tests|benchmarks)|faster|safer)\b|我们(?:测试|实测)|我們(?:測試|實測)|更快|更安全`)

// Only sections, not inline platform comparisons, are excluded. Version checks
// apply to explicit version claims, not arbitrary decimal numbers or dates.
func ValidateText(body map[string]any, version string) error {
	return validateText(body, version, false)
}

func validateText(body map[string]any, version string, allowTesting bool) error {
	var walk func(any, string) error
	walk = func(v any, key string) error {
		switch x := v.(type) {
		case map[string]any:
			for k, item := range x {
				if k == "evidence" || k == "sourceUrl" || k == "url" {
					continue
				}
				if err := walk(item, k); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range x {
				if err := walk(item, key); err != nil {
					return err
				}
			}
		case string:
			if key == "title" || key == "heading" {
				if foreignPlatform.MatchString(x) && !macPlatform.MatchString(x) {
					return reject("non_mac_section")
				}
			}
			for _, match := range foreignSection.FindAllStringSubmatch(x, -1) {
				if foreignPlatform.MatchString(match[1]) && !macPlatform.MatchString(match[1]) {
					return reject("non_mac_section")
				}
			}
			if version != "" {
				for _, match := range currentVersion.FindAllStringSubmatch(x, -1) {
					if strings.TrimSuffix(match[1], ".") != version {
						return reject("version_mismatch")
					}
				}
			}
			if !allowTesting && testingClaim.MatchString(x) {
				return reject("test_artifact_required")
			}
		}
		return nil
	}
	return walk(body, "")
}

func proofFor(source string, evidence []Evidence) (Evidence, error) {
	for _, e := range evidence {
		if source == e.SourceURL || source == e.FinalURL {
			return e, nil
		}
	}
	return Evidence{}, reject("fact_source_not_captured")
}

// Conservative evidence binding: literal values must be present in the
// server-captured excerpt; possessing an unrelated official URL proves nothing.
func bindValues(v any, e Evidence) error {
	text := strings.ToLower(normalizeText(e.Excerpt))
	contains := func(value string) bool {
		value = strings.ToLower(normalizeText(value))
		boundary := `[^\pL\pN]`
		if regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`).MatchString(value) {
			boundary = `[^\pL\pN.]`
		}
		return regexp.MustCompile(`(?:^|` + boundary + `)` + regexp.QuoteMeta(value) + `(?:$|` + boundary + `)`).MatchString(text)
	}
	var walk func(any, string) error
	walk = func(value any, key string) error {
		switch x := value.(type) {
		case map[string]any:
			for k, y := range x {
				if k == "evidence" {
					continue
				}
				if err := walk(y, k); err != nil {
					return err
				}
			}
		case []any:
			for _, y := range x {
				if err := walk(y, key); err != nil {
					return err
				}
			}
		case string:
			if x == "" || x == "unknown" || key == "channel" || key == "method" {
				return nil
			}
			if strings.HasSuffix(strings.ToLower(key), "url") {
				if x != e.SourceURL && x != e.FinalURL && !strings.Contains(e.BodyText, x) {
					return reject("fact_value_not_in_evidence")
				}
			} else if !contains(x) {
				return reject("fact_value_not_in_evidence")
			}
		case float64, int64, int, json.Number:
			if !contains(fmt.Sprint(x)) {
				return reject("fact_value_not_in_evidence")
			}
		case bool:
			// A positive and a negative observation are different claims. Do not
			// infer either from the mere occurrence of the field name.
			value := strings.ToLower(key) + "=" + fmt.Sprint(x)
			jsonValue := `"` + strings.ToLower(key) + `":` + fmt.Sprint(x)
			if !strings.Contains(strings.ReplaceAll(text, " ", ""), value) && !strings.Contains(strings.ReplaceAll(text, " ", ""), jsonValue) {
				return reject("fact_value_not_in_evidence")
			}
		}
		return nil
	}
	return walk(v, "")
}

func normalizeNestedProofs(value any, evidence []Evidence) error {
	switch v := value.(type) {
	case map[string]any:
		if raw, ok := v["evidence"].(map[string]any); ok {
			source, _ := raw["sourceUrl"].(string)
			e, err := proofFor(source, evidence)
			if err != nil {
				return err
			}
			claims := map[string]any{}
			for key, claim := range v {
				if key != "evidence" && key != "locale" && key != "key" && key != "kind" && key != "id" && key != "packageId" {
					claims[key] = claim
				}
			}
			if err := bindValues(claims, e); err != nil {
				return err
			}
			v["evidence"] = map[string]any{"sourceUrl": e.FinalURL, "checkedAt": e.FetchedAt.Format(time.RFC3339), "validUntil": e.ValidUntil.Format(time.RFC3339), "status": "verified"}
		}
		for k, child := range v {
			if k != "evidence" {
				if err := normalizeNestedProofs(child, evidence); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, child := range v {
			if err := normalizeNestedProofs(child, evidence); err != nil {
				return err
			}
		}
	}
	return nil
}

// Snippet variants are server-rendered templates; arbitrary page-specific SEO
// copy cannot be smuggled through an experiment proposal.
func ValidateSnippet(variant string, slots []string) error {
	allowed := map[string]map[string]bool{
		"verified_free":          {"name": true, "free": true},
		"current_version":        {"name": true, "version": true},
		"uninstall_alternatives": {"name": true, "uninstall": true, "alternatives": true},
	}
	required, ok := allowed[variant]
	if !ok || len(slots) != len(required) {
		return reject("invalid_snippet_template")
	}
	seen := map[string]bool{}
	for _, slot := range slots {
		if !required[slot] || seen[slot] {
			return reject("invalid_snippet_slot")
		}
		seen[slot] = true
	}
	return nil
}

func normalizeFacts(body map[string]any, evidence []Evidence) error {
	items, ok := body["items"].([]any)
	if !ok || len(items) == 0 {
		return reject("invalid_facts")
	}
	for _, item := range items {
		r, ok := item.(map[string]any)
		if !ok {
			return reject("invalid_facts")
		}
		proof, _ := r["evidence"].(map[string]any)
		source, _ := proof["sourceUrl"].(string)
		e, err := proofFor(source, evidence)
		if err != nil {
			return err
		}
		if err := bindValues(r["value"], e); err != nil {
			return err
		}
		r["evidence"] = map[string]any{"sourceUrl": e.FinalURL, "checkedAt": e.FetchedAt.Format(time.RFC3339), "validUntil": e.ValidUntil.Format(time.RFC3339), "status": "verified"}
		raw, _ := json.Marshal(r)
		var record facts.Record
		if json.Unmarshal(raw, &record) != nil || facts.ValidateRecord(record, e.FetchedAt) != nil {
			return reject("invalid_facts")
		}
	}
	return nil
}

func normalizeLegacyFact(body map[string]any, evidence []Evidence) error {
	if clear, _ := body["clear"].(bool); clear {
		return nil
	}
	source, _ := body["sourceUrl"].(string)
	e, err := proofFor(source, evidence)
	if err != nil {
		return err
	}
	values := map[string]any{}
	for k, v := range body {
		if k != "verifiedAt" && k != "note" && k != "sourceUrl" && k != "clear" {
			values[k] = v
		}
	}
	if err := bindValues(values, e); err != nil {
		return err
	}
	body["verifiedAt"] = e.FetchedAt.Format(time.RFC3339)
	return nil
}

// Log excludes submitted bodies and credentials. Rejections are written after
// rollback by the caller, so failed validation remains observable.
func Log(ctx context.Context, db *gorm.DB, request, owner, operation, result, reason string, taskID int64, ids []int64) error {
	var task any
	if taskID > 0 {
		task = taskID
	}
	if ids == nil {
		ids = []int64{}
	}
	raw, _ := json.Marshal(ids)
	return db.WithContext(ctx).Exec(`INSERT INTO seo_gate_events(request_id,task_id,owner,operation,rule_version,result,reason,evidence_ids) VALUES(?,(SELECT id FROM seo_tasks WHERE id=?),?,?,?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)::bigint))`, request, task, owner, operation, RuleVersion, result, reason, string(raw)).Error
}
