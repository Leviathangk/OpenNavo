//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Frozen pre-optimization query: do not share predicates or scoring with production.
const legacySearchCTE = `WITH RECURSIVE selected_categories AS (SELECT id FROM categories WHERE slug=? UNION ALL SELECT c.id FROM categories c JOIN selected_categories parent ON c.parent_id=parent.id), queries AS (SELECT value AS q FROM jsonb_array_elements_text(?::jsonb)), matches AS (
 SELECT p.id,p.popularity,MAX((CASE WHEN p.token=queries.q OR EXISTS(SELECT 1 FROM unnest(p.names) n WHERE lower(n)=queries.q) THEN 3 ELSE 0 END+2.0*word_similarity(queries.q,p.search_text)+ts_rank_cd(p.search_vector,websearch_to_tsquery('english',queries.q))+p.popularity/12.0)*CASE WHEN p.is_font AND position('font' in queries.q)=0 THEN ? ELSE 1 END*CASE WHEN p.is_library THEN ? ELSE 1 END) AS score
 FROM packages p CROSS JOIN queries LEFT JOIN package_meta m ON m.package_id=p.id
 WHERE p.kind='cask' AND p.removed_at IS NULL AND coalesce(m.hidden,false)=false AND (? OR NOT (p.disabled OR p.stale_disabled)) AND (?='' OR p.kind=?) AND (?='' OR EXISTS(SELECT 1 FROM package_categories pc JOIN selected_categories sc ON sc.id=pc.category_id WHERE pc.package_id=p.id))
 AND (queries.q <% p.search_text OR p.search_text LIKE '%'||replace(replace(replace(queries.q,E'\\',E'\\\\'),'%',E'\\%'),'_',E'\\_')||'%' OR p.search_content LIKE '%'||replace(replace(replace(queries.q,E'\\',E'\\\\'),'%',E'\\%'),'_',E'\\_')||'%' OR p.search_vector @@ websearch_to_tsquery('english',queries.q)) GROUP BY p.id)`

func TestSearchCandidateQueryMatchesLegacy(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,names,version,version_base,raw,raw_hash,search_text,search_content,search_vector,popularity)
 SELECT n,CASE n WHEN 8 THEN 'link' WHEN 9 THEN 'formula' ELSE 'cask' END,
 'editor-'||n,'editor-'||n,'homebrew/cask','Editor',ARRAY['VS Code','Editor'],'1','1','{}','fixture',
 'vscode visual studio code editor font','代码编辑器 日本語エディター',to_tsvector('english','editing documents'),n%3
 FROM generate_series(1,12) n;
 UPDATE packages SET is_font=true WHERE id=2;
 UPDATE packages SET is_library=true WHERE id=3;
 UPDATE packages SET disabled=true WHERE id=4;
 UPDATE packages SET stale_disabled=true WHERE id=5;
 UPDATE packages SET removed_at=now() WHERE id=6;
 INSERT INTO package_meta(package_id,hidden) VALUES(7,true);
 INSERT INTO categories(id,slug,icon,applies_to) VALUES(9001,'search-parent','code','both');
 INSERT INTO categories(id,slug,parent_id,icon,applies_to) VALUES(9002,'search-child',9001,'code','both');
 INSERT INTO package_categories(package_id,category_id,is_primary,source) VALUES(1,9001,true,'human'),(2,9002,true,'human'),(8,9002,true,'human');
 UPDATE packages SET search_text='vscode',search_content='special 100% a_b path\file',search_vector=''::tsvector WHERE id=10;
 UPDATE packages SET search_text='',search_content='',search_vector=to_tsvector('english','edited') WHERE id=11;
 UPDATE packages SET search_text='',search_content='vscode',search_vector=''::tsvector WHERE id=12`).Error)
	cases := []struct {
		query string
		terms []string
	}{
		{"vscode", []string{"vscode", "visual studio code", "vs code", "vscode"}},
		{"vs code", []string{"vs code"}}, {"editor-1", []string{"editor-1"}},
		{"font", []string{"font"}}, {"edit", []string{"edit"}},
		{"代码", []string{"代码"}}, {"日本語", []string{"日本語"}},
		{"%", []string{"%"}}, {"_", []string{"_"}}, {`\`, []string{`\`}},
		{"100%", []string{"100%"}}, {"a_b", []string{"a_b"}}, {`path\file`, []string{`path\file`}},
		{"absent", []string{"never-match-xyz"}}, {"empty", []string{}},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			for _, links := range []bool{false, true} {
				local := domain.WithLinks(ctx, links)
				for _, kind := range []string{"", "cask", "link", "formula"} {
					for _, disabled := range []bool{false, true} {
						for _, category := range []string{"", "search-parent", "search-child", "missing"} {
							input := domain.SearchInput{Query: c.query, Kind: kind, Category: category, IncludeDisabled: disabled, Current: 1, Size: 100}
							want := legacySearch(t, st, local, input, c.terms)
							for _, page := range []struct{ current, size int }{{1, 100}, {1, 2}, {2, 2}, {100, 2}, {0, 2}} {
								input.Current, input.Size = page.current, page.size
								got, total, err := st.Search(local, input, c.terms, 0.3, 0.2)
								require.NoError(t, err)
								require.Equal(t, int64(len(want)), total)
								offset, found := domain.PageOffset(page.current, page.size, total)
								expected := []domain.SearchMatch{}
								if found {
									expected = want[offset:min(offset+page.size, len(want))]
								}
								require.Equal(t, expected, got, fmt.Sprintf("links=%v input=%+v", links, input))
							}
						}
					}
				}
			}
		})
	}
}

func legacySearch(t *testing.T, st *store.Store, ctx context.Context, in domain.SearchInput, terms []string) []domain.SearchMatch {
	t.Helper()
	encoded, err := json.Marshal(terms)
	require.NoError(t, err)
	query := legacySearchCTE
	if domain.LinksAllowed(ctx) {
		query = strings.ReplaceAll(query, "p.kind='cask'", "p.kind IN ('cask','link')")
	}
	result := []domain.SearchMatch{}
	require.NoError(t, st.DB.WithContext(ctx).Raw(query+` SELECT matches.id,matches.score,(SELECT n FROM unnest(p.names) n WHERE lower(n)=? AND lower(n)<>p.token LIMIT 1) AS matched_name FROM matches JOIN packages p ON p.id=matches.id ORDER BY score DESC,matches.popularity DESC,p.token`, in.Category, string(encoded), 0.3, 0.2, in.IncludeDisabled, in.Kind, in.Kind, in.Category, in.Query).Scan(&result).Error)
	return result
}
