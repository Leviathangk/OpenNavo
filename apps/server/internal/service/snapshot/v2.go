package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

type File struct {
	Key  string
	Data []byte
}
type Bundle struct {
	Info   publicapi.CatalogSnapshotInfo
	Record store.SnapshotRecord
	Files  []File
}

func localized(values map[string]*string) (publicapi.LocalizedNullableString, error) {
	raw, err := json.Marshal(values)
	if err != nil {
		return publicapi.LocalizedNullableString{}, err
	}
	var out publicapi.LocalizedNullableString
	err = json.Unmarshal(raw, &out)
	return out, err
}

// legacyLocales is the catalog wire contract of clients released before the Mac app had ten languages (up to 0.1.16).
// Clients up to 0.1.13 reject unknown language keys and source languages, so changes, links and snapshot metadata
// carry the other desktop languages only for requests that ask with locales=all. A separate legacy base and CDN
// latest.json keep to the six; modern metadata points to the base with original source languages.
var legacyLocales = []string{"en-US", "zh-CN", "ja-JP", "es-ES", "pt-BR", "ru-RU"}

func legacySource(locale publicapi.Locale) publicapi.Locale {
	if slices.Contains(legacyLocales, string(locale)) {
		return locale
	}
	return "en-US"
}

func catalogLocales(ctx context.Context) func(string) bool {
	if domain.AllCatalogLocales(ctx) {
		return i18n.ValidDesktop
	}
	return func(locale string) bool { return slices.Contains(legacyLocales, locale) }
}

func catalogItems(ctx context.Context, view *store.Store, packages []store.PublicPackage) ([]publicapi.CatalogItem, error) {
	allowed := catalogLocales(ctx)
	ids := make([]int64, 0, len(packages))
	for _, p := range packages {
		ids = append(ids, p.ID)
	}
	texts, err := view.SnapshotTexts(ctx, ids)
	if err != nil {
		return nil, err
	}
	names, summaries := map[int64]map[string]*string{}, map[int64]map[string]*string{}
	for _, p := range packages {
		names[p.ID] = map[string]*string{}
		summaries[p.ID] = map[string]*string{}
		if p.DescEN != nil && *p.DescEN != "" {
			summaries[p.ID]["en-US"] = p.DescEN
		}
	}
	for _, text := range texts {
		if !allowed(text.Locale) {
			continue
		}
		if text.DisplayName != nil {
			names[text.PackageID][text.Locale] = text.DisplayName
		}
		if text.Summary != nil {
			summaries[text.PackageID][text.Locale] = text.Summary
		}
	}
	result := make([]publicapi.CatalogItem, 0, len(packages))
	for _, p := range packages {
		// Read structure from the same view; never copy negotiated English fallback text into other language maps.
		if !allowed(p.SourceLocale) {
			p.SourceLocale = "en-US"
		}
		p.I18n.DisplayName = names[p.ID]["zh-CN"]
		entry, err := item(p, p)
		if err != nil {
			return nil, err
		}
		source := publicapi.Locale(p.SourceLocale)
		entry.SourceLocale = &source
		entry.DisplayName, err = localized(names[p.ID])
		if err != nil {
			return nil, err
		}
		entry.Summary, err = localized(summaries[p.ID])
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}
func categoryData(categories []store.SnapshotCategory, allowed func(string) bool) ([]publicapi.CatalogBaseCategory, map[string]map[string]string, error) {
	result := make([]publicapi.CatalogBaseCategory, 0, len(categories))
	names := map[string]map[string]string{}
	for _, locale := range supportedLocales() {
		if allowed(locale) {
			names[locale] = map[string]string{}
		}
	}
	for _, category := range categories {
		raw, err := json.Marshal(category)
		if err != nil {
			return nil, nil, err
		}
		var base publicapi.CatalogBaseCategory
		if err = json.Unmarshal(raw, &base); err != nil {
			return nil, nil, err
		}
		if !allowed(string(base.SourceLocale)) {
			base.SourceLocale = publicapi.Locale("en-US")
		}
		result = append(result, base)
		for locale, name := range category.Name {
			if target, ok := names[locale]; ok && name != "" {
				target[category.Slug] = name
			}
		}
	}
	return result, names, nil
}
func compress(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err = writer.Write(raw); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
func (s *Service) bundle(ctx context.Context, allowSkip bool) (Bundle, bool, error) {
	var result Bundle
	skip := false
	err := s.Store.CatalogRead(ctx, func(view *store.Store) error {
		now := s.now()
		_, cursor, err := view.CatalogBounds(ctx)
		if err != nil {
			return err
		}
		previous, err := view.LatestSnapshot(ctx)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if previous.ID != 0 && !now.After(previous.CreatedAt) {
			now = previous.CreatedAt.Add(time.Nanosecond)
		}
		version := now.UnixMilli()
		if previous.TextPacks != nil {
			version = max(version, previous.TextPacks.EnUS.Version+1)
		}
		// A snapshot missing a desktop language's text pack (published before that language existed) is rebuilt.
		if allowSkip && previous.ID != 0 && now.Sub(previous.CreatedAt) < 24*time.Hour && cursor <= previous.Seq && previous.ModernBase != nil && packsCover(previous.ModernBase.TextPacks) {
			analytics, err := view.LastAnalytics(ctx)
			if err != nil {
				return err
			}
			var classification struct{ At time.Time }
			if err = view.DB.WithContext(ctx).Raw("SELECT GREATEST(COALESCE((SELECT max(updated_at) FROM categories),'epoch'::timestamptz),COALESCE((SELECT max(updated_at) FROM category_i18n),'epoch'::timestamptz)) AS at").Scan(&classification).Error; err != nil {
				return err
			}
			if !analytics.After(previous.CreatedAt) && !classification.At.After(previous.CreatedAt) {
				result.Info = modernInfo(previous)
				skip = true
				return nil
			}
		}
		packages, err := view.SnapshotPackages(ctx, "en-US")
		if err != nil {
			return err
		}
		items, err := catalogItems(domain.WithAllCatalogLocales(ctx, true), view, packages)
		if err != nil {
			return err
		}
		categories, err := view.SnapshotCategories(ctx)
		if err != nil {
			return err
		}
		baseCategories, names, err := categoryData(categories, i18n.ValidDesktop)
		if err != nil {
			return err
		}
		base := publicapi.CatalogBaseSnapshot{FormatVersion: 2, GeneratedAt: now, Cursor: cursor, Categories: baseCategories, Items: []publicapi.CatalogBaseItem{}}
		packs := map[string]publicapi.CatalogTextPack{}
		for _, locale := range supportedLocales() {
			packs[locale] = publicapi.CatalogTextPack{FormatVersion: 2, Locale: publicapi.Locale(locale), Version: version, Cursor: cursor, GeneratedAt: now, Items: []publicapi.CatalogTextItem{}, Categories: []publicapi.CatalogTextCategory{}}
		}
		for _, item := range items {
			raw, err := json.Marshal(item)
			if err != nil {
				return err
			}
			var entry publicapi.CatalogBaseItem
			if err = json.Unmarshal(raw, &entry); err != nil {
				return err
			}
			base.Items = append(base.Items, entry)
			var values struct {
				Names     map[string]*string `json:"displayName"`
				Summaries map[string]*string `json:"summary"`
			}
			if err = json.Unmarshal(raw, &values); err != nil {
				return err
			}
			for _, locale := range supportedLocales() {
				name, summary := values.Names[locale], values.Summaries[locale]
				if name == nil && summary == nil {
					continue
				}
				pack := packs[locale]
				pack.Items = append(pack.Items, publicapi.CatalogTextItem{Kind: publicapi.CatalogTextItemKind(item.Kind), Token: item.Token, DisplayName: name, Summary: summary})
				packs[locale] = pack
			}
		}
		metadata := map[string]publicapi.CatalogTextPackInfo{}
		// Use stable category structure order so map iteration cannot produce irreproducible file hashes.
		for _, locale := range supportedLocales() {
			pack := packs[locale]
			for _, category := range categories {
				if name, ok := names[locale][category.Slug]; ok {
					pack.Categories = append(pack.Categories, publicapi.CatalogTextCategory{Slug: category.Slug, Name: name})
				}
			}
			data, err := compress(pack)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(data)
			digest := hex.EncodeToString(hash[:])
			key := fmt.Sprintf("snapshots/text-v2-%s-%d-%s.json.gz", locale, version, digest[:12])
			result.Files = append(result.Files, File{key, data})
			metadata[locale] = publicapi.CatalogTextPackInfo{Locale: publicapi.Locale(locale), Cursor: cursor, Version: version, Url: s.Objects.URL(key), Sha256: digest, Bytes: int64(len(data))}
		}
		data, err := compress(base)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		digest := hex.EncodeToString(hash[:])
		key := fmt.Sprintf("snapshots/modern-base-v2-%d-%s.json.gz", cursor, digest[:12])
		result.Files = append([]File{{key, data}}, result.Files...)
		raw, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		var manifest publicapi.CatalogTextPacks
		if err = json.Unmarshal(raw, &manifest); err != nil {
			return err
		}
		result.Record = store.SnapshotRecord{FormatVersion: 2, Seq: cursor, ItemCount: len(items), StorageKey: key, URL: s.Objects.URL(key), SHA256: digest, Bytes: int64(len(data)), CreatedAt: now, TextPacks: &manifest}
		// Publish a compatibility projection separately; never falsify modern source locales.
		for i := range base.Items {
			base.Items[i].SourceLocale = legacySource(base.Items[i].SourceLocale)
		}
		for i := range base.Categories {
			base.Categories[i].SourceLocale = legacySource(base.Categories[i].SourceLocale)
		}
		legacyData, err := compress(base)
		if err != nil {
			return err
		}
		legacyHash := sha256.Sum256(legacyData)
		legacyDigest := hex.EncodeToString(legacyHash[:])
		legacyKey := fmt.Sprintf("snapshots/base-v2-%d-%s.json.gz", cursor, legacyDigest[:12])
		result.Files = append(result.Files, File{legacyKey, legacyData})
		// The old reader knows only catalog_snapshots and snapshotManifest. Keep both
		// legacy-safe so a backend rollback cannot expose new source locales or pack keys.
		modern := info(result.Record)
		result.Record.StorageKey = legacyKey
		result.Record.URL, result.Record.SHA256, result.Record.Bytes = s.Objects.URL(legacyKey), legacyDigest, int64(len(legacyData))
		result.Record.TextPacks = legacyInfo(modern).TextPacks
		result.Record.ModernBase = &modern
		result.Info = modern

		return nil
	})
	return result, skip, err
}

// BuildLocal uses a read-only transaction; it publishes no objects and changes neither metadata nor run records.
func (s *Service) BuildLocal(ctx context.Context) (Bundle, error) {
	bundle, _, err := s.bundle(ctx, false)
	return bundle, err
}

// packsCover reports whether a published snapshot has a text pack for every desktop language.
func packsCover(packs *publicapi.CatalogTextPacks) bool {
	if packs == nil {
		return false
	}
	raw, err := json.Marshal(packs)
	if err != nil {
		return false
	}
	var present map[string]json.RawMessage
	if json.Unmarshal(raw, &present) != nil {
		return false
	}
	for _, locale := range supportedLocales() {
		if value, ok := present[locale]; !ok || string(value) == "null" {
			return false
		}
	}
	return true
}

// v2 snapshots publish a text pack for every desktop language, independent of website languages.
func supportedLocales() []string {
	out := make([]string, 0, len(i18n.DesktopLocales))
	for _, locale := range i18n.DesktopLocales {
		out = append(out, string(locale))
	}
	return out
}
