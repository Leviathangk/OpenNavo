// Package content defines translatable content; SQL identifiers come only from this registry.
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Spec struct {
	Parent, Table, Key string
	Fields             map[string]int
	Long               map[string]bool
}

var Registry = map[string]Spec{
	"package":         {"packages", "package_i18n", "package_id", map[string]int{"display_name": 128, "summary": 200, "description": 20000}, map[string]bool{"description": true}},
	"release":         {"releases", "release_i18n", "release_id", map[string]int{"title": 256, "summary": 400, "sections": 2400, "body_markdown": 200000}, map[string]bool{"body_markdown": true}},
	"category":        {"categories", "category_i18n", "category_id", map[string]int{"name": 128, "description": 1000}, nil},
	"collection":      {"collections", "collection_i18n", "collection_id", map[string]int{"title": 200, "subtitle": 500, "body": 20000}, map[string]bool{"body": true}},
	"feature":         {"features", "feature_i18n", "feature_id", map[string]int{"badge": 80, "title": 200, "subtitle": 500, "body": 20000, "cta_label": 80}, map[string]bool{"body": true}},
	"screenshot":      {"package_screenshots", "screenshot_i18n", "screenshot_id", map[string]int{"caption": 1000}, nil},
	"collection_item": {"collection_items", "collection_item_i18n", "collection_id", map[string]int{"note": 2000}, nil},
	"desktop_release": {"desktop_releases", "desktop_release_i18n", "desktop_release_id", map[string]int{"notes": 20000}, map[string]bool{"notes": true}},
	"mirror":          {"mirrors", "mirror_i18n", "mirror_id", map[string]int{"name": 128}, nil},
	"announcement":    {"app_config", "announcement_i18n", "config_key", map[string]int{"title": 200, "body": 2000}, nil},
	// Articles translate per revision: the ID is the revision, and only the published revision of a
	// published article is translatable, so late jobs for an older revision change nothing.
	"article":        {"article_revisions", "article_revision_i18n", "revision_id", map[string]int{"title": 200, "summary": 400, "body_markdown": 100000, "test_note": 1000, "seo_title": 120, "seo_description": 300}, map[string]bool{"body_markdown": true}},
	"article_author": {"article_authors", "article_author_i18n", "author_id", map[string]int{"role": 80, "bio": 1000}, nil},
}

type Ref struct {
	Entity      string `json:"entity"`
	ID          int64  `json:"id"`
	SecondaryID int64  `json:"secondaryId,omitempty"`
}

func (r Ref) Key() string {
	if r.Entity == "about" {
		return "site.about"
	}
	if r.Entity == "announcement" {
		return "desktop.announcement"
	}
	if r.Entity == "collection_item" {
		return fmt.Sprintf("%d:%d", r.ID, r.SecondaryID)
	}
	return strconv.FormatInt(r.ID, 10)
}
func (r Ref) Valid() bool {
	if r.Entity == "about" {
		return r.ID == 0 && r.SecondaryID == 0
	}
	_, ok := Registry[r.Entity]
	return ok && (r.ID > 0 || r.Entity == "announcement") && (r.Entity != "collection_item" || r.SecondaryID > 0)
}
func Hash(locale string, fields map[string]any) string {
	raw, _ := json.Marshal(struct {
		Locale string
		Fields map[string]any
	}{locale, fields})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func Names() []string {
	out := make([]string, 0, len(Registry))
	for k := range Registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Preserve section counts, keys and array order; the model sees only string leaves.
func Flatten(fields map[string]any) map[string]string {
	out := map[string]string{}
	var walk func(string, any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				out[path] = v
			}
		case []any:
			for i, x := range v {
				walk(fmt.Sprintf("%s.%d", path, i), x)
			}
		case map[string]any:
			for k, x := range v {
				walk(path+"."+k, x)
			}
		}
	}
	for k, v := range fields {
		walk(k, v)
	}
	return out
}
func Replace(fields map[string]any, translated map[string]string) map[string]any {
	var walk func(string, any) any
	walk = func(path string, v any) any {
		if text, ok := translated[path]; ok {
			return text
		}
		switch v := v.(type) {
		case []any:
			out := make([]any, len(v))
			for i, x := range v {
				out[i] = walk(fmt.Sprintf("%s.%d", path, i), x)
			}
			return out
		case map[string]any:
			out := map[string]any{}
			for k, x := range v {
				out[k] = walk(path+"."+k, x)
			}
			return out
		}
		return v
	}
	out := map[string]any{}
	for k, v := range fields {
		out[k] = walk(k, v)
	}
	return out
}
