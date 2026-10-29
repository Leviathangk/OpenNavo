package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"path"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/pinyin"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

type Objects interface {
	Put(context.Context, string, []byte, string) error
	URL(string) string
}
type Service struct {
	Store   *store.Store
	Objects Objects
	Now     func() time.Time
}
type Document struct {
	FormatVersion int                      `json:"formatVersion"`
	GeneratedAt   time.Time                `json:"generatedAt"`
	Cursor        int64                    `json:"cursor"`
	Categories    []store.SnapshotCategory `json:"categories"`
	Items         []publicapi.CatalogItem  `json:"items"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func info(r store.SnapshotRecord) publicapi.CatalogSnapshotInfo {
	return publicapi.CatalogSnapshotInfo{FormatVersion: r.FormatVersion, Cursor: r.Seq, ItemCount: r.ItemCount, Url: r.URL, Sha256: r.SHA256, Bytes: r.Bytes, CreatedAt: r.CreatedAt.UTC(), TextPacks: r.TextPacks}
}

func modernInfo(r store.SnapshotRecord) publicapi.CatalogSnapshotInfo {
	if r.ModernBase != nil {
		return *r.ModernBase
	}
	return info(r)
}

// legacyInfo lists only the original six text packs: clients up to 0.1.13 reject any other language key, which would
// stop their catalog sync. latest.json and requests without locales=all get it.
func legacyInfo(value publicapi.CatalogSnapshotInfo) publicapi.CatalogSnapshotInfo {
	if value.TextPacks != nil {
		packs := *value.TextPacks
		packs.DeDE, packs.FrFR, packs.ItIT, packs.ZhHant = nil, nil, nil, nil
		value.TextPacks = &packs
	}
	return value
}
func nonnil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
func item(zh, en store.PublicPackage) (publicapi.CatalogItem, error) {
	var art homebrew.CaskArtifacts
	if len(zh.Artifacts) > 0 {
		if err := json.Unmarshal(zh.Artifacts, &art); err != nil {
			return publicapi.CatalogItem{}, err
		}
	}
	summaryEN := en.I18n.Summary
	if summaryEN == nil {
		summaryEN = en.DescEN
	}
	r := publicapi.CatalogItem{Kind: publicapi.PackageKind(zh.Kind), Token: zh.Token, Name: zh.Name, Names: nonnil(zh.Names), DisplayName: publicapi.LocalizedNullableString{ZhCN: zh.I18n.DisplayName, EnUS: en.I18n.DisplayName}, Summary: publicapi.LocalizedNullableString{ZhCN: zh.I18n.Summary, EnUS: summaryEN}, Version: zh.Version, Homepage: zh.Homepage, AutoUpdates: zh.AutoUpdates, Deprecated: zh.Deprecated, Disabled: zh.Disabled, Hidden: zh.Meta.Hidden, IsFont: zh.IsFont, IsLibrary: zh.IsLibrary, Categories: []string{}, Tags: nonnil(zh.Meta.Tags), Installs30d: zh.Installs30d, Rank30d: zh.Rank30d, Popularity: float32(zh.Popularity), IconUrl: zh.IconURL, AccentColor: zh.Meta.AccentColor, Apps: nonnil(art.Apps), Binaries: nonnil(art.Binaries), VersionChangedAt: zh.VersionChangedAt.UTC()}
	r.DownloadSize = zh.DownloadSize
	r.Installs90d, r.Installs365d = &zh.Installs90d, &zh.Installs365d
	// Only third-party casks carry their tap, so official items and fixtures keep their shape.
	if !homebrew.IsOfficialTap(zh.Tap) {
		tap := zh.Tap
		r.Tap = &tap
		if _, upstream, err := homebrew.ParseTapCask(zh.FullToken); err == nil && upstream != zh.Token {
			r.BrewToken = &upstream
		}
		if zh.Kind == "cask" && art.Entries != nil {
			targets := []string{}
			complete := true
			for _, entry := range art.Entries {
				if entry.Type != "app" {
					continue
				}
				if len(entry.Sources) != 1 {
					complete = false
					break
				}
				destination := path.Base(entry.Sources[0])
				if entry.Target != nil {
					destination = *entry.Target
				}
				targets = append(targets, destination)
			}
			if complete && len(targets) == len(art.Apps) {
				r.AppTargets = &targets
			}
		}
	}
	if zh.Kind == "formula" {
		r.OnRequest = &struct {
			D30  int `json:"d30"`
			D365 int `json:"d365"`
			D90  int `json:"d90"`
		}{D30: zh.OnRequest30d, D90: zh.OnRequest90d, D365: zh.OnRequest365d}
	}
	for _, c := range zh.Categories {
		r.Categories = append(r.Categories, c.Slug)
	}
	name := ""
	if zh.I18n.DisplayName != nil {
		name = *zh.I18n.DisplayName
	}
	for _, n := range zh.Names {
		name += " " + n
	}
	full, initials := pinyin.Spellings(name)
	if full != "" {
		text := full + " " + initials
		r.Pinyin = &text
	}
	return r, nil
}
func (s *Service) Latest(ctx context.Context) (publicapi.CatalogSnapshotInfo, error) {
	r, err := s.Store.LatestSnapshot(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return publicapi.CatalogSnapshotInfo{}, &domain.AppError{Code: domain.CodeUpstreamUnavailable, HTTPStatus: 503}
	}
	if domain.AllCatalogLocales(ctx) {
		return modernInfo(r), err
	}
	return legacyInfo(info(r)), err
}
func (s *Service) Build(ctx context.Context) (map[string]any, error) {
	var stats map[string]any
	err := s.Store.WithAdvisoryLock(ctx, "snapshot:build", func(ctx context.Context) error {
		bundle, skip, err := s.bundle(ctx, true)
		if err != nil {
			return err
		}
		if skip {
			stats = map[string]any{"skipped": true, "cursor": bundle.Info.Cursor}
			return nil
		}
		// Publish latest only after the base object and every language object are readable; intermediate failures never touch the previous latest.
		for _, file := range bundle.Files {
			if err = s.Objects.Put(ctx, file.Key, file.Data, "application/gzip"); err != nil {
				return err
			}
		}
		// Every client version reads the CDN copy, so it keeps the legacy six packs; the API lists the rest.
		metadata, err := json.Marshal(legacyInfo(info(bundle.Record)))
		if err != nil {
			return err
		}
		if err = s.Objects.Put(ctx, "snapshots/latest.json", metadata, "application/json"); err != nil {
			return err
		}
		if err = s.Store.SaveSnapshot(ctx, bundle.Record); err != nil {
			return err
		}
		stats = map[string]any{"cursor": bundle.Info.Cursor, "items": bundle.Info.ItemCount, "bytes": bundle.Info.Bytes, "textPacks": bundle.Info.TextPacks, "sha256": bundle.Info.Sha256, "url": bundle.Info.Url}
		return nil
	})
	return stats, err
}

func encodeDocument(doc Document) ([]byte, []byte, error) {
	var value any = doc
	if doc.FormatVersion == 1 {
		// Archived v1 fixtures retain explicit Chinese/English nulls; production v2 serializes only actual keys.
		items := make([]json.RawMessage, 0, len(doc.Items))
		for _, item := range doc.Items {
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, nil, err
			}
			var fields map[string]any
			if err = json.Unmarshal(raw, &fields); err != nil {
				return nil, nil, err
			}
			for _, key := range []string{"displayName", "summary"} {
				values := fields[key].(map[string]any)
				for _, locale := range []string{"en-US", "zh-CN"} {
					if _, ok := values[locale]; !ok {
						values[locale] = nil
					}
				}
			}
			raw, err = json.Marshal(fields)
			if err != nil {
				return nil, nil, err
			}
			items = append(items, raw)
		}
		value = struct {
			FormatVersion int                      `json:"formatVersion"`
			GeneratedAt   time.Time                `json:"generatedAt"`
			Cursor        int64                    `json:"cursor"`
			Categories    []store.SnapshotCategory `json:"categories"`
			Items         []json.RawMessage        `json:"items"`
		}{doc.FormatVersion, doc.GeneratedAt, doc.Cursor, doc.Categories, items}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		return nil, nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, nil, err
	}
	return raw, compressed.Bytes(), nil
}
func (s *Service) Changes(ctx context.Context, since int64, limit int) (publicapi.CatalogChanges, error) {
	out := publicapi.CatalogChanges{Changes: []publicapi.CatalogChange{}, NextCursor: since}
	if since < 0 || limit < 1 || limit > 1000 {
		return out, domain.Validation()
	}
	err := s.Store.CatalogRead(ctx, func(view *store.Store) error {
		low, high, err := view.CatalogBounds(ctx)
		if err != nil {
			return err
		}
		if since < low-1 || since > high {
			return &domain.AppError{Code: domain.CodeCursorExpired, HTTPStatus: 410}
		}
		rows, err := view.CatalogChanges(ctx, since, limit+1)
		if err != nil {
			return err
		}
		out.HasMore = len(rows) > limit
		if out.HasMore {
			rows = rows[:limit]
		}
		ids := []int64{}
		for _, r := range rows {
			if r.Op == "upsert" {
				ids = append(ids, r.PackageID)
			}
		}
		zh, err := view.PublicPackagesByID(ctx, ids, "zh-CN")
		if err != nil {
			return err
		}
		entries, err := catalogItems(ctx, view, zh)
		if err != nil {
			return err
		}
		zhMap := map[int64]store.PublicPackage{}
		entryMap := map[int64]publicapi.CatalogItem{}
		for index, p := range zh {
			zhMap[p.ID] = p
			entryMap[p.ID] = entries[index]
		}
		categories, err := view.SnapshotCategories(ctx)
		if err != nil {
			return err
		}
		structure, names, err := categoryData(categories, catalogLocales(ctx))
		if err != nil {
			return err
		}
		out.Categories = &structure
		raw, err := json.Marshal(names)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &out.CategoryNames); err != nil {
			return err
		}

		for _, r := range rows {
			change := publicapi.CatalogChange{Cursor: r.Seq, Kind: publicapi.PackageKind(r.Kind), Token: r.Token, Op: publicapi.CatalogChangeOp(r.Op)}
			if r.Op == "upsert" {
				p, ok := zhMap[r.PackageID]
				if !ok || p.Kind != r.Kind || p.Token != r.Token {
					change.Op = "delete"
				} else {
					entry := entryMap[r.PackageID]
					change.Item = &entry
				}
			}
			out.Changes = append(out.Changes, change)
			out.NextCursor = r.Seq
		}
		if !out.HasMore {
			out.NextCursor = high
		}
		return nil
	})
	return out, err
}
