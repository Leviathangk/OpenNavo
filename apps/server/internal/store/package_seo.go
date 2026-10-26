package store

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/traditional"
)

// Overview title and description overrides (package_seo_overrides): single-line plain text within the
// lengths search results show. The site adds its brand suffix; the stored value is what was written.
const (
	SEOTitleMaxChars       = 70
	SEODescriptionMaxChars = 200
)

// PackageSEO is an app's overview title and description in one language; a nil field keeps the template.
// A Derived zh-Hant override is zh-CN's converted to Traditional Chinese; it is never stored.
type PackageSEO struct {
	PackageID          int64
	Locale             string
	Title, Description *string
	UpdatedAt          time.Time
	// UpdatedBy is the write owner: admin:<user>, agent:<client>:<token> or system.
	UpdatedBy string
	Derived   bool
}

// SEOText is one requested field: Set is false when the request leaves the field as it is, and Value is
// nil when it clears the field.
type SEOText struct {
	Set   bool
	Value *string
}

// NormalizeSEOText trims a title or description and checks it: plain text (no < or >) on one line (no
// control characters, line or paragraph separators) within limit characters. Empty text clears the field.
// field is title or description. This is the rule; the table's CHECK only backs it up, since what
// [[:cntrl:]] matches depends on the database's character classification.
func NormalizeSEOText(value *string, limit int, field string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text := strings.TrimSpace(*value)
	if text == "" {
		return nil, nil
	}
	if strings.ContainsAny(text, "<>") || strings.ContainsFunc(text, lineBreaking) || !utf8.ValidString(text) {
		return nil, domain.Rejected("seo_text_invalid:" + field)
	}
	if utf8.RuneCountInString(text) > limit {
		return nil, domain.Rejected("field_too_long:" + field)
	}
	return &text, nil
}

// lineBreaking reports characters that break or control a line: C0 and C1 controls (\r, \n, NEL among them)
// and the Unicode line and paragraph separators.
func lineBreaking(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp)
}

// PackageSEOLocale reports whether a language takes overrides: every site language except the derived one.
func PackageSEOLocale(locale string) bool {
	return i18n.Writable(locale)
}

const packageSEOColumns = `o.package_id,o.locale,o.title,o.description,o.updated_at,o.updated_by FROM package_seo_overrides o`

// PackageSEOOverrides lists an app's overrides in site language order, with zh-Hant derived from zh-CN's.
func (s *Store) PackageSEOOverrides(ctx context.Context, id int64) ([]PackageSEO, error) {
	rows := []PackageSEO{}
	if err := s.DB.WithContext(ctx).Raw("SELECT "+packageSEOColumns+" WHERE o.package_id=?", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Locale == traditional.Source {
			derived, err := s.traditionalSEO(ctx, row)
			if err != nil {
				return nil, err
			}
			rows = append(rows, derived)
			break
		}
	}
	slices.SortFunc(rows, func(a, b PackageSEO) int {
		return cmp.Compare(slices.Index(seo.Locales, a.Locale), slices.Index(seo.Locales, b.Locale))
	})
	return rows, nil
}

// PackageSEOOverride reads an app's override in one language, or nil when the language keeps the template.
// zh-Hant converts zh-CN's override, with the glossary's protected terms, as its derived text does.
func (s *Store) PackageSEOOverride(ctx context.Context, id int64, locale string) (*PackageSEO, error) {
	source := locale
	if locale == traditional.Locale {
		source = traditional.Source
	}
	if !PackageSEOLocale(source) {
		return nil, nil
	}
	var rows []PackageSEO
	if err := s.DB.WithContext(ctx).Raw("SELECT "+packageSEOColumns+" WHERE o.package_id=? AND o.locale=?", id, source).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if locale == traditional.Locale {
		derived, err := s.traditionalSEO(ctx, rows[0])
		return &derived, err
	}
	return &rows[0], nil
}

func (s *Store) traditionalSEO(ctx context.Context, row PackageSEO) (PackageSEO, error) {
	_, protected, err := s.ContentGlossary(ctx)
	if err != nil {
		return row, err
	}
	convert := func(value *string) (*string, error) {
		if value == nil {
			return nil, nil
		}
		text, err := traditional.Text(*value, protected)
		return &text, err
	}
	derived := row
	derived.Locale, derived.Derived = traditional.Locale, true
	if derived.Title, err = convert(row.Title); err != nil {
		return row, err
	}
	derived.Description, err = convert(row.Description)
	return derived, err
}

// SetPackageSEO applies a request to one language of a live cask or link app inside the caller's
// transaction and reports whether the stored override changed. Fields the request leaves out keep their
// value; clearing both deletes the override. A change refreshes the app's cached detail and goes to the
// URL change feed, so the overview's HTML and edge copies follow. owner is the write owner.
func (s *Store) SetPackageSEO(ctx context.Context, id int64, locale string, title, description SEOText, owner string) (bool, error) {
	if locale == traditional.Locale {
		return false, domain.Rejected("derived_locale_read_only:zh-Hant:zh-CN")
	}
	if !PackageSEOLocale(locale) {
		return false, domain.Rejected("locale_invalid:locale")
	}
	var p struct{ Kind, Token string }
	if err := s.DB.WithContext(ctx).Raw("SELECT kind,token FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL FOR UPDATE", id).Scan(&p).Error; err != nil {
		return false, err
	}
	if p.Token == "" {
		return false, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	current, err := s.PackageSEOOverride(ctx, id, locale)
	if err != nil {
		return false, err
	}
	next := PackageSEO{PackageID: id, Locale: locale}
	if current != nil {
		next.Title, next.Description = current.Title, current.Description
	}
	if title.Set {
		next.Title = title.Value
	}
	if description.Set {
		next.Description = description.Value
	}
	if next.Title == nil && next.Description == nil {
		result := s.DB.WithContext(ctx).Exec("DELETE FROM package_seo_overrides WHERE package_id=? AND locale=?", id, locale)
		if result.Error != nil || result.RowsAffected == 0 {
			return false, MapAdminError(result.Error)
		}
		return true, s.packagePageChanged(ctx, id, p.Kind, p.Token, "package-seo")
	}
	if current != nil && sameText(current.Title, next.Title) && sameText(current.Description, next.Description) {
		return false, nil
	}
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO package_seo_overrides(package_id,locale,title,description,updated_by) VALUES(?,?,?,?,?)
 ON CONFLICT(package_id,locale) DO UPDATE SET title=EXCLUDED.title,description=EXCLUDED.description,updated_at=now(),updated_by=EXCLUDED.updated_by`,
		id, locale, next.Title, next.Description, owner).Error; err != nil {
		return false, MapAdminError(err)
	}
	return true, s.packagePageChanged(ctx, id, p.Kind, p.Token, "package-seo")
}

func sameText(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
