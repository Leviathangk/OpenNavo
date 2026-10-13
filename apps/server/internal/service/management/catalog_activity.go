package management

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
)

const editorialAtVersion = `EXISTS(SELECT 1 FROM releases r WHERE r.package_id=p.id AND r.version=v.version_base AND r.source='editorial' AND NOT r.hidden)`

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
	expr := `jsonb_build_object('packageVersionId',v.id,'packageId',p.id,'token',p.token,'version',v.version,'versionBase',v.version_base,'firstSeenAt',v.first_seen_at,'brewCommittedAt',v.brew_committed_at,'rank30d',p.rank_30d,'hasEditorial',` + editorialAtVersion + `,'editorialReleaseId',(SELECT id FROM releases r WHERE r.package_id=p.id AND r.version=v.version_base AND r.source='editorial' AND NOT r.hidden ORDER BY id DESC LIMIT 1),'historicalReleaseId',(SELECT id FROM releases r WHERE r.package_id=p.id AND r.version=v.version_base AND r.source<>'editorial' AND NOT r.hidden ORDER BY published_at DESC NULLS LAST,id DESC LIMIT 1))`
	return s.Store.AdminPage(ctx, expr, "FROM package_versions v JOIN packages p ON p.id=v.package_id", where, order, args, current, size)
}
func (s *Service) catalogActivity(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	now := s.now()
	from, to := now.Add(-24*time.Hour), now
	for k, target := range map[string]*time.Time{"from": &from, "to": &to} {
		if v := text(in.Params, k); v != "" {
			*target, err = time.Parse(time.RFC3339, v)
			if err != nil {
				return nil, domain.Validation()
			}
		}
	}
	if !from.Before(to) || from.Before(now.Add(-14*24*time.Hour)) || to.After(now.Add(time.Minute)) {
		return nil, domain.Validation()
	}
	where, args := "c.kind='cask' AND c.event_type IS NOT NULL AND c.created_at>=? AND c.created_at<?", []any{from, to}
	if kind := text(in.Params, "type"); kind != "" {
		where += " AND c.event_type=?"
		args = append(args, kind)
	}
	hasExpr := `EXISTS(SELECT 1 FROM releases r WHERE r.package_id=c.package_id AND r.version=split_part(c.version,',',1) AND r.source='editorial' AND NOT r.hidden)`
	if has, ok := in.Params["haseditorial"].(bool); ok {
		where += " AND (" + hasExpr + ")=?"
		args = append(args, has)
	}
	order := "c.created_at DESC,c.seq DESC"
	if text(in.Params, "sort") == "popular" {
		order = "p.popularity DESC NULLS LAST,c.seq DESC"
	}
	expr := `jsonb_build_object('seq',c.seq,'packageId',c.package_id,'token',c.token,'type',c.event_type,'changedAt',c.created_at,'version',c.version,'previousVersion',c.previous_version,'previousToken',c.previous_token,'rank30d',p.rank_30d,'popularity',COALESCE(p.popularity,0),'hasEditorial',` + hasExpr + `,'webUrl',` + sqlString(strings.TrimRight(s.Config.WebBaseURL, "/")+"/apps/") + `||c.token)`
	return s.Store.AdminPage(ctx, expr, "FROM catalog_changes c LEFT JOIN packages p ON p.id=c.package_id", where, order, args, current, size)
}
func sqlString(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
func gapConditions(in Input) (string, error) {
	raw, present := in.Params["gaps"]
	if !present {
		return "", nil
	}
	encoded, _ := json.Marshal(raw)
	var gaps []string
	if json.Unmarshal(encoded, &gaps) != nil {
		return "", domain.Validation()
	}
	known := map[string]string{"zhName": `NOT EXISTS(SELECT 1 FROM package_i18n g WHERE g.package_id=p.id AND g.locale='zh-CN' AND btrim(COALESCE(g.display_name,''))<>'')`, "zhSummary": `NOT EXISTS(SELECT 1 FROM package_i18n g WHERE g.package_id=p.id AND g.locale='zh-CN' AND btrim(COALESCE(g.summary,''))<>'')`, "enSummary": `NOT EXISTS(SELECT 1 FROM package_i18n g WHERE g.package_id=p.id AND g.locale='en-US' AND btrim(COALESCE(g.summary,''))<>'')`, "primaryCategory": `NOT EXISTS(SELECT 1 FROM package_categories g WHERE g.package_id=p.id AND g.is_primary)`, "tags": `COALESCE(cardinality(m.tags),0)=0`, "icon": `m.icon_asset_id IS NULL`, "downloadSize": `p.download_size IS NULL`, "latestEditorial": `NOT EXISTS(SELECT 1 FROM releases r WHERE r.package_id=p.id AND r.version=p.version_base AND r.source='editorial' AND NOT r.hidden)`, "price": `NOT EXISTS(SELECT 1 FROM package_facts f WHERE f.package_id=p.id AND f.price->>'status'='verified' AND (f.price->>'verifiedAt')::timestamptz > now()-interval '365 days')`}
	conditions := []string{}
	for _, gap := range gaps {
		v, ok := known[gap]
		if !ok {
			return "", domain.Validation()
		}
		conditions = append(conditions, "("+v+")")
	}
	join := " OR "
	if text(in.Params, "gapmode") == "all" {
		join = " AND "
	}
	return "(" + strings.Join(conditions, join) + ")", nil
}
