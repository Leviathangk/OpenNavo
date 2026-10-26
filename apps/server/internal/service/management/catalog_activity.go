package management

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

// A build shows its own editorial note, otherwise the shared note of its base version.
var editorialAtVersion = store.EditorialNoteSQL("v.id", "p.id", "v.version_base") + " IS NOT NULL"

func (s *Service) adminVersions(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	where, args := "p.kind='cask'", []any{}
	if id := number(in.Params, strings.ToLower("packageId"), 0); id > 0 {
		where += " AND p.id=?"
		args = append(args, id)
	}
	if q := text(in.Params, "q"); q != "" {
		where += " AND (strpos(lower(p.token),lower(?))>0 OR strpos(lower(p.name),lower(?))>0 OR strpos(lower(v.version),lower(?))>0)"
		args = append(args, q, q, q)
	}
	if has, ok := in.Params["haseditorial"].(bool); ok {
		where += " AND (" + editorialAtVersion + ")=?"
		args = append(args, has)
	}
	order := "COALESCE(v.brew_committed_at,v.first_seen_at) DESC,v.id DESC"
	if text(in.Params, "sort") == "popular" {
		order = "p.popularity DESC,v.id DESC"
	}
	expr := `jsonb_build_object('packageVersionId',v.id,'packageId',p.id,'token',p.token,'version',v.version,'versionBase',v.version_base,'firstSeenAt',v.first_seen_at,'brewCommittedAt',v.brew_committed_at,'rank30d',p.rank_30d,'hasEditorial',` + editorialAtVersion + `,'editorialReleaseId',` + store.EditorialNoteSQL("v.id", "p.id", "v.version_base") + `,'editorialScope',` + store.EditorialScopeSQL("v.id", "p.id", "v.version_base") + `,'historicalReleaseId',(SELECT id FROM releases r WHERE r.package_id=p.id AND r.version=v.version_base AND r.source<>'editorial' AND NOT r.hidden ORDER BY published_at DESC NULLS LAST,id DESC LIMIT 1))`
	return s.Store.AdminPage(ctx, expr, "FROM package_versions v JOIN packages p ON p.id=v.package_id", where, order, args, current, size)
}
func (s *Service) catalogActivity(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	afterSeq, cursor, err := catalogActivityCursor(in.Params, current)
	var from, to time.Time
	if err == nil && !cursor {
		from, to, err = catalogActivityRange(in.Params, s.now())
	}
	if err != nil {
		requestID := ""
		if op := domain.CurrentOperation(ctx); op != nil {
			requestID = op.RequestID
		}
		slog.WarnContext(ctx, "catalog activity validation failed", "requestId", requestID, "reason", err.Error())
		return nil, err
	}
	// Casks from Homebrew and third-party taps and link apps; formulae have no pages.
	where, args := "c.kind IN ('cask','link') AND c.event_type IS NOT NULL", []any{}
	if !cursor {
		where += " AND c.created_at>=? AND c.created_at<?"
		args = append(args, from, to)
	}
	if kind := text(in.Params, "type"); kind != "" {
		where += " AND c.event_type=?"
		args = append(args, kind)
	}
	// Events record the full build version (older rows only the base), so a build note counts only for its own build.
	base := "split_part(c.version,',',1)"
	hasExpr := store.EditorialNoteSQL("pv.id", "c.package_id", base) + " IS NOT NULL"
	if has, ok := in.Params["haseditorial"].(bool); ok {
		where += " AND (" + hasExpr + ")=?"
		args = append(args, has)
	}
	order := "c.created_at DESC,c.seq DESC"
	if text(in.Params, "sort") == "popular" {
		order = "p.popularity DESC NULLS LAST,c.seq DESC"
	}
	source := "CASE WHEN c.kind='link' THEN 'link' WHEN p.tap IS NULL OR p.tap='' OR p.tap LIKE 'homebrew/%' THEN 'homebrew' ELSE 'tap' END"
	expr := `jsonb_build_object('seq',c.seq,'packageId',c.package_id,'token',c.token,'kind',c.kind,'source',` + source + `,'type',c.event_type,'changedAt',c.created_at,'version',c.version,'versionBase',` + base + `,'packageVersionId',pv.id,'previousVersion',c.previous_version,'previousToken',c.previous_token,'rank30d',p.rank_30d,'popularity',COALESCE(p.popularity,0),'hasEditorial',` + hasExpr + `,'editorialScope',` + store.EditorialScopeSQL("pv.id", "c.package_id", base) + `,'webUrl',` + sqlString(strings.TrimRight(s.Config.WebBaseURL, "/")+"/apps/") + `||c.token)`
	tables := "FROM catalog_changes c LEFT JOIN packages p ON p.id=c.package_id LEFT JOIN package_versions pv ON pv.package_id=c.package_id AND pv.version=c.version"
	if !cursor {
		result, err := s.Store.AdminPage(ctx, expr, tables, where, order, args, current, size)
		if err != nil {
			return nil, err
		}
		head, err := s.catalogActivityHead(ctx)
		if err != nil {
			return nil, err
		}
		return catalogActivityPage{AdminPage: result, NextSeq: head}, nil
	}
	return s.catalogActivityAfter(ctx, expr, tables, where, args, afterSeq, size)
}

// catalogActivityPage is an activity page plus the cursor that continues after it.
type catalogActivityPage struct {
	store.AdminPage
	NextSeq int64 `json:"nextSeq"`
}

// catalogActivityHead is the newest event a cursor may stand on. Event rows are written under the catalog
// lock, so their seq follows commit order and no later commit can appear below it.
func (s *Service) catalogActivityHead(ctx context.Context) (int64, error) {
	var head int64
	err := s.Store.DB.WithContext(ctx).Raw("SELECT COALESCE(max(seq),0) FROM catalog_changes WHERE event_type IS NOT NULL AND kind IN ('cask','link')").Scan(&head).Error
	return head, err
}

// catalogActivityAfter reads events after a cursor in seq order. nextSeq is the last event returned while more
// remain, otherwise the head, so a caller that stores it resumes without gaps or repeats. A cursor older than
// the retained 14 days is rejected because pruned events cannot be replayed; afterSeq=0 starts at the oldest
// retained event.
func (s *Service) catalogActivityAfter(ctx context.Context, expr, from, where string, args []any, afterSeq int64, size int) (any, error) {
	head, err := s.catalogActivityHead(ctx)
	if err != nil {
		return nil, err
	}
	retention := s.now().Add(-14 * 24 * time.Hour)
	if afterSeq == 0 {
		where += " AND c.created_at>=?"
		args = append(args, retention)
	} else {
		var low int64
		if err := s.Store.DB.WithContext(ctx).Raw(`SELECT GREATEST(COALESCE((SELECT max(seq) FROM catalog_changes WHERE created_at<?),0),
 COALESCE((SELECT value::text::bigint FROM app_config WHERE key='catalog.retentionCursor'),0))`, retention).Scan(&low).Error; err != nil {
			return nil, err
		}
		if afterSeq < low {
			return nil, domain.Rejected("cursor_before_retention:afterSeq")
		}
	}
	where += " AND c.seq>? AND c.seq<=?"
	args = append(args, afterSeq, head)
	result, err := s.Store.AdminPage(ctx, expr, from, where, "c.seq", args, 1, size)
	if err != nil {
		return nil, err
	}
	next := head
	if result.Total > int64(len(result.Records)) {
		var last struct {
			Seq int64 `json:"seq"`
		}
		if err := json.Unmarshal(result.Records[len(result.Records)-1], &last); err != nil {
			return nil, err
		}
		next = last.Seq
	}
	return catalogActivityPage{AdminPage: result, NextSeq: next}, nil
}

// catalogActivityCursor reads afterSeq. A cursor read pages by nextSeq alone, so it takes no time window,
// popularity order or page number.
func catalogActivityCursor(params map[string]any, current int) (int64, bool, error) {
	raw, present := params["afterseq"]
	if !present || raw == nil {
		return 0, false, nil
	}
	value, ok := raw.(float64)
	if !ok || value < 0 || value != math.Trunc(value) || value >= 1<<53 {
		return 0, false, domain.Rejected("parameter_invalid:afterSeq")
	}
	for _, key := range []string{"from", "to"} {
		if text(params, key) != "" {
			return 0, false, domain.Rejected("parameter_conflict:" + key)
		}
	}
	if text(params, "sort") == "popular" {
		return 0, false, domain.Rejected("parameter_conflict:sort")
	}
	if current != 1 {
		return 0, false, domain.Rejected("parameter_conflict:current")
	}
	return int64(value), true, nil
}

// Empty optional dates use defaults; all nonempty dates must include a timezone.
func catalogActivityRange(params map[string]any, now time.Time) (time.Time, time.Time, error) {
	from, to := now.Add(-24*time.Hour), now
	for _, item := range []struct {
		key    string
		target *time.Time
	}{{"from", &from}, {"to", &to}} {
		if v := text(params, item.key); v != "" {
			parsed, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return from, to, domain.Rejected("date_time_invalid:" + item.key)
			}
			*item.target = parsed
		}
	}
	if !from.Before(to) {
		return from, to, domain.Rejected("date_range_order:from")
	}
	if from.Before(now.Add(-14 * 24 * time.Hour)) {
		return from, to, domain.Rejected("date_before_retention:from")
	}
	if to.After(now.Add(time.Minute)) {
		return from, to, domain.Rejected("date_in_future:to")
	}
	return from, to, nil
}

func sqlString(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

// currentBuild is the recorded build of a package's current version.
const currentBuild = `(SELECT b.id FROM package_versions b WHERE b.package_id=p.id AND b.version=p.version)`

func gapConditions(in Input) (string, error) {
	raw, present := in.Params["gaps"]
	if !present {
		return "", nil
	}
	encoded, _ := json.Marshal(raw)
	var gaps []string
	if json.Unmarshal(encoded, &gaps) != nil {
		return "", domain.Rejected("parameter_invalid:gaps")
	}
	known := map[string]string{"zhName": `NOT EXISTS(SELECT 1 FROM package_i18n g WHERE g.package_id=p.id AND g.locale='zh-CN' AND btrim(COALESCE(g.display_name,''))<>'')`, "zhSummary": `NOT EXISTS(SELECT 1 FROM package_i18n g WHERE g.package_id=p.id AND g.locale='zh-CN' AND btrim(COALESCE(g.summary,''))<>'')`, "enSummary": `NOT EXISTS(SELECT 1 FROM package_i18n g WHERE g.package_id=p.id AND g.locale='en-US' AND btrim(COALESCE(g.summary,''))<>'')`, "primaryCategory": `NOT EXISTS(SELECT 1 FROM package_categories g WHERE g.package_id=p.id AND g.is_primary)`, "tags": `COALESCE(cardinality(m.tags),0)=0`, "icon": `m.icon_asset_id IS NULL`, "downloadSize": `p.download_size IS NULL`, "latestEditorial": store.EditorialNoteSQL(currentBuild, "p.id", "p.version_base") + " IS NULL", "latestBuildEditorial": `p.version<>p.version_base AND ` + store.BuildNoteSQL(currentBuild) + ` IS NULL AND (SELECT count(*) FROM package_versions b WHERE b.package_id=p.id AND b.version_base=p.version_base)>1`, "price": `NOT EXISTS(SELECT 1 FROM package_facts f WHERE f.package_id=p.id AND f.price->>'status'='verified' AND (f.price->>'verifiedAt')::timestamptz > now()-interval '365 days')`}
	conditions := []string{}
	for _, gap := range gaps {
		v, ok := known[gap]
		if !ok {
			return "", domain.RejectedDetail("parameter_invalid", "gaps", gap)
		}
		conditions = append(conditions, "("+v+")")
	}
	join := " OR "
	if text(in.Params, "gapmode") == "all" {
		join = " AND "
	}
	return "(" + strings.Join(conditions, join) + ")", nil
}
