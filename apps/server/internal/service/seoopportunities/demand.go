package seoopportunities

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/service/seokeywords"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

var macQuery = regexp.MustCompile(`(?i)(^|[^a-z0-9])(mac|macos)([^a-z0-9]|$)`)
var legacySlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// A category or an informational intent is never evidence of a single new app.
// Unknown searches become topics; explicit product acquisition queries can open
// listing research, whose existing nomination gates still verify the product.
//
//nolint:misspell // Includes category terms in several languages.
var categoryTerms = regexp.MustCompile(`(?i)(^|[^a-z0-9])(best|how|guide|tools?|apps?|applications?|software|programs?|programmes?|programme|editor|editors|antivirus|antiviren|player|players|browser|browsers|vpn|converter|converters|cleaner|cleaners|logiciels?|éditeur|editeur|meilleur|meilleurs|beste|besten|herramientas|aplicaciones|programas|melhores|ferramentas|программы|редактор|антивирус|приложения)([^a-z0-9]|$)|软件|軟體|应用|應用|工具|编辑|編輯|播放器|浏览器|瀏覽器|おすすめ|ソフト|アプリ|エディタ|ツール`)

func demandKind(query string) (kind, intent string) {
	query = seokeywords.Normalize(query)
	intent, blocked := seokeywords.Intent(query)
	if blocked || !macQuery.MatchString(query) {
		return "", intent
	}
	if !categoryTerms.MatchString(query) && (intent == "download" || intent == "install" || intent == "brew") {
		return "listing", intent
	}
	return "topic", intent
}

// Article URLs currently require ASCII slugs. Fold accents, and retain a stable
// hash when other scripts would be lost. The original query stays in evidence;
// Chinese/Japanese queries neither disappear nor collapse into the same "mac".
func demandSlug(query string) string {
	query = seokeywords.Normalize(query)
	var out strings.Builder
	lost := false
	for _, r := range norm.NFKD.String(query) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			out.WriteRune(r)
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			lost = true
			out.WriteByte('-')
		default:
			out.WriteByte('-')
		}
	}
	slug := strings.Join(strings.FieldsFunc(out.String(), func(r rune) bool { return r == '-' }), "-")
	if len(slug) > 80 {
		slug, lost = strings.TrimRight(slug[:80], "-"), true
	}
	if slug == "" {
		slug, lost = "topic", true
	}
	if lost {
		slug += fmt.Sprintf("-%x", sha256.Sum256([]byte(query)))[:13]
	}
	return slug
}

type demandQuery struct {
	Keyword, Locale string
	Volume          float64
}

func demandSignal(q demandQuery) signal {
	kind, intent := demandKind(q.Keyword)
	slug := demandSlug(q.Keyword)
	entity := "listing:" + slug
	if kind == "topic" {
		entity = "article:new:" + slug
	}
	raw, _ := json.Marshal(map[string]any{"source": "keywords", "keyword": q.Keyword, "intent": intent, "targetSlug": slug})
	return signal{Kind: kind, EntityKey: entity, Locale: q.Locale, DedupeKey: "demand:" + kind + ":" + q.Locale + ":" + slug, Priority: 10 + math.Log1p(q.Volume), Evidence: raw}
}

func emitDemand(tx *gorm.DB, emit func(signal) error) error {
	var queries []demandQuery
	// Bound the candidate window as well as emitted work. Classification remains
	// in Go so it shares the multilingual intent rules with the keyword ledger.
	if err := tx.Raw(`SELECT min(k.keyword) keyword,k.language locale,max(coalesce(k.search_volume,1)) volume FROM seo_keywords k
 WHERE (k.search_volume>0 OR k.source IN ('gsc','bing')) AND k.anomaly IS NULL AND k.observed_at>clock_timestamp()-interval '60 days'
 AND k.normalized ~ '(^|[^a-z0-9])(mac|macos)([^a-z0-9]|$)'
 AND NOT EXISTS(SELECT 1 FROM seo_keyword_links l WHERE l.keyword_id=k.id AND l.state='confirmed')
 GROUP BY k.normalized,k.language ORDER BY volume DESC,k.language,k.normalized LIMIT 5000`).Scan(&queries).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, q := range queries {
		s := demandSignal(q)
		if s.Kind == "" || seen[s.DedupeKey] {
			continue
		}
		var reserved bool
		if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM seo_targets WHERE entity_key=? AND locale=?)", "topic:"+demandSlug(q.Keyword), q.Locale).Scan(&reserved).Error; err != nil {
			return err
		}
		if reserved {
			continue
		}
		// Retain decisions and leases from the old rule, including daily-ID keys.
		legacy := legacySlugPattern.ReplaceAllString(seokeywords.Normalize(q.Keyword), "-")
		var existing seotasks.Task
		r := tx.Where("locale=? AND kind IN ('listing','topic') AND dedupe_key LIKE 'demand:%' AND evidence->>'source'='keywords' AND (entity_key=? OR evidence->>'targetSlug' IN ?)", q.Locale, s.EntityKey, []string{legacy, demandSlug(q.Keyword)}).Order("id").Limit(1).Find(&existing)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected > 0 {
			if existing.Kind != s.Kind || existing.EntityKey != s.EntityKey {
				continue
			}
			s.DedupeKey, s.Evidence = existing.DedupeKey, existing.Evidence
		}
		if err := emit(s); err != nil {
			return err
		}
		seen[s.DedupeKey] = true
		if len(seen) >= 500 {
			break
		}
	}
	return nil
}

// RepairDemand previews by default. Only unclaimed keyword work is changed;
// completed decisions, nominations and active leases remain intact.
func (s *Service) RepairDemand(ctx context.Context, apply bool) (int, error) {
	count := 0
	preview := errors.New("preview rollback")
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended('seo:opportunities',0))").Error; err != nil {
			return err
		}
		var err error
		count, err = repairDemand(ctx, tx)
		if err != nil {
			return err
		}
		if !apply {
			return preview
		}
		return nil
	})
	if errors.Is(err, preview) {
		err = nil
	}
	return count, err
}

func repairDemand(ctx context.Context, tx *gorm.DB) (int, error) {
	var tasks []seotasks.Task
	if err := tx.Raw(`SELECT * FROM seo_tasks WHERE kind IN ('listing','topic') AND dedupe_key LIKE 'demand:%'
 AND evidence->>'source'='keywords' AND rule_version='opportunities-v1' AND (state IN ('open','deferred') OR (state='leased' AND lease_until<=clock_timestamp()))
 ORDER BY id FOR UPDATE SKIP LOCKED`).Scan(&tasks).Error; err != nil {
		return 0, err
	}
	for _, task := range tasks {
		var evidence struct{ Keyword, TargetSlug string }
		if err := json.Unmarshal(task.Evidence, &evidence); err != nil {
			return 0, err
		}
		locale := "en-US"
		if task.Locale != nil {
			locale = *task.Locale
		}
		// The old evidence was reconstructed from a damaged slug. Recover the
		// real query from the ledger before choosing a replacement identity.
		var keyword string
		if err := tx.Raw(`SELECT keyword FROM seo_keywords WHERE language=? AND regexp_replace(trim(normalized),'[^a-z0-9]+','-','g')=? ORDER BY observed_at DESC,id DESC LIMIT 1`, locale, evidence.TargetSlug).Scan(&keyword).Error; err != nil {
			return 0, err
		}
		if keyword == "" {
			keyword = evidence.Keyword
		}
		s := demandSignal(demandQuery{Keyword: keyword, Locale: locale})
		var duplicate int64
		// Include this task when choosing a survivor. Otherwise two legacy
		// duplicates can each retire in favor of the other, losing all work.
		if err := tx.Raw(`SELECT id FROM seo_tasks WHERE kind=? AND entity_key=? AND locale=? AND state<>'expired'
 ORDER BY CASE WHEN state IN ('done','skipped','quarantined') THEN 0
 WHEN state='leased' AND lease_until>clock_timestamp() THEN 1 ELSE 2 END,id LIMIT 1`, s.Kind, s.EntityKey, locale).Scan(&duplicate).Error; err != nil {
			return 0, err
		}
		if duplicate == task.ID {
			duplicate = 0
		}
		updates := map[string]any{"rule_version": RuleVersion, "lease_owner": nil, "lease_until": nil, "pending_evidence": nil, "pending_evidence_hash": nil}
		if duplicate != 0 || s.Kind == "" {
			updates["state"] = "expired"
			updates["result"] = fmt.Sprintf(`{"reason":"demand_reclassified","replacementTaskId":%d}`, duplicate)
		} else {
			updates["kind"], updates["entity_key"] = s.Kind, s.EntityKey
			updates["evidence"], updates["evidence_hash"] = string(s.Evidence), seotasks.Hash(s.Evidence)
			if task.State == "leased" {
				updates["state"] = "open"
			}
			if task.Kind != s.Kind || task.EntityKey != s.EntityKey {
				before, _ := json.Marshal(map[string]any{"reason": "demand_reclassified", "previousKind": task.Kind, "previousEntityKey": task.EntityKey, "previousEvidence": json.RawMessage(task.Evidence)})
				updates["result"] = string(before)
			}
		}
		if err := tx.WithContext(ctx).Model(&seotasks.Task{}).Where("id=?", task.ID).Updates(updates).Error; err != nil {
			return 0, err
		}
		if err := tx.Exec("UPDATE seo_entity_leases SET lease_until=NULL,owner=NULL,task_id=NULL WHERE task_id=? AND (lease_until IS NULL OR lease_until<=clock_timestamp())", task.ID).Error; err != nil {
			return 0, err
		}
	}
	return len(tasks), nil
}
