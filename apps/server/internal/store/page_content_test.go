package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/stretchr/testify/require"
)

// pageOverviewFixture is an overview object as pageStateInputs reads it, with its rows whole.
func pageOverviewFixture() map[string]any {
	evidence := func() map[string]any {
		return map[string]any{"sourceUrl": "https://vendor.example/pricing", "status": "verified", "checkedAt": "2026-10-20T08:00:00Z", "validUntil": "2026-11-19T08:00:00Z", "excerpt": "Pro costs 9.99"}
	}
	price := func() map[string]any {
		return map[string]any{"type": "subscription", "amount": 9.99, "currency": "USD", "region": "US", "plan": "Pro", "period": "month", "appVersion": "5.2",
			"sourceUrl": "https://vendor.example/pricing", "method": "vendor_site", "verifiedAt": "2026-10-20T08:00:00Z", "verifiedBy": "editor", "verifier": "admin", "status": "verified", "note": nil}
	}
	record := func() map[string]any {
		return map[string]any{"id": 7, "package_id": 1, "kind": "price", "key": "pro-us", "updated_at": "2026-10-20T08:00:00Z", "evidence": evidence(),
			"value": map[string]any{"region": "US", "edition": "Pro", "period": "monthly", "usage": "subscription", "downloadFree": true, "amount": "9.99", "currency": "USD", "purchaseUrl": "https://vendor.example/buy", "channel": "vendor"}}
	}
	return map[string]any{
		"app":  map[string]any{"kind": "cask", "token": "example", "name": []any{"Example"}, "version": "5.2"},
		"meta": map[string]any{"package_id": 1, "developer": "Example Inc.", "repo_url": nil, "hidden": false, "editor_choice": false, "tags": []any{}, "icon_asset_id": 3, "icon_source": "manual", "accent_color": "#336699", "notes": "internal", "updated_by": 1, "updated_at": "2026-10-20T08:00:00Z", "changelog_excluded": false, "index_override": "auto"},
		"text": map[string]any{"package_id": 1, "locale": "en-US", "display_name": "Example", "summary": "An editor", "description": "A useful editor", "status": "source", "source": "human", "source_hash": "a", "model": nil,
			"reviewed_by": nil, "reviewed_at": nil, "updated_at": "2026-10-20T08:00:00Z", "source_locale": "en-US", "translated_at": nil, "machine_translated": false, "fail_reason": nil},
		"facts": map[string]any{"package_id": 1, "price": price(), "macos": nil, "updated_at": "2026-10-20T08:00:00Z", "updated_by": 1},
		"link":  nil,
		"security": []any{map[string]any{"package_id": 1, "product_cpe": "cpe:2.3:a:example:example", "app_version": "5.2", "matches": []any{},
			"checked_at": "2026-10-25T00:00:00Z", "valid_until": "2026-10-26T00:00:00Z"}},
		"records": []any{record()},
		"sources": []any{map[string]any{"package_id": 1, "source": "app_store", "input": "com.example", "subject": "123", "status": "found", "etag": "x", "checked_at": "2026-10-25T00:00:00Z", "next_check_at": "2026-10-26T00:00:00Z", "fail_count": 0,
			"data": map[string]any{"name": "Example", "url": "https://apps.apple.com/app/id123", "bundleId": "com.example", "price": 4.99, "currency": "USD", "region": "US", "languages": []any{"EN"}, "seller": "Example Inc."}}},
		"alternatives": []any{map[string]any{"relation": map[string]any{"package_id": 1, "alternative_id": 2, "sort": 0, "reasons": []any{"similar"}, "limits": []any{}, "best_for": nil, "note": nil, "source": "agent"},
			"name": []any{"Other"}, "token": "other", "version": "1.0", "disabled": false, "facts": map[string]any{"price": price(), "macos": nil}, "records": []any{record()}}},
		"aliases":     nil,
		"categories":  []any{map[string]any{"slug": "editors", "primary": true, "text": map[string]any{"category_id": 4, "locale": "en-US", "name": "Editors", "description": "Text editors", "seo_title": "Editors for Mac", "model": nil, "updated_at": "2026-10-20T08:00:00Z"}}},
		"collections": nil,
		"articles":    nil,
	}
}

func pageOverviewHash(t *testing.T, overview map[string]any, now time.Time) string {
	t.Helper()
	raw, err := json.Marshal(overview)
	require.NoError(t, err)
	shown, err := pageOverview(raw, now)
	require.NoError(t, err)
	hash, err := pageContentHash(shown, now)
	require.NoError(t, err)
	return hash
}

func TestPageHashFollowsShownFieldsOnly(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	base := pageOverviewHash(t, pageOverviewFixture(), now)
	facts := func(o map[string]any) map[string]any { return o["facts"].(map[string]any)["price"].(map[string]any) }
	record := func(o map[string]any) map[string]any { return o["records"].([]any)[0].(map[string]any) }
	evidence := func(o map[string]any) map[string]any { return record(o)["evidence"].(map[string]any) }

	// A re-check or re-verification that confirms the same content, and fields or columns pages do not show.
	for name, change := range map[string]func(map[string]any){
		"price verified again": func(o map[string]any) {
			facts(o)["verifiedAt"], facts(o)["verifiedBy"], facts(o)["verifier"] = "2026-10-25T04:00:00Z", "agent:3:7", "agent"
		},
		"record checked again": func(o map[string]any) {
			evidence(o)["checkedAt"], evidence(o)["validUntil"], evidence(o)["excerpt"] = "2026-10-25T04:00:00Z", "2026-11-24T04:00:00Z", "Pro: 9.99"
			record(o)["updated_at"] = "2026-10-25T04:00:00Z"
		},
		"source checked again": func(o map[string]any) {
			source := o["sources"].([]any)[0].(map[string]any)
			source["checked_at"], source["etag"], source["data"].(map[string]any)["seller"] = "2026-10-26T00:00:00Z", "y", "Example Ltd."
		},
		"translation model": func(o map[string]any) { o["text"].(map[string]any)["model"] = "gpt-6-astra" },
		"internal fields": func(o map[string]any) {
			o["meta"].(map[string]any)["notes"], o["meta"].(map[string]any)["editor_choice"] = "changed", true
		},
		"new column": func(o map[string]any) {
			o["meta"].(map[string]any)["added_later"] = "x"
			o["text"].(map[string]any)["added_later"] = 1
		},
		"new fact key": func(o map[string]any) {
			facts(o)["reviewNote"] = "x"
			record(o)["value"].(map[string]any)["internal"] = true
		},
		"category page metadata": func(o map[string]any) {
			o["categories"].([]any)[0].(map[string]any)["text"].(map[string]any)["seo_title"] = "Best editors"
		},
	} {
		o := pageOverviewFixture()
		change(o)
		require.Equal(t, base, pageOverviewHash(t, o, now), name)
	}

	// What pages show, including the state of the evidence.
	for name, change := range map[string]func(map[string]any){
		"price amount":        func(o map[string]any) { facts(o)["amount"] = 12.99 },
		"price period":        func(o map[string]any) { facts(o)["period"] = "year" },
		"price record amount": func(o map[string]any) { record(o)["value"].(map[string]any)["amount"] = "12.99" },
		"price record region": func(o map[string]any) { record(o)["value"].(map[string]any)["region"] = "DE" },
		"evidence status":     func(o map[string]any) { evidence(o)["status"] = "needs_review" },
		"evidence expired":    func(o map[string]any) { evidence(o)["validUntil"] = "2026-10-25T08:00:00Z" },
		"evidence source":     func(o map[string]any) { evidence(o)["sourceUrl"] = "https://vendor.example/plans" },
		"legacy price stale":  func(o map[string]any) { facts(o)["verifiedAt"] = "2025-10-01T08:00:00Z" },
		"developer":           func(o map[string]any) { o["meta"].(map[string]any)["developer"] = "Example GmbH" },
		"description":         func(o map[string]any) { o["text"].(map[string]any)["description"] = "A faster editor" },
		"machine translated":  func(o map[string]any) { o["text"].(map[string]any)["machine_translated"] = true },
		"App Store price": func(o map[string]any) {
			o["sources"].([]any)[0].(map[string]any)["data"].(map[string]any)["price"] = 5.99
		},
		"vulnerability found": func(o map[string]any) {
			o["security"].([]any)[0].(map[string]any)["matches"] = []any{map[string]any{"id": "CVE-2026-0001"}}
		},
		"alternative price": func(o map[string]any) {
			o["alternatives"].([]any)[0].(map[string]any)["facts"].(map[string]any)["price"].(map[string]any)["amount"] = 1.99
		},
		"category name": func(o map[string]any) {
			o["categories"].([]any)[0].(map[string]any)["text"].(map[string]any)["name"] = "Code editors"
		},
		"alternative relation": func(o map[string]any) {
			o["alternatives"].([]any)[0].(map[string]any)["relation"].(map[string]any)["sort"] = 1
		},
	} {
		o := pageOverviewFixture()
		change(o)
		require.NotEqual(t, base, pageOverviewHash(t, o, now), name)
	}
}

func TestPageHashIgnoresVolatileGitHubSignals(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	withGitHub := func(stars int64, pushedAt string, archived bool) map[string]any {
		o := pageOverviewFixture()
		o["sources"] = append(o["sources"].([]any), map[string]any{
			"source": "github",
			"data": map[string]any{
				"repo": "owner/example", "url": "https://github.com/owner/example", "license": "MIT",
				"archived": archived, "stars": stars, "pushedAt": pushedAt,
			},
		})
		return o
	}
	base := pageOverviewHash(t, withGitHub(100, "2026-10-29T00:00:00Z", false), now)
	require.Equal(t, base, pageOverviewHash(t, withGitHub(200, "2026-10-30T00:00:00Z", false), now))
	require.NotEqual(t, base, pageOverviewHash(t, withGitHub(100, "2026-10-29T00:00:00Z", true), now))
}

func TestStableContentStripsMetadataAtAnyDepth(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	for _, key := range []string{"verifiedAt", "verified_at", "verifiedBy", "verified_by", "verifier", "checkedAt", "checked_at", "validUntil", "valid_until",
		"observedAt", "observed_at", "fetchedAt", "fetched_at", "capturedAt", "captured_at", "updatedAt", "updated_at", "createdAt", "created_at",
		"lastCheckedAt", "last_checked_at", "nextCheckAt", "next_check_at", "expiresAt", "expires_at", "translatedAt", "model", "etag"} {
		value := map[string]any{"entries": []any{map[string]any{"version": "1", key: "x"}}}
		require.Equal(t, map[string]any{"entries": []any{map[string]any{"version": "1"}}}, stableContent(value, now), key)
	}
	// Release and integration dates are content.
	value := map[string]any{"publishedAt": "2026-10-01T00:00:00Z", "brewCommittedAt": "2026-10-01T00:00:00Z", "releasedAt": "2026-10-01T00:00:00Z"}
	require.Len(t, stableContent(value, now), 3)
	// Evidence keeps its source and its state now.
	evidence := map[string]any{"evidence": map[string]any{"sourceUrl": "https://vendor.example/docs", "status": "verified", "checkedAt": "2026-10-01T00:00:00Z", "validUntil": "2026-10-20T00:00:00Z", "excerpt": "text", "fetchedAt": "2026-10-01T00:00:00Z"}}
	require.Equal(t, map[string]any{"evidence": map[string]any{"sourceUrl": "https://vendor.example/docs", "status": "stale"}}, stableContent(evidence, now))
}

func TestPageChangesByPageKind(t *testing.T) {
	at := func(day int) time.Time { return time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC) }
	release, other := int64(7), int64(8)
	changes := pageChanges{version: at(1), text: at(2), screenshots: at(3), releases: map[int64]time.Time{release: at(4), other: at(9)}, versions: map[string]time.Time{"2.0": at(5)}}
	latest := []publicapi.ReleaseEntry{{Id: &release, Version: "2.0"}}
	require.Equal(t, at(5), *changes.at("overview", latest))
	require.Equal(t, at(2), *changes.at("details", nil))
	require.Equal(t, at(1), *changes.at("dependencies", nil))
	require.Equal(t, at(5), *changes.at("versions", latest), "a release on another page does not count")
	require.Nil(t, pageChanges{}.at("details", nil))
}
