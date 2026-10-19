package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/about"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

type PublicService struct {
	Now        func() time.Time
	Store      *store.Store
	Cache      cache.Backend
	WebBaseURL string
	// Secrets decrypts credentials read by public endpoints (IndexNow key file).
	Secrets config.Config
}
type SummaryPage struct {
	Current int                        `json:"current"`
	Size    int                        `json:"size"`
	Total   int                        `json:"total"`
	Records []publicapi.PackageSummary `json:"records"`
}
type RankingPage struct {
	Current int                      `json:"current"`
	Size    int                      `json:"size"`
	Total   int                      `json:"total"`
	Records []publicapi.RankingEntry `json:"records"`
}
type SitemapPage struct {
	Current int                      `json:"current"`
	Size    int                      `json:"size"`
	Total   int                      `json:"total"`
	Records []publicapi.SitemapEntry `json:"records"`
}

func ValidPage(current, size, maximum int) bool {
	return current > 0 && size > 0 && size <= maximum && current <= math.MaxInt/size
}
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	return err
}
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func nonnil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
func localePointer(value string) *publicapi.Locale {
	if !i18n.Valid(value) {
		value = "en-US"
	}
	result := publicapi.Locale(value)
	return &result
}
func boolPointer(value bool) *bool { return &value }
func summary(p store.PublicPackage) publicapi.PackageSummary {
	name := p.Name
	if p.I18n.DisplayName != nil && *p.I18n.DisplayName != "" {
		name = *p.I18n.DisplayName
	}
	text := p.I18n.Summary
	if text == nil || *text == "" {
		text = p.DescEN
	}
	source := publicapi.Locale(p.I18n.SourceLocale)
	machine := p.I18n.MachineTranslated
	s := publicapi.PackageSummary{Link: p.Link, BundleIds: p.BundleIDs, SourceLocale: &source, MachineTranslated: &machine, Kind: publicapi.PackageKind(p.Kind), Token: p.Token, Name: p.Name, DisplayName: name, Summary: text, IconUrl: p.IconURL, AccentColor: p.Meta.AccentColor, Version: p.Version, Installs30d: p.Installs30d, Rank30d: p.Rank30d, AutoUpdates: p.AutoUpdates, Deprecated: p.Deprecated, Disabled: p.Disabled, IsFont: p.IsFont, IsLibrary: p.IsLibrary, EditorChoice: p.Meta.EditorChoice, VersionChangedAt: p.VersionChangedAt.UTC()}
	for _, category := range p.Categories {
		if category.IsPrimary {
			s.PrimaryCategory = &publicapi.CategoryRef{SourceLocale: localePointer(category.SourceLocale), MachineTranslated: boolPointer(category.MachineTranslated), Slug: category.Slug, Name: category.Name, Icon: category.Icon}
			break
		}
	}
	return s
}
func Summaries(rows []store.PublicPackage) []publicapi.PackageSummary {
	out := make([]publicapi.PackageSummary, 0, len(rows))
	for _, p := range rows {
		out = append(out, summary(p))
	}
	return out
}
func (s *PublicService) SummariesByID(ctx context.Context, ids []int64, locale string) (map[int64]publicapi.PackageSummary, error) {
	rows, err := s.Store.PublicPackagesByID(ctx, ids, locale)
	out := map[int64]publicapi.PackageSummary{}
	for _, row := range rows {
		out[row.ID] = summary(row)
	}
	return out, err
}
func (s *PublicService) List(ctx context.Context, f store.PublicFilter) (SummaryPage, error) {
	if f.Kind == "formula" {
		return SummaryPage{}, notFound(gorm.ErrRecordNotFound)
	}
	if !ValidPage(f.Current, f.Size, 100) {
		return SummaryPage{}, domain.Validation()
	}
	rows, total, err := s.Store.PublicPackages(ctx, f)
	return SummaryPage{f.Current, f.Size, total, Summaries(rows)}, err
}
func (s *PublicService) Categories(ctx context.Context, locale string) ([]publicapi.CategoryNode, error) {
	return cache.ReadThrough(ctx, s.Cache, "c:cat:tree:c2:"+locale+domain.LinksCacheKey(ctx), 30*time.Minute, func(ctx context.Context) ([]publicapi.CategoryNode, error) {
		rows, err := s.Store.PublicCategories(ctx, locale)
		if err != nil {
			return nil, err
		}
		var tree func(*int64, map[int64]bool) []publicapi.CategoryNode
		tree = func(parent *int64, seen map[int64]bool) []publicapi.CategoryNode {
			out := []publicapi.CategoryNode{}
			for _, r := range rows {
				if (parent == nil && r.ParentID != nil) || (parent != nil && (r.ParentID == nil || *r.ParentID != *parent)) || seen[r.ID] {
					continue
				}
				seen[r.ID] = true
				out = append(out, publicapi.CategoryNode{SourceLocale: localePointer(r.SourceLocale), MachineTranslated: boolPointer(r.MachineTranslated), Slug: r.Slug, Name: r.Name, Description: r.Description, Icon: r.Icon, AppliesTo: publicapi.CategoryNodeAppliesTo(r.AppliesTo), HiddenByDefault: r.HiddenByDefault, PackageCount: r.PackageCount, Children: tree(&r.ID, seen)})
			}
			return out
		}
		return tree(nil, map[int64]bool{}), nil
	})
}
func (s *PublicService) Home(ctx context.Context, locale string) (publicapi.HomeData, error) {
	data, err := cache.ReadThrough(ctx, s.Cache, "c:home:c2:"+locale+domain.LinksCacheKey(ctx), 5*time.Minute, func(ctx context.Context) (publicapi.HomeData, error) {
		data := publicapi.HomeData{PopularCli: []publicapi.PackageSummary{}, Features: []publicapi.Feature{}, Collections: []publicapi.CollectionSummary{}}
		var err error
		data.Categories, err = s.Categories(ctx, locale)
		if err != nil {
			return data, err
		}
		for _, section := range []struct {
			kind, sort string
			target     *[]publicapi.PackageSummary
		}{{"", "popular", &data.PopularApps}, {"", "updated", &data.RecentlyUpdated}} {
			page, err := s.List(ctx, store.PublicFilter{Kind: section.kind, Sort: section.sort, Locale: locale, Current: 1, Size: 12, IncludeFonts: true})
			if err != nil {
				return data, err
			}
			*section.target = page.Records
		}
		stats, err := s.Store.PublicStats(ctx)
		data.Stats.Casks = stats["casks"]
		data.Stats.Formulae = stats["formulae"]
		data.Stats.UpdatedLast24h = stats["updatedLast24h"]
		return data, err
	})
	if err != nil {
		return data, err
	}
	data.Features, err = s.Features(ctx, locale)
	if err != nil {
		return data, err
	}
	collections, err := s.Collections(ctx, locale, 1, 6)
	data.Collections = collections.Records
	return data, err

}
func (s *PublicService) Detail(ctx context.Context, kind, token, locale string) (publicapi.PackageDetail, error) {
	return cache.ReadThrough(ctx, s.Cache, cache.PackageDetailPrefix+kind+":"+token+":"+locale+domain.LinksCacheKey(ctx), 10*time.Minute, func(ctx context.Context) (publicapi.PackageDetail, error) {
		p, err := s.Store.PublicPackage(ctx, kind, token, locale)
		if err != nil {
			return publicapi.PackageDetail{}, notFound(err)
		}
		base, err := json.Marshal(summary(p))
		if err != nil {
			return publicapi.PackageDetail{}, err
		}
		var d publicapi.PackageDetail
		if err = json.Unmarshal(base, &d); err != nil {
			return d, err
		}
		d.Names = nonnil(p.Names)
		d.Tap = p.Tap
		d.DescriptionEn = p.DescEN
		d.Description = p.I18n.Description
		d.Homepage = p.Homepage
		d.Developer = p.Meta.Developer
		d.RepoUrl = p.Meta.RepoURL
		d.License = p.License
		d.Caveats = p.Caveats
		d.DownloadUrl = p.DownloadURL
		d.DownloadSize = p.DownloadSize
		d.DownloadSha256 = p.DownloadSHA256
		d.MinMacos = p.MinMacos
		d.KegOnly = p.KegOnly
		d.Tags = nonnil(p.Meta.Tags)
		d.Categories = []publicapi.CategoryRef{}
		d.Screenshots = []publicapi.Screenshot{}
		d.InstallCommand = "brew install --" + kind + " " + token
		if !homebrew.IsOfficialTap(p.Tap) {
			// brew taps the repository on first install when given the qualified name.
			d.InstallCommand = "brew install --" + kind + " " + p.Tap + "/" + token
		}
		d.Installs = publicapi.InstallStats{D30: p.Installs30d, D90: p.Installs90d, D365: p.Installs365d}
		if kind == "formula" {
			d.OnRequest = &publicapi.InstallStats{D30: p.OnRequest30d, D90: p.OnRequest90d, D365: p.OnRequest365d}
		}
		d.Supports.Arm64 = p.SupportsArm64
		d.Supports.X8664 = p.SupportsX8664
		metadata, platforms, err := platformData(p)
		if err != nil {
			return d, err
		}
		status := publicapi.PackageDetailSupportsStatus(metadata.SupportStatus)
		d.Supports.Status = &status
		d.Supports.RequiresRosetta = &metadata.RequiresRosetta
		d.Platforms = &platforms
		for _, c := range p.Categories {
			d.Categories = append(d.Categories, publicapi.CategoryRef{SourceLocale: localePointer(c.SourceLocale), MachineTranslated: boolPointer(c.MachineTranslated), Slug: c.Slug, Name: c.Name, Icon: c.Icon})
		}
		if err := json.Unmarshal(p.Artifacts, &d.Artifacts); err != nil {
			return d, err
		}
		d.Artifacts.Apps = nonnil(d.Artifacts.Apps)
		d.Artifacts.Binaries = nonnil(d.Artifacts.Binaries)
		d.Artifacts.Pkgs = nonnil(d.Artifacts.Pkgs)
		d.ConflictsWith = conflicts(p.ConflictsWith)
		d.Dependencies, d.DependsOn = dependencyData(p)
		d.Deprecation = lifecycle(p.Deprecated, p.DeprecationDate, p.DeprecationReason, p.DeprecationReplacement)
		d.Disable = packageDisable(p, locale)
		d.HomebrewStatus = homebrewStatus(p)
		d.SummaryTranslation.Status = publicapi.TranslationInfoStatusNone
		if p.I18n.Summary != nil && *p.I18n.Summary != "" {
			d.SummaryTranslation.Status = publicapi.TranslationInfoStatus(p.I18n.Status)
			l := publicapi.Locale(p.I18n.Locale)
			d.SummaryTranslation.Locale = &l
			d.SummaryTranslation.SourceLocale = d.SourceLocale
			d.SummaryTranslation.MachineTranslated = d.MachineTranslated
		}
		path := p.SourcePath
		if path == "" {
			folder := "Formula"
			if kind == "cask" {
				folder = "Casks"
			}
			path = folder + "/" + token[:1] + "/" + token + ".rb"
		}
		repo := strings.Replace(p.Tap, "/", "/homebrew-", 1)
		d.SourceUrl = "https://github.com/" + repo + "/blob/HEAD/" + path
		d.FormulaeUrl = "https://formulae.brew.sh/" + kind + "/" + token
		if kind == "link" {
			d.InstallCommand = ""
			d.SourceUrl = ""
			d.FormulaeUrl = ""
			if p.DownloadURL != nil {
				d.SourceUrl = *p.DownloadURL
			}
			if p.Homepage != nil {
				d.SourceUrl = *p.Homepage
			}
			if p.Link != nil && p.Link.Github != nil {
				d.SourceUrl = "https://github.com/" + p.Link.Github.Repo
			}
			d.FormulaeUrl = d.SourceUrl
		}
		if kind == "cask" || kind == "link" {
			if d.Indexing, d.VersionCommittedAt, err = s.indexing(ctx, token); err != nil {
				return d, err
			}
			if err := s.appFacts(ctx, &d, p.ID, locale); err != nil {
				return d, err
			}
			if err := s.sourceFacts(ctx, &d, p.ID); err != nil {
				return d, err
			}
			related, err := s.packageArticles(ctx, p.ID, locale)
			if err != nil {
				return d, err
			}
			d.Articles = &related
		}
		shots, err := s.Store.PublicScreenshots(ctx, p.ID, locale)
		if err != nil {
			return d, err
		}
		if err = json.Unmarshal(shots, &d.Screenshots); err != nil {
			return d, err
		}
		releases, err := s.ReleasePage(ctx, kind, token, locale, 1, 1, false)
		if err != nil {
			return d, err
		}
		d.ReleaseCount = releases.Total
		d.ReleaseStats = &releases.Stats
		if len(releases.Records) > 0 {
			entry := releases.Records[0]
			d.LatestRelease = &entry
		}
		return d, nil
	})
}
func packageDisable(p store.PublicPackage, locale string) *publicapi.LifecycleNotice {
	if p.StaleDisabled && !p.UpstreamDisabled {
		reason := "No version update for more than 90 days"
		switch locale {
		case "zh-CN":
			reason = "超过 90 天没有版本更新"
		case "ja-JP":
			reason = "90 日以上バージョン更新がありません"
		case "es-ES":
			reason = "Sin actualizaciones de versión durante más de 90 días"
		case "pt-BR":
			reason = "Sem atualização de versão há mais de 90 dias"
		case "ru-RU":
			reason = "Версия не обновлялась более 90 дней"
		}
		return &publicapi.LifecycleNotice{Reason: &reason}
	}
	return lifecycle(p.Disabled, p.DisableDate, p.DisableReason, p.DisableReplacement)
}

func lifecycle(active bool, date, reason, replacement *string) *publicapi.LifecycleNotice {
	if !active {
		return nil
	}
	out := &publicapi.LifecycleNotice{Reason: reason}
	if date != nil {
		if value, err := time.Parse("2006-01-02", *date); err == nil {
			out.Date = &openapi_types.Date{Time: value}
		}
	}
	if replacement != nil {
		kind, token, ok := strings.Cut(*replacement, ":")
		if ok {
			out.Replacement = &publicapi.Replacement{Kind: publicapi.PackageKind(kind), Token: token}
		}
	}
	return out
}
func conflicts(raw json.RawMessage) publicapi.ConflictsWith {
	out := publicapi.ConflictsWith{Casks: []string{}, Formulae: []string{}}
	_ = json.Unmarshal(raw, &out)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if len(out.Casks) == 0 {
		out.Casks = stringList(fields["cask"])
	}
	if len(out.Formulae) == 0 {
		out.Formulae = stringList(fields["formula"])
	}
	out.Casks = nonnil(out.Casks)
	out.Formulae = nonnil(out.Formulae)
	return out
}
func stringList(raw json.RawMessage) []string {
	out := []string{}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return out
	}
	for _, item := range values {
		var value string
		if json.Unmarshal(item, &value) == nil {
			out = append(out, value)
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(item, &object) == nil {
			for key := range object {
				out = append(out, key)
			}
		}
	}
	return out
}
func dependencyData(p store.PublicPackage) (*publicapi.FormulaDependencies, *publicapi.CaskDependsOn) {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(p.Dependencies, &raw)
	if p.Kind == "formula" {
		return &publicapi.FormulaDependencies{Runtime: stringList(raw["runtime"]), Build: stringList(raw["build"]), Test: stringList(raw["test"]), Optional: stringList(raw["optional"]), Recommended: stringList(raw["recommended"]), UsesFromMacos: stringList(raw["usesFromMacos"])}, nil
	}
	var on map[string]json.RawMessage
	_ = json.Unmarshal(raw["dependsOn"], &on)
	c := &publicapi.CaskDependsOn{Arch: []publicapi.CaskDependsOnArch{}, Formulae: stringList(on["formula"]), Casks: stringList(on["cask"])}
	if p.SupportsArm64 {
		c.Arch = append(c.Arch, publicapi.CaskDependsOnArchArm64)
	}
	if p.SupportsX8664 {
		c.Arch = append(c.Arch, publicapi.CaskDependsOnArchX8664)
	}
	arch := []string{}
	for _, value := range c.Arch {
		arch = append(arch, string(value))
	}
	requirements := homebrew.Requirements(on, arch)
	c.Macos = requirements.Macos
	var complete map[string]interface{}
	if json.Unmarshal(raw["dependsOn"], &complete) == nil && complete != nil {
		c.Requirements = &complete
	}
	return nil, c
}

func platformData(p store.PublicPackage) (homebrew.CaskPlatformMetadata, []publicapi.CaskPlatform, error) {
	var metadata homebrew.CaskPlatformMetadata
	if err := json.Unmarshal(p.Dependencies, &metadata); err != nil {
		return metadata, nil, fmt.Errorf("decode cask platform metadata: %w", err)
	}
	if metadata.SupportStatus == "" {
		metadata.SupportStatus = "unknown"
	}
	platforms := []publicapi.CaskPlatform{}
	data, err := json.Marshal(metadata.Platforms)
	if err != nil {
		return metadata, nil, fmt.Errorf("encode cask platforms: %w", err)
	}
	if err := json.Unmarshal(data, &platforms); err != nil {
		return metadata, nil, fmt.Errorf("decode cask platforms: %w", err)
	}
	platforms = nonnil(platforms)
	return metadata, platforms, nil
}
func (s *PublicService) Related(ctx context.Context, kind, token, locale string, limit int) ([]publicapi.PackageSummary, error) {
	key := fmt.Sprintf("c:related:c1:%s:%s:%s:%d", kind, token, locale, limit)
	return cache.ReadThrough(ctx, s.Cache, key, 10*time.Minute, func(ctx context.Context) ([]publicapi.PackageSummary, error) {
		p, err := s.Store.PublicPackage(ctx, kind, token, locale)
		if err != nil {
			return nil, notFound(err)
		}
		rows, err := s.Store.PublicRelated(ctx, p, locale, limit)
		return Summaries(rows), err
	})
}
func packageForPlatform(p store.PublicPackage, tag string) (store.PublicPackage, bool, error) {
	var metadata homebrew.CaskPlatformMetadata
	if err := json.Unmarshal(p.Dependencies, &metadata); err != nil {
		return p, false, fmt.Errorf("decode dependency platforms: %w", err)
	}
	for _, platform := range metadata.Platforms {
		if platform.Tag != tag {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(p.Dependencies, &fields); err != nil {
			return p, false, fmt.Errorf("decode package dependencies: %w", err)
		}
		data, err := json.Marshal(platform.DependsOn.Requirements)
		if err != nil {
			return p, false, fmt.Errorf("encode platform requirements: %w", err)
		}
		fields["dependsOn"] = data
		p.Dependencies, err = json.Marshal(fields)
		if err != nil {
			return p, false, fmt.Errorf("encode platform dependencies: %w", err)
		}
		p.Version = platform.Version
		p.SupportsArm64 = platform.Arch == "arm64"
		p.SupportsX8664 = platform.Arch == "x86_64"
		p.ConflictsWith = platform.ConflictsWith
		return p, true, nil
	}
	return p, false, nil
}

func (s *PublicService) Dependencies(ctx context.Context, kind, token, locale string, depth int, platformTag string) (publicapi.Dependencies, error) {
	key := fmt.Sprintf("c:dep:c1:%s:%s:%s:%d:%s", kind, token, locale, depth, platformTag)
	return cache.ReadThrough(ctx, s.Cache, key, 10*time.Minute, func(ctx context.Context) (publicapi.Dependencies, error) {
		return s.dependencies(ctx, kind, token, locale, depth, platformTag)
	})
}
func (s *PublicService) dependencies(ctx context.Context, kind, token, locale string, depth int, platformTag string) (publicapi.Dependencies, error) {
	p, err := s.Store.PublicPackage(ctx, kind, token, locale)
	if err != nil {
		return publicapi.Dependencies{}, notFound(err)
	}
	if platformTag != "" {
		var supported bool
		p, supported, err = packageForPlatform(p, platformTag)
		if err != nil {
			return publicapi.Dependencies{}, err
		}
		if !supported {
			return publicapi.Dependencies{}, domain.Validation()
		}
	}
	out := publicapi.Dependencies{ConflictsWith: conflicts(p.ConflictsWith)}
	_, platforms, err := platformData(p)
	if err != nil {
		return out, err
	}
	out.Platforms = &platforms
	_, out.DependsOn = dependencyData(p)
	var build func(context.Context, store.PublicPackage, int, map[string]bool) ([]publicapi.DependencyNode, error)
	build = func(ctx context.Context, parent store.PublicPackage, remaining int, seen map[string]bool) ([]publicapi.DependencyNode, error) {
		nodes := []publicapi.DependencyNode{}
		if remaining == 0 {
			return nodes, nil
		}
		formula, cask := dependencyData(parent)
		groups := []struct {
			kind, typ string
			tokens    []string
		}{}
		if formula != nil {
			for _, g := range []struct {
				typ    string
				tokens []string
			}{{"runtime", formula.Runtime}, {"build", formula.Build}, {"test", formula.Test}, {"optional", formula.Optional}, {"recommended", formula.Recommended}, {"usesFromMacos", formula.UsesFromMacos}} {
				groups = append(groups, struct {
					kind, typ string
					tokens    []string
				}{"formula", g.typ, g.tokens})
			}
		} else {
			groups = append(groups, struct {
				kind, typ string
				tokens    []string
			}{"formula", "runtime", cask.Formulae}, struct {
				kind, typ string
				tokens    []string
			}{"cask", "cask", cask.Casks})
		}
		for _, g := range groups {
			for _, token := range g.tokens {
				node := publicapi.DependencyNode{Kind: publicapi.PackageKind(g.kind), Token: token, Name: token, Type: publicapi.DependencyNodeType(g.typ), Children: []publicapi.DependencyNode{}}
				child, err := s.Store.PublicPackage(ctx, g.kind, token, locale)
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, err
				}
				if err == nil {
					available := true
					if platformTag != "" && child.Kind == "cask" {
						child, available, err = packageForPlatform(child, platformTag)
						if err != nil {
							return nil, err
						}
					}
					sum := summary(child)
					node.Name = sum.DisplayName
					if available {
						node.Version = &child.Version
					}
					node.Summary = sum.Summary
					node.SourceLocale = sum.SourceLocale
					node.MachineTranslated = sum.MachineTranslated
					key := g.kind + ":" + token
					// If a child dependency lacks the same-platform variant, retain only its declaration; never substitute another platform's version or subtree.
					if available && !seen[key] {
						path := map[string]bool{}
						for k, v := range seen {
							path[k] = v
						}
						path[key] = true
						node.Children, err = build(ctx, child, remaining-1, path)
						if err != nil {
							return nil, err
						}
					}
				}
				nodes = append(nodes, node)
				if len(nodes) >= 100 {
					return nodes, nil
				}
			}
		}
		return nodes, nil
	}
	out.Dependencies, err = build(ctx, p, depth, map[string]bool{kind + ":" + token: true})
	if err != nil {
		return out, err
	}
	rows, total, err := s.Store.PublicDependents(ctx, p, locale)
	out.Dependents.Count = total
	out.Dependents.Top = Summaries(rows)
	return out, err
}
func (s *PublicService) Rankings(ctx context.Context, f store.PublicFilter) (RankingPage, error) {
	if f.Kind == "formula" {
		return RankingPage{}, notFound(gorm.ErrRecordNotFound)
	}
	if !ValidPage(f.Current, f.Size, 100) {
		return RankingPage{}, domain.Validation()
	}
	key := fmt.Sprintf("c:rank:c2:%s:%s:%s:%d:%d:%s:%t", f.Kind, f.Period, f.Category, f.Current, f.Size, f.Locale, f.IncludeFonts)
	return cache.ReadThrough(ctx, s.Cache, key, 30*time.Minute, func(ctx context.Context) (RankingPage, error) {
		f.IncludeLibraries = true
		rows, total, err := s.Store.PublicPackages(ctx, f)
		out := RankingPage{f.Current, f.Size, total, []publicapi.RankingEntry{}}
		for index, p := range rows {
			installs := p.Installs30d
			switch f.Period {
			case "90d":
				installs = p.Installs90d
			case "365d":
				installs = p.Installs365d
			}
			out.Records = append(out.Records, publicapi.RankingEntry{Rank: (f.Current-1)*f.Size + index + 1, Installs: installs, Package: summary(p)})
		}
		return out, err
	})
}
func (s *PublicService) Lookup(ctx context.Context, items []publicapi.PackageRef, locale string) ([]publicapi.LookupItem, error) {
	out := make([]publicapi.LookupItem, 0, len(items))
	for _, item := range items {
		if item.Kind == publicapi.PackageKindFormula {
			return nil, notFound(gorm.ErrRecordNotFound)
		}
		p, err := s.Store.PublicPackage(ctx, string(item.Kind), item.Token, locale)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		row := publicapi.LookupItem{Kind: item.Kind, Token: item.Token, Found: err == nil}
		if row.Found {
			sum := summary(p)
			row.SourceLocale = sum.SourceLocale
			row.MachineTranslated = sum.MachineTranslated
			row.Version = &p.Version
			row.VersionBase = &p.VersionBase
			row.AutoUpdates = &p.AutoUpdates
			row.Deprecated = &p.Deprecated
			row.Disabled = &p.Disabled
			releases, err := s.ReleasePage(ctx, string(item.Kind), item.Token, locale, 1, 1, false)
			if err != nil {
				return nil, err
			}
			if len(releases.Records) > 0 {
				entry := releases.Records[0]
				row.LatestRelease = &struct {
					PublishedAt *time.Time `json:"publishedAt,omitempty"`
					Summary     *string    `json:"summary,omitempty"`
					Version     string     `json:"version"`
				}{PublishedAt: entry.PublishedAt, Summary: entry.Summary, Version: entry.Version}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// Sitemap pages indexable casks and links in id order (WP3). Under an enforced index policy only eligible
// apps and their published languages are listed; otherwise the long-standing rule applies (listed,
// still offered by Homebrew, all six languages). Apps paused only by the 90-day rule are listed in
// both cases. Records can be fewer than size: total counts candidates.
func (s *PublicService) Sitemap(ctx context.Context, group string, current, size int) (SitemapPage, error) {
	if !ValidPage(current, size, 5000) || (group != "" && group != "apps" && group != "fonts") {
		return SitemapPage{}, domain.Validation()
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return SitemapPage{}, err
	}
	rows, total, err := s.Store.SitemapIndexRows(ctx, group, current, size)
	out := SitemapPage{current, size, total, []publicapi.SitemapEntry{}}
	for _, row := range rows {
		facts := row.Facts()
		decision := seo.Decide(facts, policy)
		if !decision.Indexable || (!decision.Enforced && facts.Unavailable()) {
			continue
		}
		locales := make([]publicapi.SitemapLocale, 0, len(decision.Locales))
		for _, locale := range decision.Locales {
			locales = append(locales, publicapi.SitemapLocale{Locale: publicapi.Locale(locale), Lastmod: row.Lastmod(locale)})
		}
		isFont, notes := row.IsFont, row.NotesCount
		out.Records = append(out.Records, publicapi.SitemapEntry{
			Kind: publicapi.PackageKind(row.Kind), Token: row.Token, UpdatedAt: row.UpdatedAt.UTC(),
			IsFont: &isFont, NotesCount: &notes, Locales: &locales,
		})
	}
	return out, err
}

// indexing is the WP11 decision for one cask's pages, plus the Homebrew commit time of its current
// version. Both are nil for unknown tokens.
func (s *PublicService) indexing(ctx context.Context, token string) (*publicapi.PackageIndexing, *time.Time, error) {
	row, found, err := s.Store.IndexRowByToken(ctx, token)
	if err != nil || !found {
		return nil, nil, err
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, nil, err
	}
	decision := seo.Decide(row.Facts(), policy)
	locales := make([]publicapi.Locale, 0, len(decision.Locales))
	for _, locale := range decision.Locales {
		locales = append(locales, publicapi.Locale(locale))
	}
	var reason *string
	if decision.Reason != "" {
		reason = &decision.Reason
	}
	return &publicapi.PackageIndexing{Indexable: decision.Indexable, Reason: reason, Locales: locales, Enforced: decision.Enforced, NotesCount: row.NotesCount}, row.CommittedAt, nil
}
func (s *PublicService) Config(ctx context.Context, platform, version, locale string) (publicapi.ClientConfig, error) {
	if platform == "desktop" && version == "" {
		return publicapi.ClientConfig{}, domain.Validation()
	}
	return cache.ReadThrough(ctx, s.Cache, "c:cfg:client:"+platform+":"+locale, 5*time.Minute, func(ctx context.Context) (publicapi.ClientConfig, error) {
		out := publicapi.ClientConfig{Mirrors: []publicapi.Mirror{}, Features: map[string]bool{}}
		values, err := s.Store.PublicConfig(ctx)
		if err != nil {
			return out, err
		}
		mirrors, err := s.Store.PublicMirrors(ctx, locale)
		if err != nil {
			return out, err
		}
		if err = json.Unmarshal(mirrors, &out.Mirrors); err != nil {
			return out, err
		}
		if raw := values[about.Key]; len(raw) > 0 {
			content, err := about.Parse(raw)
			if err != nil {
				return out, fmt.Errorf("read about config: %w", err)
			}
			out.About = content.Localized(locale)
		}
		out.MinSupportedVersion = "0.0.1"
		if raw := values["desktop.minSupportedVersion"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &out.MinSupportedVersion)
		}
		out.LatestVersion, err = s.Store.PublicLatestVersion(ctx)
		if err != nil {
			return out, err
		}
		if out.LatestVersion == "" {
			out.LatestVersion = out.MinSupportedVersion
		}
		if raw := values["desktop.features"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &out.Features)
		}
		out.Announcement = decodeAnnouncement(values["desktop.announcement"], locale)
		if out.Announcement != nil {
			raw, err := s.Store.LocalizeAnnouncement(ctx, locale)
			if err != nil {
				return out, err
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, out.Announcement); err != nil {
					return out, err
				}
			}
		}
		base := strings.TrimRight(s.WebBaseURL, "/")
		out.Links.Web = base
		out.Links.Download = base + "/download"
		out.Links.Feedback = base + "/feedback"
		out.Links.Privacy = base + "/about"
		if raw := values["web.downloadUrl"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &out.Links.Download)
		}
		return out, nil
	})
}
func (s *PublicService) Feedback(ctx context.Context, r publicapi.FeedbackCreateRequest) (int64, error) {
	if r.Website != nil && strings.TrimSpace(*r.Website) != "" {
		return 0, nil
	}
	input := store.FeedbackInput{Type: string(r.Type), Content: r.Content, Contact: r.Contact, Platform: string(r.Platform), AppVersion: r.AppVersion, OSVersion: r.OsVersion, ErrorCode: r.ErrorCode}
	if r.PackageKind != nil && r.PackageToken != nil {
		p, err := s.Store.PublicPackage(ctx, string(*r.PackageKind), *r.PackageToken, "zh-CN")
		if err == nil {
			input.PackageID = &p.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
	}
	return s.Store.SaveFeedback(ctx, input)
}
