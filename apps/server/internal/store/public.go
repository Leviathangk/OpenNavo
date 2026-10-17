package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

type PublicI18n struct {
	MachineTranslated                 bool `json:"machine_translated"`
	DisplayName, Summary, Description *string
	Status, Source, Locale            string
	SourceLocale                      string `json:"source_locale"`
	SourceHash                        string `json:"source_hash"`
}
type PublicMeta struct {
	Developer, RepoURL, AccentColor *string
	Hidden, EditorChoice            bool
	Tags                            []string
	IconSource                      string `json:"icon_source"`
}

func (m *PublicMeta) UnmarshalJSON(value []byte) error {
	type alias PublicMeta
	var v struct {
		*alias
		RepoURL      *string `json:"repo_url"`
		AccentColor  *string `json:"accent_color"`
		EditorChoice bool    `json:"editor_choice"`
	}
	v.alias = (*alias)(m)
	if err := json.Unmarshal(value, &v); err != nil {
		return err
	}
	m.RepoURL, m.AccentColor, m.EditorChoice = v.RepoURL, v.AccentColor, v.EditorChoice
	return nil
}
func (i *PublicI18n) UnmarshalJSON(value []byte) error {
	type alias PublicI18n
	var v struct {
		*alias
		DisplayName *string `json:"display_name"`
	}
	v.alias = (*alias)(i)
	if err := json.Unmarshal(value, &v); err != nil {
		return err
	}
	i.DisplayName = v.DisplayName
	return nil
}

type PublicCategory struct {
	SourceLocale      string `json:"source_locale"`
	MachineTranslated bool   `json:"machine_translated"`
	ID                int64  `json:"id"`
	ParentID          *int64 `json:"parent_id"`
	Slug, Name, Icon  string
	Description       *string
	AppliesTo         string   `json:"applies_to"`
	HiddenByDefault   bool     `json:"hidden_by_default"`
	IsPrimary         bool     `json:"is_primary"`
	PackageCount      int      `json:"package_count"`
	Confidence        *float64 `json:"confidence"`
}
type PublicPackage struct {
	Link                                   *publicapi.LinkInfo `json:"link"`
	BundleIDs                              *[]string           `json:"bundle_ids"`
	SourceLocale                           string              `json:"source_locale"`
	ID                                     int64
	Kind, Token, Name, Tap, Version        string
	VersionBase                            string `json:"version_base"`
	Names                                  []string
	DescEN                                 *string `json:"desc_en"`
	Homepage, License, Caveats             *string
	DownloadURL                            *string `json:"download_url"`
	DownloadSize                           *int64  `json:"download_size"`
	DownloadSHA256                         *string `json:"download_sha256"`
	MinMacos                               *string `json:"min_macos"`
	AutoUpdates                            bool    `json:"auto_updates"`
	KegOnly                                bool    `json:"keg_only"`
	SupportsArm64                          bool    `json:"supports_arm64"`
	SupportsX8664                          bool    `json:"supports_x86_64"`
	Deprecated, Disabled                   bool
	UpstreamDisabled                       bool    `json:"upstream_disabled"`
	StaleDisabled                          bool    `json:"stale_disabled"`
	IsFont                                 bool    `json:"is_font"`
	IsLibrary                              bool    `json:"is_library"`
	DeprecationDate                        *string `json:"deprecation_date"`
	DeprecationReason                      *string `json:"deprecation_reason"`
	DeprecationReplacement                 *string `json:"deprecation_replacement"`
	DisableDate                            *string `json:"disable_date"`
	DisableReason                          *string `json:"disable_reason"`
	DisableReplacement                     *string `json:"disable_replacement"`
	Installs30d                            int     `json:"installs_30d"`
	Installs90d                            int     `json:"installs_90d"`
	Installs365d                           int     `json:"installs_365d"`
	OnRequest30d                           int     `json:"on_request_30d"`
	OnRequest90d                           int     `json:"on_request_90d"`
	OnRequest365d                          int     `json:"on_request_365d"`
	Rank30d                                *int    `json:"rank_30d"`
	Popularity                             float64
	VersionChangedAt                       time.Time `json:"version_changed_at"`
	UpdatedAt                              time.Time `json:"updated_at"`
	Artifacts, Dependencies, ConflictsWith json.RawMessage
	Meta                                   PublicMeta
	I18n                                   PublicI18n
	IconURL                                *string `json:"icon_url"`
	SourcePath                             string  `json:"source_path"`
	Categories                             []PublicCategory
}

// Configuration JSON names match database columns; convert to API models at the boundary so domain does not import generated code.
func (p *PublicPackage) UnmarshalJSON(value []byte) error {
	type alias PublicPackage
	var v struct {
		*alias
		ConflictsWith json.RawMessage `json:"conflicts_with"`
	}
	v.alias = (*alias)(p)
	if err := json.Unmarshal(value, &v); err != nil {
		return err
	}
	p.ConflictsWith = v.ConflictsWith
	return nil
}

type PublicFilter struct {
	Kind, Category, Sort, Period, Locale            string
	Current, Size                                   int
	IncludeFonts, IncludeLibraries, IncludeDisabled bool
}

func linkSQL(ctx context.Context, query string) string {
	if !domain.LinksAllowed(ctx) {
		return query
	}
	query = strings.ReplaceAll(query, "p.kind='cask'", "p.kind IN ('cask','link')")
	return query
}

const publicJoins = ` FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id`
const publicVisible = `p.kind='cask' AND p.removed_at IS NULL AND NOT COALESCE(m.hidden,false)`

// Empty categories may be curated in advance; subtrees containing only Formula entries do not enter the catalog.
const publicCategoryVisible = `c.visible AND (NOT EXISTS (WITH RECURSIVE subtree AS (SELECT c.id UNION ALL SELECT child.id FROM categories child JOIN subtree parent ON child.parent_id=parent.id) SELECT 1 FROM package_categories pc JOIN subtree ON subtree.id=pc.category_id JOIN packages legacy_package ON legacy_package.id=pc.package_id WHERE legacy_package.kind<>'link') OR EXISTS (WITH RECURSIVE subtree AS (SELECT c.id UNION ALL SELECT child.id FROM categories child JOIN subtree parent ON child.parent_id=parent.id) SELECT 1 FROM package_categories pc JOIN subtree ON subtree.id=pc.category_id JOIN packages cp ON cp.id=pc.package_id WHERE cp.kind='cask')) AND c.applies_to<>'formula'`
const packageView = `SELECT (to_jsonb(p)-ARRAY['raw','raw_hash','search_text','search_vector']) ||
 jsonb_build_object('link',(SELECT jsonb_build_object('homepage',l.homepage,'downloadUrl',l.download_url,'github',CASE WHEN l.github_repo IS NULL THEN NULL ELSE jsonb_build_object('repo',l.github_repo,'latest',l.latest) END,'teamId',l.team_id,'pricing',l.pricing,'notes',l.notes,'adoptedByCask',NULL) FROM link_apps l WHERE l.package_id=p.id AND p.kind='link'),'bundle_ids',(SELECT l.bundle_ids FROM link_apps l WHERE l.package_id=p.id AND p.kind='link'),'upstream_disabled',p.disabled,'disabled',` + PackageDisabledSQL + `,'meta',COALESCE(to_jsonb(m),'{}'::jsonb),'i18n',COALESCE(to_jsonb(i),jsonb_build_object('source_locale',p.source_locale,'locale',p.source_locale)),'icon_url',a.url,'source_path',COALESCE(p.raw->>'ruby_source_path',''),
 'download_sha256',CASE WHEN COALESCE(p.raw->>'sha256',p.raw#>>'{urls,stable,checksum}') ~ '^[a-fA-F0-9]{64}$' THEN lower(COALESCE(p.raw->>'sha256',p.raw#>>'{urls,stable,checksum}')) ELSE NULL END,
 'categories',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',c.id,'source_locale',c.source_locale,'machine_translated',COALESCE(ci.machine_translated,false),'slug',c.slug,'name',COALESCE(ci.name,c.slug),'icon',c.icon,'is_primary',pc.is_primary,'confidence',pc.confidence) ORDER BY pc.is_primary DESC,c.sort,c.id)
 FROM package_categories pc JOIN categories c ON c.id=pc.category_id LEFT JOIN LATERAL (SELECT t.* FROM category_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE NULLIF(t.name,'') IS NOT NULL AND t.category_id=c.id AND t.locale IN (lang.requested,c.source_locale,'en-US') ORDER BY ` + publicLocaleOrder + ` LIMIT 1) ci ON TRUE
 WHERE pc.package_id=p.id AND c.visible),'[]'::jsonb)) AS data` + publicJoins + `
 LEFT JOIN LATERAL (SELECT t.* FROM package_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE COALESCE(NULLIF(t.display_name,''),NULLIF(t.summary,''),NULLIF(t.description,'')) IS NOT NULL AND t.package_id=p.id AND t.locale IN (lang.requested,p.source_locale,'en-US') ORDER BY ` + publicLocaleOrder + ` LIMIT 1) i ON TRUE LEFT JOIN assets a ON a.id=m.icon_asset_id`

func (s *Store) publicRows(ctx context.Context, locale, condition, order string, args ...any) ([]PublicPackage, error) {
	var rows []struct{ Data json.RawMessage }
	params := append([]any{locale, locale}, args...)
	if err := s.DB.WithContext(ctx).Raw(linkSQL(ctx, packageView+" WHERE p.kind='cask' AND "+condition+" "+order), params...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query public packages: %w", err)
	}
	out := make([]PublicPackage, 0, len(rows))
	for _, row := range rows {
		var p PublicPackage
		if err := json.Unmarshal(row.Data, &p); err != nil {
			return nil, fmt.Errorf("decode package view: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// publicPage selects the ordered page of IDs before building the JSON view: packageView
// references the whole row, which would otherwise de-TOAST every candidate row.
func (s *Store) publicPage(ctx context.Context, locale, condition, order string, args ...any) ([]PublicPackage, error) {
	var ids []int64
	if err := s.DB.WithContext(ctx).Raw(linkSQL(ctx, "SELECT p.id"+publicJoins+" WHERE p.kind='cask' AND "+condition+" "+order), args...).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("query public package page: %w", err)
	}
	return s.PublicPackagesByID(ctx, ids, locale)
}
func publicCondition(f PublicFilter) (string, []any) {
	condition := publicVisible
	args := []any{}
	if !f.IncludeDisabled {
		condition += " AND NOT (p.disabled OR p.stale_disabled)"
	}
	if !f.IncludeFonts {
		condition += " AND NOT p.is_font"
	}
	if !f.IncludeLibraries {
		condition += " AND NOT p.is_library"
	}
	if f.Kind != "" {
		condition += " AND p.kind=?"
		args = append(args, f.Kind)
	}
	if f.Category != "" {
		condition += ` AND EXISTS(SELECT 1 FROM package_categories pc WHERE pc.package_id=p.id AND pc.category_id IN (WITH RECURSIVE subtree AS (SELECT id FROM categories WHERE slug=? UNION ALL SELECT c.id FROM categories c JOIN subtree t ON c.parent_id=t.id) SELECT id FROM subtree))`
		args = append(args, f.Category)
	}
	return condition, args
}
func (s *Store) PublicPackages(ctx context.Context, f PublicFilter) ([]PublicPackage, int, error) {
	condition, args := publicCondition(f)
	condition = linkSQL(ctx, condition)
	var total int
	if err := s.DB.WithContext(ctx).Raw("SELECT count(*)"+publicJoins+" WHERE "+condition, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "p.popularity DESC,p.id"
	switch f.Sort {
	case "updated":
		order = "p.version_changed_at DESC,p.id"
	case "name":
		order = "lower(p.name),p.id"
	}
	if f.Period != "" {
		column := map[string]string{"30d": "installs_30d", "90d": "installs_90d", "365d": "installs_365d"}[f.Period]
		if column == "" {
			return nil, 0, errors.New("invalid ranking period")
		}
		order = "p." + column + " DESC,p.id"
	}
	offset, found := domain.PageOffset(f.Current, f.Size, int64(total))
	if !found {
		return []PublicPackage{}, total, nil
	}
	args = append(args, f.Size, offset)
	rows, err := s.publicPage(ctx, f.Locale, condition, "ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	return rows, total, err
}
func (s *Store) PublicPackage(ctx context.Context, kind, token, locale string) (PublicPackage, error) {
	if kind == "link" {
		ctx = domain.WithLinks(ctx, true)
	}
	rows, err := s.publicRows(ctx, locale, "p.kind=? AND p.token=? AND p.removed_at IS NULL", "", kind, token)
	if err != nil {
		return PublicPackage{}, err
	}
	if len(rows) == 0 {
		return PublicPackage{}, gorm.ErrRecordNotFound
	}
	return rows[0], nil
}
func (s *Store) PublicPackagesByID(ctx context.Context, ids []int64, locale string) ([]PublicPackage, error) {
	if len(ids) == 0 {
		return []PublicPackage{}, nil
	}
	rows, err := s.publicRows(ctx, locale, "p.id IN ?", "", ids)
	if err != nil {
		return nil, err
	}
	// IN returns rows in arbitrary order; keep the caller's order.
	position := make(map[int64]int, len(ids))
	for index, id := range ids {
		if _, seen := position[id]; !seen {
			position[id] = index
		}
	}
	sort.SliceStable(rows, func(a, b int) bool { return position[rows[a].ID] < position[rows[b].ID] })
	return rows, nil
}
func (s *Store) PublicCategories(ctx context.Context, locale string) ([]PublicCategory, error) {
	var rows []PublicCategory
	// Aggregate by ancestor once, avoiding costly JIT compilation triggered by overestimated per-category correlated queries.
	err := s.DB.WithContext(ctx).Raw(linkSQL(ctx, `WITH RECURSIVE descendants AS(SELECT id ancestor,id FROM categories UNION ALL SELECT d.ancestor,c.id FROM descendants d JOIN categories c ON c.parent_id=d.id),
	 counts AS(SELECT d.ancestor,count(DISTINCT pc.package_id) package_count FROM descendants d JOIN package_categories pc ON pc.category_id=d.id JOIN packages p ON p.id=pc.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE `+publicVisible+` AND NOT (p.disabled OR p.stale_disabled) GROUP BY d.ancestor)
	 SELECT c.id,c.source_locale,COALESCE(i.machine_translated,false) machine_translated,c.parent_id,c.slug,c.icon,c.applies_to,c.hidden_by_default,COALESCE(i.name,c.slug) name,i.description,COALESCE(counts.package_count,0) package_count
	 FROM categories c LEFT JOIN LATERAL (SELECT t.* FROM category_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE NULLIF(t.name,'') IS NOT NULL AND t.category_id=c.id AND t.locale IN (lang.requested,c.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1) i ON TRUE LEFT JOIN counts ON counts.ancestor=c.id WHERE `+publicCategoryVisible+` ORDER BY c.sort,c.id`), locale).Scan(&rows).Error
	return rows, err
}
func (s *Store) PublicStats(ctx context.Context) (map[string]int, error) {
	var v struct{ Casks, Formulae, Updated int }
	err := s.DB.WithContext(ctx).Raw(`SELECT count(*) FILTER(WHERE p.kind='cask') casks,count(*) FILTER(WHERE p.kind='formula') formulae,count(*) FILTER(WHERE p.version_changed_at>now()-interval '24 hours') updated` + publicJoins + ` WHERE ` + publicVisible + ` AND NOT (p.disabled OR p.stale_disabled)`).Scan(&v).Error
	return map[string]int{"casks": v.Casks, "formulae": v.Formulae, "updatedLast24h": v.Updated}, err
}
func (s *Store) PublicRelated(ctx context.Context, p PublicPackage, locale string, limit int) ([]PublicPackage, error) {
	return s.publicPage(ctx, locale, publicVisible+` AND NOT (p.disabled OR p.stale_disabled) AND NOT p.is_font AND NOT p.is_library AND p.kind=? AND p.id<>? AND EXISTS(SELECT 1 FROM package_categories pc JOIN package_categories target ON target.category_id=pc.category_id WHERE pc.package_id=p.id AND target.package_id=? AND target.is_primary)`, "ORDER BY abs(p.popularity-?),p.popularity DESC,p.id LIMIT ?", p.Kind, p.ID, p.ID, p.Popularity, limit)
}
func (s *Store) PublicDependents(ctx context.Context, p PublicPackage, locale string) ([]PublicPackage, int, error) {
	tokens, err := json.Marshal([]string{p.Token})
	if err != nil {
		return nil, 0, err
	}
	// Casks are required only through dependsOn.cask; formulae through runtime or dependsOn.formula.
	// Each path has an expression index (migration 00015).
	condition := `p.dependencies->'dependsOn'->'cask' @> ?::jsonb`
	args := []any{string(tokens)}
	if p.Kind != "cask" {
		condition = `(p.dependencies->'runtime' @> ?::jsonb OR p.dependencies->'dependsOn'->'formula' @> ?::jsonb)`
		args = append(args, string(tokens))
	}
	var rows []struct {
		ID    int64
		Total int
	}
	// MATERIALIZED keeps the planner on the indexed filter; with ORDER BY ... LIMIT it would
	// otherwise walk the popularity index and test every package.
	if err := s.DB.WithContext(ctx).Raw("WITH d AS MATERIALIZED (SELECT p.id,p.popularity"+publicJoins+" WHERE "+publicVisible+" AND NOT (p.disabled OR p.stale_disabled) AND "+condition+") SELECT id,(SELECT count(*) FROM d) AS total FROM d ORDER BY popularity DESC,id LIMIT 6", args...).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("query package dependents: %w", err)
	}
	if len(rows) == 0 {
		return []PublicPackage{}, 0, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	out, err := s.PublicPackagesByID(ctx, ids, locale)
	return out, rows[0].Total, err
}
func (s *Store) PublicScreenshots(ctx context.Context, id int64, locale string) (json.RawMessage, error) {
	var row struct{ Data json.RawMessage }
	err := s.DB.WithContext(ctx).Raw(`SELECT COALESCE(jsonb_agg(jsonb_build_object('url',a.url,'thumbUrl',regexp_replace(a.url,'-1280[.]png$','-640.png'),'width',a.width,'height',a.height,'theme',s.theme,'caption',i.caption,'sourceLocale',s.source_locale,'machineTranslated',COALESCE(i.machine_translated,false)) ORDER BY s.sort,s.id),'[]'::jsonb) data FROM package_screenshots s JOIN assets a ON a.id=s.asset_id LEFT JOIN LATERAL (SELECT t.* FROM screenshot_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE t.caption IS NOT NULL AND t.screenshot_id=s.id AND t.locale IN (lang.requested,s.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1) i ON TRUE WHERE s.package_id=?`, locale, id).Scan(&row).Error
	return row.Data, err
}
func (s *Store) PublicConfig(ctx context.Context) (map[string]json.RawMessage, error) {
	var rows []struct {
		Key   string
		Value json.RawMessage
	}
	if err := s.DB.WithContext(ctx).Table("app_config").Select("key, COALESCE(value, 'null'::jsonb) AS value").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}
func (s *Store) PublicMirrors(ctx context.Context, locale string) (json.RawMessage, error) {
	var row struct{ Data json.RawMessage }
	err := s.DB.WithContext(ctx).Raw(`SELECT COALESCE(jsonb_agg(jsonb_build_object('key',m.key,'name',i.name,'sourceLocale',m.source_locale,'machineTranslated',COALESCE(i.machine_translated,false),'apiDomain',api_domain,'bottleDomain',bottle_domain,'brewGitRemote',brew_git_remote,'coreGitRemote',core_git_remote,'probeUrl',probe_url,'recommended',recommended) ORDER BY m.sort,m.id),'[]'::jsonb) data FROM mirrors m LEFT JOIN LATERAL (SELECT t.* FROM mirror_i18n t CROSS JOIN (SELECT ?::text requested) lang WHERE NULLIF(t.name,'') IS NOT NULL AND t.mirror_id=m.id AND t.locale IN (lang.requested,m.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1) i ON TRUE WHERE enabled`, locale).Scan(&row).Error
	return row.Data, err
}
func (s *Store) PublicLatestVersion(ctx context.Context) (string, error) {
	var v string
	err := s.DB.WithContext(ctx).Raw("SELECT version FROM desktop_releases WHERE channel='stable' AND status='published' ORDER BY pub_date DESC,id DESC LIMIT 1").Scan(&v).Error
	return v, err
}

type FeedbackInput struct {
	Type, Content, Platform                   string
	PackageID                                 *int64
	Contact, AppVersion, OSVersion, ErrorCode *string
}

func (s *Store) SaveFeedback(ctx context.Context, in FeedbackInput) (int64, error) {
	var id int64
	err := s.DB.WithContext(ctx).Raw(`INSERT INTO feedback(type,package_id,content,contact,platform,app_version,os_version,error_code) VALUES(?,?,?,?,?,?,?,?) RETURNING id`, in.Type, in.PackageID, strings.TrimSpace(in.Content), in.Contact, in.Platform, in.AppVersion, in.OSVersion, in.ErrorCode).Scan(&id).Error
	return id, err
}
