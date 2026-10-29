package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/facts"
)

// pageFields are the fields app pages show, or that decide what they show, of each object in
// pageStateInputs' overview. Only these reach the page hash, so a column or JSON key pages do not show can
// be added without moving any sitemap date. When pages start showing a field, add it here and bump
// pageHashVersion (dates stay unless pageChanges finds a real change); never add a field pages do not show.
var pageFields = map[string][]string{
	// package_meta: hidden and index_override decide indexing.
	"meta": {"developer", "repo_url", "hidden", "tags", "icon_asset_id", "accent_color", "index_override"},
	// package_i18n: the text shown, its language and whether it is marked machine-translated.
	"text": {"locale", "display_name", "summary", "description", "machine_translated"},
	// package_facts.price and .macos, the legacy facts: stale is derived from verifiedAt.
	"price": {"type", "amount", "currency", "region", "plan", "period", "sourceUrl", "status", "stale"},
	"macos": {"ranges", "appVersion", "sourceUrl", "status", "stale"},
	// link_apps, its latest GitHub release and the release files (arch and format pick the suitable ones).
	"link":    {"homepage", "download_url", "github_repo", "bundle_ids", "team_id", "pricing", "notes", "latest"},
	"release": {"tag", "version", "publishedAt", "url", "assets"},
	"asset":   {"name", "url", "size", "arch", "format"},
	// package_cve_checks and the vulnerabilities found.
	"security": {"product_cpe", "app_version", "matches"},
	"match":    {"id", "productCpe", "appVersion", "sourceUrl"},
	// package_fact_records: the value by kind; of the evidence, pages show its source and state (pageEvidence).
	"record":               {"kind", "key", "value", "evidence"},
	"record:price":         {"region", "edition", "period", "usage", "downloadFree", "amount", "currency", "purchaseUrl", "channel"},
	"record:security":      {"appVersion", "developer", "teamId", "channels", "openSource", "repositoryUrl", "productCpe"},
	"record:attribute":     {"name", "appVersion", "booleanValue", "formats"},
	"record:migration":     {"targetPackageId", "locale", "sourceVersion", "targetVersion", "steps"},
	"record:compatibility": {"appVersion", "minimumMacOS", "maximumMacOS", "appleSilicon"},
	"record:issue":         {"locale", "title", "symptom", "appVersions", "macosVersions", "steps", "communityLinks"},
	"record:integration":   {"kind", "name", "appVersion", "repositoryUrl", "version", "installation", "clients", "permissions", "appleSilicon", "versions"},
	// package_source_facts found rows: the Mac App Store listing and the GitHub repository.
	"source":           {"source", "data"},
	"source:app_store": {"name", "url", "languages", "price", "currency", "region"},
	"source:github":    {"repo", "url", "license", "archived"},
	// package_alternatives, with each alternative's name, version and facts.
	"alternative": {"relation", "name", "token", "version", "disabled", "facts", "records"},
	"relation":    {"alternative_id", "sort", "reasons", "limits", "best_for", "note"},
	// Category, collection and article cards: their links and the localized name or title shown.
	"category":        {"slug", "primary", "text"},
	"category:text":   {"name"},
	"collection":      {"slug", "text"},
	"collection:text": {"title"},
	"article":         {"slug", "type", "text"},
	"article:text":    {"title", "summary"},
}

// pageOverview keeps what the overview and details pages show of pageStateInputs' overview object.
func pageOverview(raw json.RawMessage, now time.Time) (map[string]any, error) {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	legacy := func(value any) any {
		f, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		return map[string]any{"price": legacyFact(f["price"], "price", now), "macos": legacyFact(f["macos"], "macos", now)}
	}
	card := func(name string) func(map[string]any) any {
		return func(c map[string]any) any {
			out := pick(c, pageFields[name])
			out["text"] = pickValue(c["text"], pageFields[name+":text"])
			return out
		}
	}
	return map[string]any{
		// app is listed column by column in pageStateInputs: the Homebrew definition pages show.
		"app": in["app"], "aliases": in["aliases"],
		"meta": pickValue(in["meta"], pageFields["meta"]), "text": pickValue(in["text"], pageFields["text"]),
		"facts": legacy(in["facts"]),
		"link":  pageLink(in["link"]),
		"security": each(in["security"], func(c map[string]any) any {
			out := pick(c, pageFields["security"])
			out["matches"] = each(c["matches"], func(m map[string]any) any { return pick(m, pageFields["match"]) })
			return out
		}),
		"records": each(in["records"], pageRecord),
		"sources": each(in["sources"], func(s map[string]any) any {
			out := pick(s, pageFields["source"])
			if source, _ := s["source"].(string); pageFields["source:"+source] != nil {
				out["data"] = pickValue(s["data"], pageFields["source:"+source])
			}
			return out
		}),
		"alternatives": each(in["alternatives"], func(a map[string]any) any {
			out := pick(a, pageFields["alternative"])
			out["relation"] = pickValue(a["relation"], pageFields["relation"])
			out["facts"] = legacy(a["facts"])
			out["records"] = each(a["records"], pageRecord)
			return out
		}),
		"categories":  each(in["categories"], card("category")),
		"collections": each(in["collections"], card("collection")),
		"articles":    each(in["articles"], card("article")),
	}, nil
}

// legacyFact keeps a package_facts price or macOS supplement as pages show it: a year after its verification
// it is shown as possibly outdated, which counts; the verification date itself does not.
func legacyFact(value any, name string, now time.Time) any {
	f, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if at, ok := f["verifiedAt"].(string); ok {
		if verified, err := time.Parse(time.RFC3339, at); err == nil {
			f["stale"] = facts.Stale(verified, now)
		}
	}
	return pick(f, pageFields[name])
}

func pageRecord(r map[string]any) any {
	out := pick(r, pageFields["record"])
	if kind, _ := r["kind"].(string); pageFields["record:"+kind] != nil {
		out["value"] = pickValue(r["value"], pageFields["record:"+kind])
	}
	return out
}

func pageLink(value any) any {
	l, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := pick(l, pageFields["link"])
	if latest, ok := l["latest"].(map[string]any); ok {
		release := pick(latest, pageFields["release"])
		release["assets"] = each(latest["assets"], func(a map[string]any) any { return pick(a, pageFields["asset"]) })
		out["latest"] = release
	}
	return out
}

// pick copies the listed keys an object has.
func pick(value map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if child, ok := value[key]; ok {
			out[key] = child
		}
	}
	return out
}

// pickValue is pick for a value that may be absent (null) or not an object, which stays as it is.
func pickValue(value any, keys []string) any {
	if object, ok := value.(map[string]any); ok {
		return pick(object, keys)
	}
	return value
}

// each maps the objects of a JSON array; anything else (null for no rows) stays as it is.
func each(value any, fn func(map[string]any) any) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]any, len(items))
	for i, item := range items {
		if object, ok := item.(map[string]any); ok {
			out[i] = fn(object)
		} else {
			out[i] = item
		}
	}
	return out
}

// pageMetadata are when, how often and by whom a value was stored, fetched, translated or checked. Pages show
// some check dates ("checked on …"), but a check that confirms the same content does not change what a page
// says, and sitemap dates follow that. Both spellings are stripped at any depth of the hashed input, with the
// category and collection page metadata.
var pageMetadata = func() map[string]bool {
	keys := map[string]bool{"etag": true, "revision": true, "verifier": true, "model": true, "seo_title": true, "seo_description": true}
	for _, key := range []string{"updated_at", "created_at", "updated_by", "created_by", "reviewed_by", "reviewed_at", "translated_at",
		"fetched_at", "synced_at", "last_error", "fail_reason", "source_hash", "body_hash", "revision_id", "next_check_at", "fail_count",
		"checked_at", "valid_until", "verified_at", "verified_by", "observed_at", "captured_at", "last_checked_at", "expires_at"} {
		keys[key] = true
		parts := strings.Split(key, "_")
		for i := 1; i < len(parts); i++ {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
		keys[strings.Join(parts, "")] = true
	}
	return keys
}()

// stableContent strips pageMetadata and reduces evidence to what pages show of it.
func stableContent(value any, now time.Time) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch {
			case pageMetadata[key]:
				delete(v, key)
			case key == "evidence":
				v[key] = pageEvidence(child, now)
			default:
				v[key] = stableContent(child, now)
			}
		}
	case []any:
		for i, child := range v {
			v[i] = stableContent(child, now)
		}
	}
	return value
}

// pageEvidence is what pages show of a fact's evidence: its source and its state now (verified, stale when its
// validity has passed, unknown when incomplete). Check times, excerpts and capture details are left out.
func pageEvidence(value any, now time.Time) any {
	object, ok := value.(map[string]any)
	if !ok {
		return stableContent(value, now)
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return nil
	}
	var proof facts.Proof
	if json.Unmarshal(raw, &proof) != nil {
		return stableContent(object, now)
	}
	proof = proof.At(now)
	return map[string]any{"sourceUrl": proof.SourceURL, "status": proof.Status}
}

func pageContentHash(value any, now time.Time) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	raw, err = json.Marshal(stableContent(decoded, now))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// pageChanges are the latest real changes an app's pages show in one language, from columns no check or sync
// rewrites: the version, the text shown, screenshots, and the releases and Homebrew versions of the timeline.
// Hashes of different pageHashVersions cannot be compared, so a page keeps its date across an upgrade unless
// one of these moved after it. Migration 00113 repaired dates with these columns (for all releases and
// versions of an app, not only those of the page), so this finds nothing later than a repaired date.
type pageChanges struct {
	version, text, screenshots, seo time.Time
	releases                        map[int64]time.Time
	versions                        map[string]time.Time
}

func (s *Store) pageChanges(ctx context.Context, id int64, locale string) (pageChanges, error) {
	var row struct{ Version, Text, Screenshots *time.Time }
	if err := s.DB.WithContext(ctx).Raw(`SELECT p.version_changed_at AS version,
 (SELECT t.updated_at FROM package_i18n t WHERE t.package_id=p.id AND t.locale IN (lang.requested,p.source_locale,'en-US') AND COALESCE(NULLIF(t.display_name,''),NULLIF(t.summary,''),NULLIF(t.description,'')) IS NOT NULL ORDER BY `+publicLocaleOrder+` LIMIT 1) AS text,
 (SELECT GREATEST(max(s.created_at),max(t.updated_at)) FROM package_screenshots s LEFT JOIN screenshot_i18n t ON t.screenshot_id=s.id AND t.locale IN (lang.requested,s.source_locale,'en-US') WHERE s.package_id=p.id) AS screenshots
 FROM packages p CROSS JOIN (SELECT ?::text requested) lang WHERE p.id=?`, locale, id).Scan(&row).Error; err != nil {
		return pageChanges{}, err
	}
	changes := pageChanges{releases: map[int64]time.Time{}, versions: map[string]time.Time{}}
	if row.Version != nil {
		changes.version = *row.Version
	}
	if row.Text != nil {
		changes.text = *row.Text
	}
	if row.Screenshots != nil {
		changes.screenshots = *row.Screenshots
	}
	var releases []struct {
		ID int64
		At time.Time
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT r.id,GREATEST(r.updated_at,max(t.updated_at)) AS at FROM releases r
 LEFT JOIN release_i18n t ON t.release_id=r.id AND t.locale IN (?,r.source_locale,'en-US') WHERE r.package_id=? GROUP BY r.id`, locale, id).Scan(&releases).Error; err != nil {
		return pageChanges{}, err
	}
	for _, r := range releases {
		changes.releases[r.ID] = r.At
	}
	var versions []struct {
		Version   string
		CreatedAt time.Time
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT version,created_at FROM package_versions WHERE package_id=?", id).Scan(&versions).Error; err != nil {
		return pageChanges{}, err
	}
	for _, v := range versions {
		changes.versions[v.Version] = v.CreatedAt
	}
	return changes, nil
}

// at is the latest change a page of the kind shows, or nil without any.
func (c pageChanges) at(kind string, entries []publicapi.ReleaseEntry) *time.Time {
	latest := c.version
	later := func(at time.Time) {
		if at.After(latest) {
			latest = at
		}
	}
	if kind == "overview" || kind == "details" {
		later(c.text)
	}
	if kind == "overview" {
		later(c.screenshots)
		later(c.seo)
	}
	if kind == "overview" || kind == "versions" {
		for _, entry := range entries {
			if entry.Id != nil {
				later(c.releases[*entry.Id])
			}
			later(c.versions[entry.Version])
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}
