package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
)

func (s *PublicService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

type CollectionPage struct {
	Current int                           `json:"current"`
	Size    int                           `json:"size"`
	Total   int64                         `json:"total"`
	Records []publicapi.CollectionSummary `json:"records"`
}

func (s *PublicService) Collections(ctx context.Context, locale string, current, size int) (CollectionPage, error) {
	out := CollectionPage{Current: current, Size: size, Records: []publicapi.CollectionSummary{}}
	if !ValidPage(current, size, 100) {
		return out, domain.Validation()
	}
	rows, total, err := s.Store.PublicCollections(ctx, locale, s.now(), current, size)
	if err != nil {
		return out, err
	}
	out.Total = total
	for _, row := range rows {
		var v publicapi.CollectionSummary
		if err := json.Unmarshal(row, &v); err != nil {
			return out, err
		}
		out.Records = append(out.Records, v)
	}
	return out, nil
}
func (s *PublicService) Collection(ctx context.Context, slug, locale string) (publicapi.CollectionDetail, error) {
	out := publicapi.CollectionDetail{Items: []publicapi.CollectionItem{}, IconUrls: []*string{}}
	raw, err := s.Store.PublicCollection(ctx, slug, locale, s.now())
	if err != nil {
		return out, notFound(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	rows, err := s.Store.CollectionItems(ctx, slug, locale)
	if err != nil {
		return out, err
	}
	for _, raw := range rows {
		var row struct {
			ID                int64
			SourceLocale      string
			MachineTranslated bool
			Note              *string
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return out, err
		}
		packages, err := s.Store.PublicPackagesByID(ctx, []int64{row.ID}, locale)
		if err != nil {
			return out, err
		}
		if len(packages) == 1 {
			out.Items = append(out.Items, publicapi.CollectionItem{SourceLocale: localePointer(row.SourceLocale), MachineTranslated: boolPointer(row.MachineTranslated), Package: summary(packages[0]), Note: row.Note})
		}
	}
	setup, err := s.collectionSetup(ctx, slug, locale)
	if err != nil {
		return out, err
	}
	encoded, err := json.Marshal(setup)
	if err != nil {
		return out, err
	}
	out.Setup = &publicapi.CollectionSetup{}
	if err := json.Unmarshal(encoded, out.Setup); err != nil {
		return out, err
	}
	return out, nil
}

func (s *PublicService) Brewfile(ctx context.Context, slug string) (string, error) {
	ctx = domain.WithLinks(ctx, false)
	if _, err := s.Store.PublicCollection(ctx, slug, "en-US", s.now()); err != nil {
		return "", notFound(err)
	}
	rows, err := s.Store.CollectionItems(ctx, slug, "en-US")
	if err != nil {
		return "", err
	}
	tokens := []string{}
	for _, raw := range rows {
		var p struct{ Kind, Token string }
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		if facts.ClientCaskToken(p.Kind, p.Token) {
			tokens = append(tokens, p.Token)
		}
	}
	return facts.Brewfile(tokens), nil
}
func (s *PublicService) Features(ctx context.Context, locale string) ([]publicapi.Feature, error) {
	out := []publicapi.Feature{}
	rows, err := s.Store.PublicFeatures(ctx, locale, s.now())
	if err != nil {
		return out, err
	}
	for _, raw := range rows {
		var feature publicapi.Feature
		if err := json.Unmarshal(raw, &feature); err != nil {
			return out, err
		}
		var row struct{ PackageID *int64 }
		if err := json.Unmarshal(raw, &row); err != nil {
			return out, err
		}
		if row.PackageID != nil {
			packages, err := s.Store.PublicPackagesByID(ctx, []int64{*row.PackageID}, locale)
			if err != nil {
				return out, err
			}
			if len(packages) == 1 {
				p := summary(packages[0])
				feature.Package = &p
			}
		}
		out = append(out, feature)
	}
	return out, nil
}

func (s *PublicService) ContentCachePolicy(ctx context.Context) (string, error) {
	var row struct{ Boundary *time.Time }
	err := s.Store.DB.WithContext(ctx).Raw(`SELECT min(t) AS boundary FROM (SELECT publish_at t FROM collections WHERE status IN('scheduled','published') UNION ALL SELECT unpublish_at FROM collections WHERE status IN('scheduled','published') UNION ALL SELECT starts_at FROM features WHERE status IN('scheduled','published') UNION ALL SELECT ends_at FROM features WHERE status IN('scheduled','published')) windows WHERE t>?`, s.now()).Scan(&row).Error
	if err != nil {
		return "", err
	}
	browser, cdn := 60, 300
	if row.Boundary != nil {
		seconds := int(row.Boundary.Sub(s.now()).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		browser = min(browser, seconds)
		cdn = min(cdn, seconds)
	}
	return fmt.Sprintf("public, max-age=%d, s-maxage=%d, must-revalidate", browser, cdn), nil
}
