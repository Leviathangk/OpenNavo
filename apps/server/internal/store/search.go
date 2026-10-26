package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/searchtext"
	"gorm.io/gorm"
)

// Keep translated prose separate from names so descriptions do not dilute name
// similarity scores or turn the name/pinyin autocomplete into a prose search.
const searchContentSQL = `lower(normalize(coalesce((SELECT string_agg(concat_ws(' ',t.summary,t.description),' ' ORDER BY t.locale) FROM package_i18n t WHERE t.package_id=p.id),''),NFKC))`

func UpdateSearchIndex(ctx context.Context, tx *gorm.DB, ids []int64) error {
	var rows []struct {
		ID                                        int64
		Token, Names, DisplayName, Tags, Binaries string
	}
	query := tx.WithContext(ctx).Raw("SELECT p.id,p.token,to_json(p.names)::text AS names,coalesce((SELECT string_agg(t.display_name,' ' ORDER BY t.locale) FROM package_i18n t WHERE t.package_id=p.id),'') AS display_name,coalesce(to_json(m.tags)::text,'[]') AS tags,coalesce(p.artifacts->'binaries','[]'::jsonb)::text AS binaries FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.id IN ?", ids)
	if err := query.Scan(&rows).Error; err != nil {
		return fmt.Errorf("load search index inputs: %w", err)
	}
	groups := []string{}
	arguments := []any{}
	for _, row := range rows {
		var names, tags, binaries []string
		for _, input := range []struct {
			Encoded string
			Target  *[]string
		}{{row.Names, &names}, {row.Tags, &tags}, {row.Binaries, &binaries}} {
			if err := json.Unmarshal([]byte(input.Encoded), input.Target); err != nil {
				return fmt.Errorf("decode search index inputs: %w", err)
			}
		}
		text := searchtext.Build(row.Token, names, row.DisplayName, tags, binaries)
		groups = append(groups, "(?::bigint,?::text)")
		arguments = append(arguments, row.ID, text)
	}
	if len(groups) == 0 {
		return nil
	}
	statement := "UPDATE packages p SET search_content=" + searchContentSQL + ",search_text=v.search_text,search_vector=setweight(to_tsvector('simple',p.token||' '||p.name),'A')||setweight(to_tsvector('english',coalesce(p.desc_en,'')),'C') FROM (VALUES " + strings.Join(groups, ",") + ") AS v(id,search_text) WHERE p.id=v.id AND (p.search_content IS DISTINCT FROM " + searchContentSQL + " OR p.search_text IS DISTINCT FROM v.search_text OR p.search_vector IS DISTINCT FROM setweight(to_tsvector('simple',p.token||' '||p.name),'A')||setweight(to_tsvector('english',coalesce(p.desc_en,'')),'C'))"
	if err := tx.WithContext(ctx).Exec(statement, arguments...).Error; err != nil {
		return fmt.Errorf("update search index: %w", err)
	}
	return nil
}

func (s *Store) ReindexSearch(ctx context.Context) (int, error) {
	var ids []int64
	if err := s.DB.WithContext(ctx).Raw("SELECT id FROM packages ORDER BY id").Scan(&ids).Error; err != nil {
		return 0, fmt.Errorf("load reindex IDs: %w", err)
	}
	for start := 0; start < len(ids); start += 500 {
		if err := s.WithTx(ctx, func(tx *gorm.DB) error { return UpdateSearchIndex(ctx, tx, ids[start:min(start+500, len(ids))]) }); err != nil {
			return start, err
		}
	}
	return len(ids), nil
}

func (s *Store) SearchSynonyms(ctx context.Context, query string) ([]string, error) {
	var terms []string
	if err := s.DB.WithContext(ctx).Raw("SELECT DISTINCT unnest(terms) FROM search_synonyms WHERE enabled AND ?=ANY(terms)", query).Scan(&terms).Error; err != nil {
		return nil, fmt.Errorf("load search synonyms: %w", err)
	}
	return terms, nil
}

// Keep each predicate in its own branch so PostgreSQL can use its GIN index.
// Deduplicate (package, term) before scoring: a package may match several branches.
const searchCTE = `WITH RECURSIVE selected_categories AS (SELECT id FROM categories WHERE slug=? UNION ALL SELECT c.id FROM categories c JOIN selected_categories parent ON c.parent_id=parent.id), queries AS (SELECT value AS q FROM jsonb_array_elements_text(?::jsonb)), candidates AS (
 SELECT DISTINCT c.id,queries.q FROM queries CROSS JOIN LATERAL (
SELECT p.id FROM packages p WHERE queries.q <% p.search_text
 UNION ALL SELECT p.id FROM packages p WHERE p.search_text LIKE '%'||replace(replace(replace(queries.q,E'\\',E'\\\\'),'%',E'\\%'),'_',E'\\_')||'%'
 UNION ALL SELECT p.id FROM packages p WHERE p.search_content LIKE '%'||replace(replace(replace(queries.q,E'\\',E'\\\\'),'%',E'\\%'),'_',E'\\_')||'%'
 UNION ALL SELECT p.id FROM packages p WHERE p.search_vector @@ websearch_to_tsquery('english',queries.q)
 ) c), matches AS (
 SELECT p.id,p.popularity,MAX((CASE WHEN p.token=candidates.q OR EXISTS(SELECT 1 FROM unnest(p.names) n WHERE lower(n)=candidates.q) THEN 3 ELSE 0 END+2.0*word_similarity(candidates.q,p.search_text)+ts_rank_cd(p.search_vector,websearch_to_tsquery('english',candidates.q))+p.popularity/12.0)*CASE WHEN p.is_font AND position('font' in candidates.q)=0 THEN ? ELSE 1 END*CASE WHEN p.is_library THEN ? ELSE 1 END) AS score
 FROM candidates JOIN packages p ON p.id=candidates.id LEFT JOIN package_meta m ON m.package_id=p.id
 WHERE p.kind='cask' AND p.removed_at IS NULL AND coalesce(m.hidden,false)=false AND (? OR NOT (p.disabled OR p.stale_disabled)) AND (?='' OR p.kind=?) AND (?='' OR EXISTS(SELECT 1 FROM package_categories pc JOIN selected_categories sc ON sc.id=pc.category_id WHERE pc.package_id=p.id)) GROUP BY p.id)`

func (s *Store) Search(ctx context.Context, input domain.SearchInput, terms []string, fontPenalty, libraryPenalty float64) ([]domain.SearchMatch, int64, error) {
	encoded, err := json.Marshal(terms)
	if err != nil {
		return nil, 0, err
	}
	arguments := []any{input.Category, string(encoded), fontPenalty, libraryPenalty, input.IncludeDisabled, input.Kind, input.Kind, input.Category}
	var total int64
	if err := s.DB.WithContext(ctx).Raw(linkSQL(ctx, searchCTE)+" SELECT count(*) FROM matches", arguments...).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count search matches: %w", err)
	}
	offset, found := domain.PageOffset(input.Current, input.Size, total)
	if !found {
		return []domain.SearchMatch{}, total, nil
	}
	var records []domain.SearchMatch
	args := append(append([]any{}, arguments...), input.Query, input.Size, offset)
	if err := s.DB.WithContext(ctx).Raw(linkSQL(ctx, searchCTE)+" SELECT matches.id,matches.score,(SELECT n FROM unnest(p.names) n WHERE lower(n)=? AND lower(n)<>p.token LIMIT 1) AS matched_name FROM matches JOIN packages p ON p.id=matches.id ORDER BY score DESC,matches.popularity DESC,p.token LIMIT ? OFFSET ?", args...).Scan(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("find search matches: %w", err)
	}
	if records == nil {
		records = []domain.SearchMatch{}
	}
	return records, total, nil
}

func (s *Store) SearchPenalty(ctx context.Context, key string, fallback float64) float64 {
	var encoded string
	if err := s.DB.WithContext(ctx).Raw("SELECT value::text FROM app_config WHERE key=?", key).Scan(&encoded).Error; err != nil || encoded == "" {
		return fallback
	}
	var value float64
	if json.Unmarshal([]byte(encoded), &value) != nil || value < 0 || value > 1 {
		return fallback
	}
	return value
}

func (s *Store) Suggest(ctx context.Context, query, locale string, limit int) ([]domain.Suggestion, error) {
	var records []domain.Suggestion
	prefix := searchtext.EscapeLike(query) + "%"
	if err := s.DB.WithContext(ctx).Raw("SELECT 'package' AS type,p.source_locale,COALESCE(i.machine_translated,false) machine_translated,p.kind,p.token,coalesce(i.display_name,p.name) AS name,coalesce(i.summary,p.desc_en) AS summary,a.url AS icon_url,p.version FROM packages p LEFT JOIN LATERAL (SELECT t.* FROM package_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE COALESCE(NULLIF(t.display_name,''),NULLIF(t.summary,''),NULLIF(t.description,'')) IS NOT NULL AND t.package_id=p.id AND t.locale IN (lang.requested,p.source_locale,'en-US') ORDER BY "+publicLocaleOrder+" LIMIT 1) i ON TRUE LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN assets a ON a.id=m.icon_asset_id WHERE "+linkSQL(ctx, "p.kind='cask'")+" AND p.removed_at IS NULL AND NOT (p.disabled OR p.stale_disabled) AND NOT coalesce(m.hidden,false) AND (p.token LIKE ? OR lower(coalesce(i.display_name,'')) LIKE ? OR EXISTS(SELECT 1 FROM unnest(p.names) n WHERE lower(n) LIKE ?) OR p.search_text LIKE ?) ORDER BY p.popularity DESC,p.token LIMIT ?", locale, prefix, prefix, prefix, "% "+prefix, limit).Scan(&records).Error; err != nil {
		return nil, fmt.Errorf("find search suggestions: %w", err)
	}
	if len(records) < limit {
		var categories []domain.Suggestion
		if err := s.DB.WithContext(ctx).Raw("SELECT 'category' AS type,c.source_locale,COALESCE(i.machine_translated,false) machine_translated,c.slug,coalesce(i.name,c.slug) AS name FROM categories c LEFT JOIN LATERAL (SELECT t.* FROM category_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE NULLIF(t.name,'') IS NOT NULL AND t.category_id=c.id AND t.locale IN (lang.requested,c.source_locale,'en-US') ORDER BY "+publicLocaleOrder+" LIMIT 1) i ON TRUE WHERE "+publicCategoryVisible+" AND (c.slug LIKE ? OR lower(i.name) LIKE ?) ORDER BY c.sort LIMIT ?", locale, prefix, prefix, limit-len(records)).Scan(&categories).Error; err != nil {
			return nil, fmt.Errorf("find category suggestions: %w", err)
		}
		records = append(records, categories...)
	}
	if records == nil {
		records = []domain.Suggestion{}
	}
	return records, nil
}

func (s *Store) ReserveQueryID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.DB.WithContext(ctx).Raw("SELECT nextval(pg_get_serial_sequence('search_queries','id'))").Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("reserve search query ID: %w", err)
	}
	return id, nil
}
func (s *Store) SaveSearchQuery(ctx context.Context, query domain.SearchQueryLog) error {
	if err := s.DB.WithContext(ctx).Exec("INSERT INTO search_queries (id,query,normalized,locale,platform,result_count) VALUES (?,?,?,?,?,?)", query.ID, query.Query, query.Normalized, query.Locale, query.Platform, query.ResultCount).Error; err != nil {
		return fmt.Errorf("save search query: %w", err)
	}
	return nil
}
func (s *Store) SaveSearchClick(ctx context.Context, queryID int64, kind, token string, position int) error {
	if err := s.DB.WithContext(ctx).Exec("UPDATE search_queries q SET clicked_package_id=p.id,clicked_position=? FROM packages p WHERE q.id=? AND p.kind=? AND p.token=?", position, queryID, kind, token).Error; err != nil {
		return fmt.Errorf("save search click: %w", err)
	}
	return nil
}
