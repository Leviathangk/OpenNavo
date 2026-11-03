package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/pinyin"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// PurgePendingWarning is returned to the admin write when origin revocation completed but the CDN
// purge is queued. Edge copies can persist until the queue resumes.
const PurgePendingWarning = "Snapshot revocation committed; CDN purge is pending; edge copies may persist until the purge is resumed."

// maxQueuedSnapshotPurges bounds the shared queue exactly like the notifier's page limit; snapshot
// revocations queue one prefix, so this only matters if many revocations defer at once.
const maxQueuedSnapshotPurges = 1000

// ConvergingWarning is returned to the admin when the visibility write committed but a burst of
// concurrent changes exhausted the bounded rebuild loop. The outbox finishes the revocation, so a
// retry is not needed and the write must not be reported as failed.
const ConvergingWarning = "Visibility change committed; snapshot revocation is still converging after concurrent changes and will finish from the outbox."

type Objects interface {
	Put(context.Context, string, []byte, string) error
	URL(string) string
}
type Service struct {
	Store   *store.Store
	Objects Objects
	Now     func() time.Time
	Purge   func(context.Context, []string) error
	// SelfCheck verifies that a new bundle publishes no private token; tests replace it to prove
	// that a failing check blocks the revocation instead of acknowledging it.
	SelfCheck func(Bundle, []string) (string, bool)
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
	visibility, err := s.Store.SnapshotVisibility(ctx)
	if err != nil {
		return publicapi.CatalogSnapshotInfo{}, err
	}
	if visibility.RevocationGeneration > visibility.SnapshotRevocation {
		s.markUnavailable(ctx)
		return publicapi.CatalogSnapshotInfo{}, &domain.AppError{Code: domain.CodeUpstreamUnavailable, HTTPStatus: 503}
	}
	r, err := s.Store.LatestSnapshot(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.markUnavailable(ctx)
		return publicapi.CatalogSnapshotInfo{}, &domain.AppError{Code: domain.CodeUpstreamUnavailable, HTTPStatus: 503}
	}
	if err != nil {
		return publicapi.CatalogSnapshotInfo{}, err
	}
	s.clearUnavailable(ctx)
	if domain.AllCatalogLocales(ctx) {
		return modernInfo(r), err
	}
	return legacyInfo(info(r)), err
}

// markUnavailable records the first moment Latest() answered 503; clearUnavailable resets it.
// The metric is advisory, so a Redis failure never changes the response.
func (s *Service) markUnavailable(ctx context.Context) {
	if s.Store != nil && s.Store.Redis != nil {
		_ = s.Store.Redis.SetNX(ctx, "metrics:snapshot:unavailable_since", s.now().Unix(), 0).Err()
	}
}

func (s *Service) clearUnavailable(ctx context.Context) {
	if s.Store != nil && s.Store.Redis != nil {
		_ = s.Store.Redis.Del(ctx, "metrics:snapshot:unavailable_since").Err()
	}
}

func (s *Service) Build(ctx context.Context) (map[string]any, error) {
	var stats map[string]any
	err := s.Store.WithBlockingAdvisoryLock(ctx, "snapshot:build", func(ctx context.Context) error {
		visibility, err := s.Store.SnapshotVisibility(ctx)
		if err != nil {
			return err
		}
		revoke := visibility.RevocationGeneration > visibility.SnapshotRevocation
		purgeWarning := ""
		var obsolete []string
		var objects revocableObjects
		if revoke {
			var ok bool
			objects, ok = s.Objects.(revocableObjects)
			if !ok {
				return errors.New("snapshot revocation requires object deletion")
			}
			obsolete, err = objects.List(ctx, "snapshots/")
			if err != nil {
				return err
			}
		}
		// The private set comes from the bundle's own transaction, so a package that turns private
		// while the documents are built cannot slip past the self-check below.
		bundle, skip, err := s.bundle(ctx, !revoke, revoke)
		if err != nil {
			return err
		}
		if skip {
			stats = map[string]any{"skipped": true, "cursor": bundle.Info.Cursor}
			return nil
		}
		if revoke {
			// A package that turned private after the read-only snapshot is still in these
			// documents. Publish nothing that names them; the caller rebuilds.
			if err = s.revocationCurrent(ctx, visibility); err != nil {
				return err
			}
		}
		// Publish latest only after the base object and every language object are readable; intermediate failures never touch the previous latest.
		for _, file := range bundle.Files {
			if err = s.Objects.Put(ctx, file.Key, file.Data, "application/gzip"); err != nil {
				return err
			}
		}
		if revoke {
			// The bundle files are unreferenced until latest.json names them, so this is the last
			// moment to stop a manifest that would publish a package that just turned private.
			if err = s.revocationCurrent(ctx, visibility); err != nil {
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
		if revoke {
			// The rebuilt latest.json is the tombstone: it names only the new bundle. Old objects go
			// before the revocation is acknowledged, so a retry continues from the same generation.
			keep := map[string]bool{"snapshots/latest.json": true}
			for _, file := range bundle.Files {
				keep[file.Key] = true
			}
			for _, key := range obsolete {
				if !keep[key] {
					if err := objects.Delete(ctx, key); err != nil {
						return fmt.Errorf("revoke snapshot object: %w", err)
					}
				}
			}
			check := s.SelfCheck
			if check == nil {
				check = bundleContainsToken
			}
			if token, found := check(bundle, bundle.PrivateTokens); found {
				slog.Default().Error("snapshot self-check found a private package", "token", token)
				return fmt.Errorf("snapshot self-check still contains private package %q", token)
			}
			prefix, parseErr := url.Parse(s.Objects.URL("snapshots/"))
			if parseErr != nil || prefix.Host == "" {
				return errors.New("invalid snapshot CDN URL")
			}
			target := prefix.Host + prefix.EscapedPath()
			if s.Purge == nil {
				// A missing purger must not drop the obligation: queue the same durable debt the
				// notifier drains, and keep the admin warning.
				if queueErr := s.Store.QueueURLPurges(ctx, []string{target}, maxQueuedSnapshotPurges, nil); queueErr != nil {
					return fmt.Errorf("queue revoked snapshot purge: %w", queueErr)
				}
				purgeWarning = PurgePendingWarning
			} else if purgeErr := s.Purge(ctx, []string{target}); purgeErr != nil {
				if !errors.Is(purgeErr, urlnotify.ErrPurgeDeferred) {
					return fmt.Errorf("queue revoked snapshot purge: %w", purgeErr)
				}
				purgeWarning = PurgePendingWarning
				slog.Default().Error("snapshot CDN purge deferred", "status", "paused: Cloudflare purge unavailable", "error", purgeErr)
			}
			if err = s.revocationCurrent(ctx, visibility); err != nil {
				return err
			}
			if err = s.Store.CompleteSnapshotRevocation(ctx, visibility.RevocationGeneration, bundle.Record); err != nil {
				return err
			}
		}
		stats = map[string]any{"cursor": bundle.Info.Cursor, "items": bundle.Info.ItemCount, "bytes": bundle.Info.Bytes, "textPacks": bundle.Info.TextPacks, "sha256": bundle.Info.Sha256, "url": bundle.Info.Url, "purgeWarning": purgeWarning}
		return nil
	})
	return stats, err
}

type revocableObjects interface {
	List(context.Context, string) ([]string, error)
	Delete(context.Context, string) error
}

// ErrRevocationSuperseded reports a revocation whose generation moved to a newer build before it
// could be acknowledged. The bundle on screen may still hold the package that turned private, so
// the build publishes nothing it has not already written and the caller runs again.
var ErrRevocationSuperseded = errors.New("snapshot revocation superseded by a newer visibility change")

// revocationCurrent fails when a visibility change has advanced the revocation generation since
// the build read it, so no acknowledgement can cover a package the build never filtered.
func (s *Service) revocationCurrent(ctx context.Context, seen store.SnapshotVisibility) error {
	current, err := s.Store.SnapshotVisibility(ctx)
	if err != nil {
		return err
	}
	if current.RevocationGeneration > seen.RevocationGeneration {
		slog.Default().Warn("snapshot revocation superseded", "generation", seen.RevocationGeneration, "current", current.RevocationGeneration)
		return ErrRevocationSuperseded
	}
	return nil
}

// RevokePrivate completes durable revocation before a visibility write is acknowledged. A build
// that another change superseded returns ErrRevocationSuperseded; the loop then reads the newer
// generation and builds again, up to a bound that keeps a burst of admin writes from looping here.
// Build also retries the same work from the outbox.
func (s *Service) RevokePrivate(ctx context.Context) (string, error) {
	warning := ""
	for attempt := 0; attempt < maxRevocationAttempts; attempt++ {
		state, err := s.Store.SnapshotVisibility(ctx)
		if err != nil || state.RevocationGeneration <= state.SnapshotRevocation {
			return warning, err
		}
		stats, err := s.Build(ctx)
		if errors.Is(err, ErrRevocationSuperseded) {
			continue
		}
		if err != nil {
			return warning, err
		}
		if value, ok := stats["purgeWarning"].(string); ok && value != "" {
			warning = value
		}
	}
	// Every attempt lost to a newer visibility change; the write is not acknowledged and the
	// outbox rebuilds against the final generation.
	return warning, ErrRevocationSuperseded
}

// maxRevocationAttempts bounds the rebuilds one admin write triggers when other changes keep
// advancing the generation while it runs.
const maxRevocationAttempts = 5

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
