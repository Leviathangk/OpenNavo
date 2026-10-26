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
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"golang.org/x/sync/singleflight"
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
	Lastmod *time.Time               `json:"lastmod"`
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
	s := publicapi.PackageSummary{RecommendationPaused: &p.StaleDisabled, Link: p.Link, BundleIds: p.BundleIDs, SourceLocale: &source, MachineTranslated: &machine, Kind: publicapi.PackageKind(p.Kind), Token: p.Token, Name: p.Name, DisplayName: name, Summary: text, IconUrl: p.IconURL, AccentColor: p.Meta.AccentColor, Version: p.Version, Installs30d: p.Installs30d, Rank30d: p.Rank30d, AutoUpdates: p.AutoUpdates, Deprecated: p.Deprecated, Disabled: p.Disabled, IsFont: p.IsFont, IsLibrary: p.IsLibrary, EditorChoice: p.Meta.EditorChoice, VersionChangedAt: p.VersionChangedAt.UTC()}
	variants := make([]publicapi.ImageVariant, 0, len(p.IconVariants))
	for _, v := range p.IconVariants {
		variants = append(variants, publicapi.ImageVariant{Width: v.Width, Height: v.Height, Url: v.URL, Type: publicapi.ImageVariantType(v.Type)})
	}
	s.IconVariants = &variants
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
				out = append(out, publicapi.CategoryNode{SourceLocale: localePointer(r.SourceLocale), MachineTranslated: boolPointer(r.MachineTranslated), Slug: r.Slug, Name: r.Name, Description: r.Description, SeoTitle: r.SeoTitle, SeoDescription: r.SeoDescription, Icon: r.Icon, AppliesTo: publicapi.CategoryNodeAppliesTo(r.AppliesTo), HiddenByDefault: r.HiddenByDefault, PackageCount: r.PackageCount, Children: tree(&r.ID, seen)})
			}
			return out
		}
		return tree(nil, map[int64]bool{}), nil
	})
}
func (s *PublicService) Home(ctx context.Context, locale string, requestedSize ...int) (publicapi.HomeData, error) {
	sectionSize := 12
	if len(requestedSize) > 0 {
		sectionSize = requestedSize[0]
	}
	if sectionSize < 1 || sectionSize > 24 {
		return publicapi.HomeData{}, domain.Validation()
	}
	data, err := cache.ReadThrough(ctx, s.Cache, fmt.Sprintf("c:home:c2:%s:%d%s", locale, sectionSize, domain.LinksCacheKey(ctx)), 5*time.Minute, func(ctx context.Context) (publicapi.HomeData, error) {
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
			page, err := s.List(ctx, store.PublicFilter{Kind: section.kind, Sort: section.sort, Locale: locale, Current: 1, Size: sectionSize, IncludeFonts: true})
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
	collections, err := s.Collections(ctx, locale, 1, sectionSize)
	data.Collections = collections.Records
	return data, err

}
func (s *PublicService) Detail(ctx context.Context, kind, token, locale string) (publicapi.PackageDetail, error) {
	d, err := cache.ReadThrough(ctx, s.Cache, cache.PackageDetailPrefix+kind+":"+token+":"+locale+domain.LinksCacheKey(ctx), cache.PackageDetailTTL, func(ctx context.Context) (publicapi.PackageDetail, error) {
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
		collision, err := s.Store.PackageNameCollides(ctx, p)
		if err != nil {
			return d, err
		}
		if collision {
			d.DisplayName = disambiguatedName(d.DisplayName, p.Token, p.Meta.Developer)
		}
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
			if d.Indexing, d.VersionCommittedAt, err = s.indexing(ctx, token, locale); err != nil {
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
			collections, err := s.Store.PackageCollections(ctx, p.ID, locale)
			if err != nil {
				return d, err
			}
			d.Collections = &collections
			if d.SeoOverride, err = s.seoOverride(ctx, p.ID, locale); err != nil {
				return d, err
			}
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
		if err := s.normalizedFacts(ctx, &d, p, locale); err != nil {
			return d, err
		}
		return d, nil
	})
	if err != nil {
		return d, err
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if err := refreshFacts(&d, now); err != nil {
		return d, err
	}
	if err := s.refreshIntentPages(ctx, token, locale, &d); err != nil {
		return d, err
	}
	enabled, err := s.Store.AppIntegrationsEnabled(ctx)
	if err != nil {
		return d, err
	}
	if !enabled && d.Facts != nil {
		empty := []publicapi.AppIntegrationFact{}
		d.Facts.Integrations = &empty
	}
	if d.LatestRelease != nil && d.LatestRelease.Compatibility != nil {
		raw, err := json.Marshal(d.LatestRelease.Compatibility)
		if err != nil {
			return d, err
		}
		var v facts.VersionCompatibility
		if err = json.Unmarshal(raw, &v); err != nil {
			return d, err
		}
		raw, err = json.Marshal(facts.SummarizeCompatibility(v.AppVersion, v.Requirements, now))
		if err != nil {
			return d, err
		}
		if err = json.Unmarshal(raw, d.LatestRelease.Compatibility); err != nil {
			return d, err
		}
	}
	return d, nil
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
		case "de-DE":
			reason = "Seit mehr als 90 Tagen kein Versionsupdate"
		case "fr-FR":
			reason = "Aucune mise à jour de version depuis plus de 90 jours"
		case "it-IT":
			reason = "Nessun aggiornamento di versione da oltre 90 giorni"
		case "zh-Hant":
			reason = "超過 90 天沒有版本更新"
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
	if kind == "link" {
		return publicapi.Dependencies{}, notFound(gorm.ErrRecordNotFound)
	}
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

// SitemapPageSize is the largest sitemap page, the size the web server reads. Bigger pages cost seconds of
// database work and only an internal caller needs them.
const SitemapPageSize = 500

// sitemapLoads coalesces concurrent requests for one sitemap page into a single computation.
var sitemapLoads singleflight.Group

// Sitemap pages indexable casks and links in id order (WP3). Under an enforced index policy only eligible
// apps and their published languages are listed; otherwise the long-standing rule applies (listed,
// still offered by Homebrew, every site language). Apps paused only by the 90-day rule are listed in
// both cases. Records can be fewer than size: total counts candidates. Pages are cached compressed for 10
// minutes; catalog changes clear them with the other package caches.
func (s *PublicService) Sitemap(ctx context.Context, group string, current, size int) (SitemapPage, error) {
	if !ValidPage(current, size, SitemapPageSize) || (group != "" && group != "apps" && group != "fonts") {
		return SitemapPage{}, domain.Validation()
	}
	key := fmt.Sprintf("c:pkg:sitemap:%s:%d:%d", group, current, size)
	// The computation outlives a caller that disconnects, so the page still reaches the cache for the next one.
	result, err, _ := sitemapLoads.Do(fmt.Sprintf("%p|%s", s.Store, key), func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		return cache.ReadThroughCompressed(loadCtx, s.Cache, key, 10*time.Minute, func(ctx context.Context) (SitemapPage, error) {
			return s.sitemap(ctx, group, current, size)
		})
	})
	if err != nil {
		return SitemapPage{}, err
	}
	return result.(SitemapPage), nil
}

func (s *PublicService) sitemap(ctx context.Context, group string, current, size int) (SitemapPage, error) {
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return SitemapPage{}, err
	}
	rows, total, err := s.Store.SitemapIndexRows(ctx, group, current, size)
	out := SitemapPage{Current: current, Size: size, Total: total, Records: []publicapi.SitemapEntry{}}
	if err != nil {
		return out, err
	}
	if err := s.Store.PopulateIntentEligibility(ctx, rows, policy, s.WebBaseURL); err != nil {
		return out, err
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	states, err := s.Store.PageStates(ctx, ids)
	if err != nil {
		return out, err
	}
	// The page-state job stores the first versions page's notes facts. An app it has not reached yet keeps its
	// versions page out until it does: counting here reads every release in every language, and after a migration
	// resets the facts of every app, whole sitemap pages outlast their load timeout and never reach the cache.
	fillStoredNotes(rows, states)
	dates := map[int64]map[string]map[string]time.Time{}
	for _, state := range states {
		if dates[state.PackageID] == nil {
			dates[state.PackageID] = map[string]map[string]time.Time{}
		}
		if dates[state.PackageID][state.Locale] == nil {
			dates[state.PackageID][state.Locale] = map[string]time.Time{}
		}
		key := state.PageKind
		if state.PageNumber > 1 {
			key += fmt.Sprintf(":%d", state.PageNumber)
		}
		dates[state.PackageID][state.Locale][key] = state.ContentModifiedAt.UTC()
	}
	for _, row := range rows {
		locales := []publicapi.SitemapLocale{}
		for _, locale := range seo.Locales {
			pages := packagePages(row, locale, policy)
			if !pages.Overview.InSitemap {
				continue
			}
			lastmod := row.Lastmod(locale)
			pageDates := dates[row.ID][locale]
			if pageDates == nil {
				pageDates = map[string]time.Time{}
			}
			if value, ok := pageDates["overview"]; ok {
				lastmod = &value
			}
			if lastmod != nil && (out.Lastmod == nil || lastmod.After(*out.Lastmod)) {
				out.Lastmod = lastmod
			}
			for _, value := range pageDates {
				if out.Lastmod == nil || value.After(*out.Lastmod) {
					date := value
					out.Lastmod = &date
				}
			}
			locales = append(locales, publicapi.SitemapLocale{Locale: publicapi.Locale(locale), Lastmod: lastmod, Pages: &pages, PageLastmod: &pageDates})
		}
		if len(locales) == 0 {
			continue
		}
		isFont, notes := row.IsFont, row.NotesCount
		out.Records = append(out.Records, publicapi.SitemapEntry{
			Kind: publicapi.PackageKind(row.Kind), Token: row.Token, UpdatedAt: row.UpdatedAt.UTC(),
			IsFont: &isFont, NotesCount: &notes, Locales: &locales,
		})
	}
	return out, err
}

// fillStoredNotes copies the first versions page's stored notes facts into rows. A language without stored facts
// stays empty, so its versions page counts as thin until the page-state job stores them.
func fillStoredNotes(rows []store.IndexRow, states []store.PageState) {
	stored := map[int64]map[string]seo.NotesFacts{}
	for _, state := range states {
		if state.PageKind != "versions" || state.PageNumber != 1 || state.NotesChars == nil || state.NotesEntries == nil {
			continue
		}
		if stored[state.PackageID] == nil {
			stored[state.PackageID] = map[string]seo.NotesFacts{}
		}
		stored[state.PackageID][state.Locale] = seo.NotesFacts{Chars: *state.NotesChars, Entries: *state.NotesEntries}
	}
	for i := range rows {
		facts := stored[rows[i].ID]
		if len(facts) == 0 {
			continue
		}
		if rows[i].NotesPages == nil {
			rows[i].NotesPages = map[int]map[string]seo.NotesFacts{}
		}
		rows[i].NotesPages[1] = facts
	}
}

// indexing is the WP11 decision for one cask's pages, plus the Homebrew commit time of its current
// version. Both are nil for unknown tokens.
func (s *PublicService) indexing(ctx context.Context, token, locale string) (*publicapi.PackageIndexing, *time.Time, error) {
	row, found, err := s.Store.IndexRowByToken(ctx, token)
	if err != nil || !found {
		return nil, nil, err
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := []store.IndexRow{row}
	if err := s.Store.PopulatePageNotes(ctx, rows, 1); err != nil {
		return nil, nil, err
	}
	if err := s.Store.PopulateIntentEligibility(ctx, rows, policy, s.WebBaseURL); err != nil {
		return nil, nil, err
	}
	row = rows[0]
	pages := packagePages(row, locale, policy)
	decision := seo.Decide(row.Facts(), policy)
	var reason *string
	if pages.Overview.Reason != "" {
		reason = &pages.Overview.Reason
	}
	settings, err := s.Store.SEOSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	merged := row.Kind == "cask"
	redirect := merged && settings.Settings.Pages.DetailsRedirect && seo.DecisionRobots(string(pages.Overview.Robots)).Index
	return &publicapi.PackageIndexing{DetailsMerged: &merged, DetailsRedirect: &redirect, Indexable: seo.DecisionRobots(string(pages.Overview.Robots)).Index, Reason: reason, Locales: pages.Overview.Hreflang, Enforced: decision.Enforced, NotesCount: row.NotesCount, Pages: pages}, row.CommittedAt, nil
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
	if r.Attachments != nil && len(*r.Attachments) > 0 {
		return 0, domain.Validation()
	}
	input.PackageVersion, input.MacOSVersion, input.Symptom, input.DiagnosticLog = r.PackageVersion, r.MacosVersion, r.Symptom, r.DiagnosticLog
	if r.Links != nil {
		input.Links = *r.Links
	}
	if r.PackageKind != nil && r.PackageToken != nil {
		p, err := s.Store.PublicPackage(ctx, string(*r.PackageKind), *r.PackageToken, "zh-CN")
		if err == nil {
			input.PackageID = &p.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
	}
	id, err := s.Store.SaveFeedback(ctx, input)
	if errors.Is(err, facts.ErrInvalid) {
		return 0, domain.Validation()
	}
	return id, err
}

// Detail names flow into every tab's title, breadcrumb and structured data.
func disambiguatedName(name, token string, developer *string) string {
	qualifier := token
	if developer != nil && strings.TrimSpace(*developer) != "" {
		qualifier = strings.TrimSpace(*developer)
	}
	suffix := " (" + qualifier + ")"
	if strings.HasSuffix(name, suffix) {
		return name
	}
	return name + suffix
}
