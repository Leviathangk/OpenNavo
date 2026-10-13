package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Lifecycle tells the web what a package URL whose detail returned 404 should do (WP2): redirect a
// renamed app, answer 410 with a summary for a removed one, or 404. Catalog syncs clear c:pkg:*.
func (s *PublicService) Lifecycle(ctx context.Context, kind, token, locale string) (publicapi.PackageLifecycle, error) {
	return cache.ReadThrough(ctx, s.Cache, "c:pkg:life:"+kind+":"+token+":"+locale, 10*time.Minute, func(ctx context.Context) (publicapi.PackageLifecycle, error) {
		row, found, err := s.Store.PackageLifecycle(ctx, kind, token)
		if err != nil {
			return publicapi.PackageLifecycle{}, err
		}
		if !found {
			return publicapi.PackageLifecycle{}, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
		}
		out := publicapi.PackageLifecycle{State: publicapi.Active, Token: row.Token}
		if row.RemovedAt == nil {
			if row.Token != token {
				out.State = publicapi.Renamed
			}
			return out, nil
		}
		out.State = publicapi.Removed
		removed, err := s.removedSummary(ctx, row, locale)
		out.Removed = &removed
		return out, err
	})
}

func (s *PublicService) removedSummary(ctx context.Context, row store.LifecycleRow, locale string) (publicapi.RemovedPackage, error) {
	out := publicapi.RemovedPackage{Token: row.Token, LastVersion: row.Version, RemovedAt: row.RemovedAt.UTC(), Alternatives: []publicapi.PackageSummary{}, Categories: []publicapi.CategoryRef{}}
	self, err := s.Store.PublicPackagesByID(ctx, []int64{row.ID}, locale)
	if err != nil || len(self) == 0 {
		return out, err
	}
	info := summary(self[0])
	out.DisplayName, out.Summary, out.IconUrl = info.DisplayName, info.Summary, info.IconUrl
	for _, c := range self[0].Categories {
		out.Categories = append(out.Categories, publicapi.CategoryRef{SourceLocale: localePointer(c.SourceLocale), MachineTranslated: boolPointer(c.MachineTranslated), Slug: c.Slug, Name: c.Name, Icon: c.Icon})
	}
	ids := []int64{}
	// Homebrew writes replacements as "cask:token" or "formula:token"; only available casks link.
	if row.Replacement != nil {
		if kind, token, ok := strings.Cut(*row.Replacement, ":"); ok && kind == "cask" {
			if id, live, err := s.Store.LivePackageID(ctx, kind, token); err != nil {
				return out, err
			} else if live {
				ids = append(ids, id)
			}
		}
	}
	similar, err := s.Store.LifecycleAlternatives(ctx, row.ID, 6)
	if err != nil {
		return out, err
	}
	for _, id := range similar {
		if len(ids) == 0 || id != ids[0] {
			ids = append(ids, id)
		}
	}
	rows, err := s.Store.PublicPackagesByID(ctx, ids, locale)
	if err != nil {
		return out, err
	}
	for index, p := range rows {
		item := summary(p)
		if index == 0 && row.Replacement != nil && len(rows) > 0 && strings.HasSuffix(*row.Replacement, ":"+p.Token) {
			out.Replacement = &item
			continue
		}
		if len(out.Alternatives) < 6 {
			out.Alternatives = append(out.Alternatives, item)
		}
	}
	return out, nil
}
