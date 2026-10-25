//go:build integration

package catalog_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Editorial notes that only say the changes are not known are published nowhere: not on the versions page, the
// overview's latest release, the lookup the Mac app reads or the feeds built from them. Their versions stay listed
// with their dates; where upstream notes exist for the version, those are shown instead.
func TestPublicReleasesNeverPublishNotesWithoutInformation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"placeholder-notes","name":["Placeholder Notes"],"version":"3.0","artifacts":[{"app":["Placeholder Notes.app"]}]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var id int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='placeholder-notes'").Scan(&id).Error)
	for _, version := range []string{"1.0", "2.0"} {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base,first_seen_at) VALUES(?,?,?,'2026-01-01') ON CONFLICT DO NOTHING`, id, version, version).Error)
	}
	published := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	editorial := func(version, summary, body string) int64 {
		t.Helper()
		var release int64
		require.NoError(t, st.DB.Raw(`INSERT INTO releases(package_id,source,source_key,version,source_locale,source_url,published_at)
 VALUES(?,'editorial',?,?,'en-US','https://vendor.example/releases/'||?::text,?) RETURNING id`, id, "editorial:"+version, version, version, published).Scan(&release).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,title,summary,body_markdown,sections)
 VALUES(?,'en-US','en-US','source',?,?,?,'[]')`, release, "Placeholder Notes "+version, summary, body).Error)
		return release
	}
	latest := editorial("3.0", "Official release notes are unavailable.", "Detailed official release notes are unavailable. No changes are inferred from the version number.")
	editorial("2.0", "Adds tabs", "Adds tabs to the main window and remembers the last open tab between launches.")
	editorial("1.0", "Official release notes are unavailable.", "### Changes\n\n- Not provided: exact changes for this version are unverified.")
	// Version 1.0 has upstream notes, which take the empty note's place.
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_locale,title,body_markdown,source_url,published_at)
 VALUES(?,'github_release','v1.0','1.0','en-US','v1.0','- Fixed a crash when importing large files.','https://github.com/example/placeholder/releases/v1.0','2026-01-02')`, id).Error)
	// A Japanese machine translation of the latest placeholder, current with its source, is no more publishable.
	source, err := st.ReadContentSource(ctx, content.Ref{Entity: "release", ID: latest})
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`INSERT INTO release_i18n(release_id,locale,source_locale,status,source_hash,machine_translated,title,summary,body_markdown,sections)
 VALUES(?,'ja-JP','en-US','machine',?,true,'バージョン 3.0','変更内容は不明です。','このバージョンの変更内容は提供されていません。','[]')`, latest, source.Hash).Error)

	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	published3 := func(entry publicapi.ReleaseEntry) {
		t.Helper()
		require.Equal(t, "3.0", entry.Version)
		require.True(t, entry.IsLatest)
		require.Equal(t, latest, *entry.Id)
		require.Equal(t, publicapi.ReleaseEntrySourceEditorial, entry.Source)
		require.True(t, published.Equal(*entry.PublishedAt))
		require.Equal(t, "https://vendor.example/releases/3.0", *entry.SourceUrl)
		require.False(t, entry.HasNotes)
		require.Nil(t, entry.Title)
		require.Nil(t, entry.Summary)
		require.Nil(t, entry.BodyMarkdown)
		require.Empty(t, entry.Sections)
		require.Equal(t, publicapi.TranslationInfoStatusNone, entry.Translation.Status)
		require.NotNil(t, entry.Compatibility, "the version keeps its metadata")
	}
	unpublished := func(value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		for _, fragment := range []string{"unavailable", "Not provided", "unverified", "変更内容", "Placeholder Notes 3.0", "Placeholder Notes 1.0", "バージョン"} {
			require.NotContains(t, string(raw), fragment)
		}
	}
	for _, locale := range []string{"en-US", "ja-JP"} {
		page, err := svc.ReleasePage(ctx, "cask", "placeholder-notes", locale, 1, 24, false)
		require.NoError(t, err, locale)
		require.Equal(t, 3, page.Total, "every version stays listed")
		published3(page.Records[0])
		unpublished(page)
	}
	page, err := svc.ReleasePage(ctx, "cask", "placeholder-notes", "en-US", 1, 24, false)
	require.NoError(t, err)
	require.True(t, page.Records[1].HasNotes)
	require.Equal(t, "Adds tabs", *page.Records[1].Summary)
	upstream := page.Records[2]
	require.Equal(t, publicapi.ReleaseEntrySourceGithubRelease, upstream.Source)
	require.True(t, upstream.HasNotes)
	require.Equal(t, "- Fixed a crash when importing large files.", *upstream.BodyMarkdown)
	// The notes filter lists only versions that show notes.
	filtered, err := svc.ReleasePage(ctx, "cask", "placeholder-notes", "en-US", 1, 24, true)
	require.NoError(t, err)
	require.Equal(t, 2, filtered.Total)
	require.Equal(t, []string{"2.0", "1.0"}, []string{filtered.Records[0].Version, filtered.Records[1].Version})

	detail, err := svc.Detail(ctx, "cask", "placeholder-notes", "en-US")
	require.NoError(t, err)
	require.Equal(t, 3, detail.ReleaseCount)
	require.NotNil(t, detail.LatestRelease)
	published3(*detail.LatestRelease)
	unpublished(detail.LatestRelease)

	items, err := svc.Lookup(ctx, []publicapi.PackageRef{{Kind: publicapi.PackageKindCask, Token: "placeholder-notes"}}, "ja-JP")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].LatestRelease)
	require.Equal(t, "3.0", items[0].LatestRelease.Version)
	require.True(t, published.Equal(*items[0].LatestRelease.PublishedAt))
	require.Nil(t, items[0].LatestRelease.Summary)
}
