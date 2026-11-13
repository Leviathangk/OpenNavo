//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
)

// A `new` submission becomes a real link package on publish: catalog row, i18n
// text, curated meta, location and Discover pick all land together.
func TestDeveloperPublishCreatesLinkPackageAndPick(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)

	payload := json.RawMessage(`{
		"name": "Hyperfocus Timer",
		"summary": "A tiny focus timer for macOS.",
		"description": "Focus sessions with a menu bar timer.",
		"developer": "Example Studio",
		"homepage": "https://example.com/hyperfocus",
		"repoUrl": "https://github.com/example/hyperfocus",
		"downloadUrl": "https://example.com/hyperfocus/download.dmg",
		"iconUrl": "https://example.com/icon.png",
		"countryCode": "US",
		"city": "Seattle",
		"cityKey": "seattle",
		"region": "americas",
		"lat": 47.6062,
		"lng": -122.3321,
		"remote": true
	}`)
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(slug, icon) VALUES('productivity','lucide:timer')`).Error)
	row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind:         "new",
		SourceKind:   "github",
		SourceURL:    "https://github.com/example/hyperfocus",
		Payload:      payload,
		ContactEmail: "dev@example.com",
	})
	require.NoError(t, err)
	require.NotZero(t, row.ID)
	require.Len(t, row.PublicToken, 32, "receipt token is a random 32-char hex string")

	found, err := st.DeveloperSubmissionByToken(ctx, row.PublicToken)
	require.NoError(t, err)
	require.Equal(t, row.ID, found.ID)

	parsed := store.ParseDeveloperPayload(row.Payload)
	parsed.SourceURL = row.SourceURL
	packageID, err := st.CreateLinkPackageFromPayload(ctx, parsed)
	require.NoError(t, err)

	pkg, err := st.PublicPackage(ctx, "link", "hyperfocus-timer", "en-US")
	require.NoError(t, err)
	require.Equal(t, "Hyperfocus Timer", pkg.Name)
	require.NotNil(t, pkg.I18n.Summary)
	require.Equal(t, "A tiny focus timer for macOS.", *pkg.I18n.Summary)
	require.NotNil(t, pkg.Meta.Developer)
	require.Equal(t, "Example Studio", *pkg.Meta.Developer)
	require.Equal(t, "https://example.com/icon.png", *pkg.IconURL)

	require.NoError(t, st.UpsertPackageLocation(ctx, packageID, "US", "Seattle", "seattle", "americas", 47.6062, -122.3321, true, "self", "approved", "self_reported", "", nil))
	require.NoError(t, st.SetDeveloperPick(ctx, packageID, &row.ID, "A calm timer that stays out of the way.", 1, true))

	picks, err := st.DeveloperPicks(ctx, 4)
	require.NoError(t, err)
	require.NotEmpty(t, picks)
	require.Equal(t, packageID, picks[0].PackageID)

	note := "Approved after a quick look."
	updated, err := st.UpdateDeveloperSubmission(ctx, row.ID, "published", &note, row.Payload, &packageID, nil)
	require.NoError(t, err)
	require.Equal(t, "published", updated.Status)
	require.Equal(t, packageID, *updated.PackageID)

	require.NoError(t, st.AppendDeveloperSubmissionEvent(ctx, row.ID, "published", &note, "admin"))
	events, err := st.DeveloperSubmissionEvents(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "published", events[0].Status)

}

// Claim updates land on the existing package and never touch Homebrew-owned versions.
func TestDeveloperPublishAppliesClaimToExistingPackage(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)

	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,names,desc_en,version,version_base,raw,raw_hash,source_locale)
VALUES(1201,'cask','ghostty','ghostty','homebrew/cask','Ghostty',ARRAY['Ghostty'],'old summary','1.2.0','1.2.0','{}','fixture','en-US')`).Error)

	payload := store.ParseDeveloperPayload(json.RawMessage(`{"summary":"Fast, native terminal for macOS.","developer":"Ghostty","version":"9.9.9"}`))
	require.NoError(t, st.ApplyDeveloperPayload(ctx, 1201, payload))

	pkg, err := st.PublicPackage(ctx, "cask", "ghostty", "en-US")
	require.NoError(t, err)
	require.NotNil(t, pkg.I18n.Summary)
	require.Equal(t, "Fast, native terminal for macOS.", *pkg.I18n.Summary)
	require.NotNil(t, pkg.Meta.Developer)
	require.Equal(t, "Ghostty", *pkg.Meta.Developer)
	require.Equal(t, "1.2.0", pkg.Version, "a cask keeps its Homebrew version")
}

// Rejected leads and old timeline rows age out; active submissions stay.
func TestDeveloperRetentionPrunesRejectedLeadsAndOldEvents(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)

	rejected, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "new", SourceURL: "https://example.com/rejected", ContactEmail: "rejected@example.com",
	})
	require.NoError(t, err)
	kept, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "new", SourceURL: "https://example.com/kept", ContactEmail: "kept@example.com",
	})
	require.NoError(t, err)

	require.NoError(t, st.DB.Exec(`UPDATE developer_submissions SET status='rejected', updated_at=now()-interval '400 days' WHERE id=?`, rejected.ID).Error)
	require.NoError(t, st.DB.Exec(`UPDATE developer_submissions SET updated_at=now()-interval '400 days' WHERE id=?`, kept.ID).Error)
	require.NoError(t, st.AppendDeveloperSubmissionEvent(ctx, rejected.ID, "rejected", nil, "admin"))
	require.NoError(t, st.AppendDeveloperSubmissionEvent(ctx, kept.ID, "submitted", nil, "developer"))
	require.NoError(t, st.DB.Exec(`UPDATE developer_submission_events SET created_at=now()-interval '400 days'`).Error)

	before := time.Now().Add(-store.DeveloperRetention)
	removed, err := st.PruneDeveloperSubmissions(ctx, before)
	require.NoError(t, err)
	require.EqualValues(t, 1, removed)

	events, err := st.PruneDeveloperSubmissionEvents(ctx, before)
	require.NoError(t, err)
	require.EqualValues(t, 1, events)

	remaining, err := st.DeveloperSubmission(ctx, kept.ID)
	require.NoError(t, err)
	require.Equal(t, kept.ID, remaining.ID)
	gone, err := st.DeveloperSubmission(ctx, rejected.ID)
	require.NoError(t, err)
	require.Zero(t, gone.ID)
}

// Publishing is one transaction: a bad city or invalid metadata leaves no package.
func TestPublishDeveloperSubmissionIsAtomic(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(slug, icon) VALUES('productivity','lucide:timer')`).Error)

	payload := json.RawMessage(`{
		"name": "Orbit Notes",
		"summary": "Notes that orbit your menu bar.",
		"developer": "Orbit Labs",
		"category": "productivity",
		"homepage": "https://example.com/orbit",
		"downloadUrl": "https://example.com/orbit.dmg",
		"countryCode": "DE",
		"city": "Berlin"
	}`)
	row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "new", SourceURL: "https://example.com/orbit", Payload: payload, ContactEmail: "orbit@example.com",
	})
	require.NoError(t, err)

	feature := true
	published, err := st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, Feature: &feature, Quote: "Lighter notes.", Sort: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, published.PackageID)
	require.Equal(t, "published", published.Status)

	var location struct {
		CityKey string
		Region  string
		Lat     float64
	}
	require.NoError(t, st.DB.Raw(`SELECT city_key, region, lat FROM package_locations WHERE package_id = ?`, *published.PackageID).Scan(&location).Error)
	require.Equal(t, "berlin", location.CityKey)
	require.Equal(t, "europe", location.Region)
	require.InDelta(t, 52.52, location.Lat, 0.01)

	picks, err := st.DeveloperPicks(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, *published.PackageID, picks[0].PackageID)

	// Publish, location and pick writes queue public cache invalidation in the same transaction.
	var invalidations []string
	require.NoError(t, st.DB.Raw(`SELECT payload::text FROM job_outbox WHERE job_type='cache:invalidate' AND unique_key LIKE 'cache:invalidate:developer:%'`).Scan(&invalidations).Error)
	joined := strings.Join(invalidations, "\n")
	require.Contains(t, joined, "c:developer-map:*")
	require.Contains(t, joined, "c:developer-picks:*")
	require.Contains(t, joined, "c:pkg:*")

	// An unknown city fails validation and must not leave a package behind.
	bad := json.RawMessage(`{"name":"Nowhere App","summary":"x","developer":"x","homepage":"https://example.com/n","countryCode":"US","city":"Nowhere City"}`)
	badRow, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "new", SourceURL: "https://example.com/nowhere", Payload: bad, ContactEmail: "nowhere@example.com",
	})
	require.NoError(t, err)
	var before int64
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM packages WHERE kind='link'`).Scan(&before).Error)
	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{SubmissionID: badRow.ID, Status: "published", Payload: bad})
	require.Error(t, err)
	var after int64
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM packages WHERE kind='link'`).Scan(&after).Error)
	require.Equal(t, before, after)

	// A claim that turns location updates off must not overwrite the existing point.
	claim := json.RawMessage(`{"updateLocation":false,"countryCode":"US","city":"Seattle","updateSummary":false,"updateIcon":false}`)
	claimRow, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "claim", PackageID: published.PackageID, SourceURL: "https://example.com/orbit", Payload: claim, ContactEmail: "orbit@example.com",
	})
	require.NoError(t, err)
	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{SubmissionID: claimRow.ID, Status: "published", Payload: claim})
	require.NoError(t, err)
	var cityKey string
	require.NoError(t, st.DB.Raw(`SELECT city_key FROM package_locations WHERE package_id = ?`, *published.PackageID).Scan(&cityKey).Error)
	require.Equal(t, "berlin", cityKey)
}

// A submission that names only a country belongs on that country's tab; without a
// region it would only ever show under "other" and miss the region filters.
func TestPublishDeveloperSubmissionCountryOnlyRegion(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)

	for _, tc := range []struct{ code, want string }{
		{"US", "americas"},
		{"DE", "europe"},
		{"JP", "apac"},
		{"ZZ", "other"},
	} {
		token := strings.ToLower(tc.code)
		payload := json.RawMessage(`{"name":"Region App ` + tc.code + `","summary":"x","developer":"x","homepage":"https://example.com/region-` + token + `","countryCode":"` + tc.code + `"}`)
		row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
			Kind: "new", SourceURL: "https://example.com/region-" + token, Payload: payload, ContactEmail: "region@example.com",
		})
		require.NoError(t, err)

		published, err := st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{SubmissionID: row.ID, Status: "published", Payload: payload})
		require.NoError(t, err)
		require.NotNil(t, published.PackageID)

		var location struct {
			CityKey string
			Region  string
		}
		require.NoError(t, st.DB.Raw(`SELECT COALESCE(city_key, '') AS city_key, region FROM package_locations WHERE package_id = ?`, *published.PackageID).Scan(&location).Error)
		require.Empty(t, location.CityKey, "a country-only submission has no city")
		require.Equal(t, tc.want, location.Region, tc.code)
	}
}

// Unpublishing takes the Discover pick, the map point and a `new` submission's link
// package down together, and publishing again restores the same token and pick.
func TestWithdrawDeveloperSubmissionTakesPublicSurfaceDown(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)

	payload := json.RawMessage(`{
		"name": "Orbit Notes",
		"summary": "Notes that orbit your menu bar.",
		"developer": "Orbit Labs",
		"homepage": "https://example.com/orbit",
		"countryCode": "DE",
		"city": "Berlin"
	}`)
	row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "new", SourceURL: "https://example.com/orbit", Payload: payload, ContactEmail: "orbit@example.com",
	})
	require.NoError(t, err)

	feature := true
	published, err := st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, Feature: &feature, Quote: "Lighter notes.", Sort: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, published.PackageID)
	packageID := *published.PackageID

	kind, token, _, ok, err := st.DeveloperPackageRef(ctx, packageID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "link", kind)

	note := "Needs changes before it goes live."
	withdrawn, err := st.ReviewDeveloperSubmission(ctx, store.DeveloperReviewInput{SubmissionID: row.ID, Status: "changes_requested", Note: &note})
	require.NoError(t, err)
	require.Equal(t, "changes_requested", withdrawn.Status)

	var pickActive bool
	require.NoError(t, st.DB.Raw(`SELECT COALESCE(bool_or(active), FALSE) FROM developer_picks WHERE package_id = ?`, packageID).Scan(&pickActive).Error)
	require.False(t, pickActive, "the Discover pick stops showing")

	var locationStatus string
	require.NoError(t, st.DB.Raw(`SELECT status FROM package_locations WHERE package_id = ?`, packageID).Scan(&locationStatus).Error)
	require.Equal(t, "rejected", locationStatus, "the map point leaves the approved set")

	_, err = st.PublicPackage(ctx, "link", token, "en-US")
	require.Error(t, err, "the app page is gone")

	events, err := st.DeveloperSubmissionEvents(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, events, 2, "publish and withdrawal each add one timeline row")
	require.Equal(t, "changes_requested", events[1].Status)

	republished, err := st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Note: &note, Payload: payload, PackageID: &packageID,
		Feature: &feature, Quote: "Lighter notes.", Sort: 1,
	})
	require.NoError(t, err)
	require.Equal(t, packageID, *republished.PackageID)

	_, restoredToken, _, ok, err := st.DeveloperPackageRef(ctx, packageID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, token, restoredToken, "the public URL stays stable")

	pkg, err := st.PublicPackage(ctx, "link", token, "en-US")
	require.NoError(t, err)
	require.Equal(t, packageID, pkg.ID)

	require.NoError(t, st.DB.Raw(`SELECT active FROM developer_picks WHERE package_id = ?`, packageID).Scan(&pickActive).Error)
	require.True(t, pickActive)
}

// Turning the pick switch off retires that submission's pick, and rejecting a claim
// never takes the Homebrew cask itself down.
func TestReviewDeveloperSubmissionKeepsClaimPackageLive(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,names,desc_en,version,version_base,raw,raw_hash,source_locale)
VALUES(1201,'cask','ghostty','ghostty','homebrew/cask','Ghostty',ARRAY['Ghostty'],'old summary','1.2.0','1.2.0','{}','fixture','en-US')`).Error)

	payload := json.RawMessage(`{"summary":"Fast, native terminal for macOS.","developer":"Ghostty","countryCode":"GB","city":"London"}`)
	packageID := int64(1201)
	row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "claim", PackageID: &packageID, SourceURL: "https://ghostty.org", Payload: payload, ContactEmail: "dev@example.com",
	})
	require.NoError(t, err)

	feature := true
	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, Feature: &feature, Quote: "A calm terminal.", Sort: 1,
	})
	require.NoError(t, err)

	off := false
	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, Feature: &off,
	})
	require.NoError(t, err)
	var pickActive bool
	require.NoError(t, st.DB.Raw(`SELECT COALESCE(bool_or(active), FALSE) FROM developer_picks WHERE package_id = ?`, packageID).Scan(&pickActive).Error)
	require.False(t, pickActive, "an explicit off retires the pick")

	note := "The location looks wrong."
	_, err = st.ReviewDeveloperSubmission(ctx, store.DeveloperReviewInput{SubmissionID: row.ID, Status: "rejected", Note: &note})
	require.NoError(t, err)

	var removed bool
	require.NoError(t, st.DB.Raw(`SELECT removed_at IS NOT NULL FROM packages WHERE id = ?`, packageID).Scan(&removed).Error)
	require.False(t, removed, "a claim never takes the Homebrew cask down")

	var locationStatus string
	require.NoError(t, st.DB.Raw(`SELECT status FROM package_locations WHERE package_id = ?`, packageID).Scan(&locationStatus).Error)
	require.Equal(t, "rejected", locationStatus)
}

// Payload problems an editor can fix name their cause, so the drawer can show it.
func TestPublishDeveloperSubmissionNamesTheRejection(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(slug, icon) VALUES('productivity','lucide:timer')`).Error)

	cases := []struct{ name, payload, want string }{
		{"unknown category", `{"name":"Unknown Category","homepage":"https://example.com/unknown-category","category":"not-a-category"}`, "category_not_found"},
		{"unknown city", `{"name":"Unknown City","homepage":"https://example.com/unknown-city","countryCode":"US","city":"Nowhere City"}`, "city_not_found"},
		{"plain http homepage", `{"name":"Insecure Homepage","homepage":"http://example.com/insecure"}`, "url_invalid:homepage"},
	}
	for _, tc := range cases {
		payload := json.RawMessage(tc.payload)
		row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
			Kind: "new", SourceURL: "https://example.com/lead", Payload: payload, ContactEmail: "lead@example.com",
		})
		require.NoError(t, err)

		_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{SubmissionID: row.ID, Status: "published", Payload: payload})
		var appErr *domain.AppError
		require.ErrorAs(t, err, &appErr, tc.name)
		require.Equal(t, domain.CodeValidation, appErr.Code, tc.name)
		require.Contains(t, appErr.Msg, tc.want, tc.name)
	}

	// A claim updates an existing package, so the curated fields are checked by the
	// payload writer instead of the link validation.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,names,desc_en,version,version_base,raw,raw_hash,source_locale)
VALUES(1301,'cask','insecure-home','insecure-home','homebrew/cask','Insecure Home',ARRAY['Insecure Home'],'x summary','1.0.0','1.0.0','{}','fixture','en-US')`).Error)
	claimPackageID := int64(1301)
	claim := json.RawMessage(`{"homepage":"http://example.com/insecure-home"}`)
	claimRow, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "claim", PackageID: &claimPackageID, SourceURL: "https://example.com/insecure-home", Payload: claim, ContactEmail: "claim@example.com",
	})
	require.NoError(t, err)

	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{SubmissionID: claimRow.ID, Status: "published", Payload: claim})
	var claimErr *domain.AppError
	require.ErrorAs(t, err, &claimErr)
	require.Contains(t, claimErr.Msg, "homepage_not_https")
}

// A claim that never turned updateLocation on must not withdraw the editor's point
// when it is rejected: publishing never wrote it either.
func TestReviewDeveloperSubmissionLeavesUntouchedClaimLocation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,names,desc_en,version,version_base,raw,raw_hash,source_locale)
VALUES(1401,'cask','curated-claim','curated-claim','homebrew/cask','Curated Claim',ARRAY['Curated Claim'],'x summary','1.0.0','1.0.0','{}','fixture','en-US')`).Error)
	// An editor already approved this app's point, from the admin UI.
	require.NoError(t, st.UpsertPackageLocation(ctx, 1401, "US", "Seattle", "seattle", "americas", 47.6062, -122.3321, false, "admin", "approved", "verified", "", nil))

	packageID := int64(1401)
	payload := json.RawMessage(`{"summary":"A curated claim.","updateLocation":false,"countryCode":"US","city":"Austin"}`)
	row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "claim", PackageID: &packageID, SourceURL: "https://example.com/claim", Payload: payload, ContactEmail: "claim@example.com",
	})
	require.NoError(t, err)

	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{SubmissionID: row.ID, Status: "published", Payload: payload})
	require.NoError(t, err)

	note := "Not the right fit."
	_, err = st.ReviewDeveloperSubmission(ctx, store.DeveloperReviewInput{SubmissionID: row.ID, Status: "rejected", Note: &note})
	require.NoError(t, err)

	var location struct {
		CityKey string
		Status  string
	}
	require.NoError(t, st.DB.Raw(`SELECT COALESCE(city_key, '') AS city_key, status FROM package_locations WHERE package_id = ?`, packageID).Scan(&location).Error)
	require.Equal(t, "seattle", location.CityKey)
	require.Equal(t, "approved", location.Status, "a claim that never touched the location cannot withdraw it")
}

// Re-saving a published submission with the same pick must not reset published_at,
// which would push it ahead of picks that share its sort.
func TestPublishDeveloperSubmissionKeepsPickTimestamp(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)

	payload := json.RawMessage(`{"name":"Steady Pick","homepage":"https://example.com/steady-pick","countryCode":"FR","city":"Paris"}`)
	row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "new", SourceURL: "https://example.com/steady-pick", Payload: payload, ContactEmail: "steady@example.com",
	})
	require.NoError(t, err)

	feature := true
	published, err := st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, Feature: &feature, Quote: "Steady pick.", Sort: 1,
	})
	require.NoError(t, err)
	packageID := *published.PackageID

	// Age the pick so a rewrite is visible.
	require.NoError(t, st.DB.Exec(`UPDATE developer_picks SET published_at = now() - interval '1 hour' WHERE package_id = ?`, packageID).Error)
	var before time.Time
	require.NoError(t, st.DB.Raw(`SELECT published_at FROM developer_picks WHERE package_id = ?`, packageID).Scan(&before).Error)

	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, PackageID: &packageID,
		Feature: &feature, Quote: "Steady pick.", Sort: 1,
	})
	require.NoError(t, err)
	var after time.Time
	require.NoError(t, st.DB.Raw(`SELECT published_at FROM developer_picks WHERE package_id = ?`, packageID).Scan(&after).Error)
	require.True(t, before.Equal(after), "an unchanged pick keeps its published_at")

	// A real edit still moves it to the front of its sort.
	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, PackageID: &packageID,
		Feature: &feature, Quote: "A fresh quote.", Sort: 1,
	})
	require.NoError(t, err)
	var edited time.Time
	require.NoError(t, st.DB.Raw(`SELECT published_at FROM developer_picks WHERE package_id = ?`, packageID).Scan(&edited).Error)
	require.True(t, edited.After(before), "a changed quote republishes the pick")
}

// A pick curated on the picks page belongs to no submission: publishing a claim never
// takes it over, so rejecting that claim cannot switch the editor's pick off.
func TestPublishDeveloperSubmissionDoesNotClaimUnownedPick(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,names,desc_en,version,version_base,raw,raw_hash,source_locale)
VALUES(1501,'cask','editor-pick','editor-pick','homebrew/cask','Editor Pick',ARRAY['Editor Pick'],'x summary','1.0.0','1.0.0','{}','fixture','en-US')`).Error)

	packageID := int64(1501)
	require.NoError(t, st.SetDeveloperPick(ctx, packageID, nil, "Editor pick.", 0, true))

	payload := json.RawMessage(`{"summary":"Claimed by the developer.","updateLocation":false}`)
	row, err := st.CreateDeveloperSubmission(ctx, store.DeveloperSubmissionInput{
		Kind: "claim", PackageID: &packageID, SourceURL: "https://example.com/editor-pick", Payload: payload, ContactEmail: "claim@example.com",
	})
	require.NoError(t, err)

	feature := true
	_, err = st.PublishDeveloperSubmission(ctx, store.DeveloperPublishInput{
		SubmissionID: row.ID, Status: "published", Payload: payload, Feature: &feature, Quote: "Developer pick.", Sort: 5,
	})
	require.NoError(t, err)

	readPick := func() (submissionID *int64, quote string, sort int, active bool) {
		var row struct {
			SubmissionID *int64
			Quote        string
			Sort         int
			Active       bool
		}
		require.NoError(t, st.DB.Raw(`SELECT submission_id, COALESCE(quote, '') AS quote, sort, active FROM developer_picks WHERE package_id = ?`, packageID).Scan(&row).Error)
		return row.SubmissionID, row.Quote, row.Sort, row.Active
	}
	submissionID, quote, sort, active := readPick()
	require.Nil(t, submissionID, "the editor's pick keeps no submission")
	require.Equal(t, "Editor pick.", quote)
	require.Equal(t, 0, sort)
	require.True(t, active)

	note := "Not the right fit."
	_, err = st.ReviewDeveloperSubmission(ctx, store.DeveloperReviewInput{SubmissionID: row.ID, Status: "rejected", Note: &note})
	require.NoError(t, err)

	submissionID, quote, sort, active = readPick()
	require.Nil(t, submissionID)
	require.Equal(t, "Editor pick.", quote)
	require.Equal(t, 0, sort)
	require.True(t, active, "rejecting the claim leaves the editor's pick on")
}
