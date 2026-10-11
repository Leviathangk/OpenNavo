//go:build integration

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

type memoryObjects map[string][]byte

func (m memoryObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	m[key] = bytes.Clone(data)
	return nil
}
func (m memoryObjects) URL(key string) string { return "https://cdn.example.test/" + key }

func e2eFingerprint(t *testing.T, st *store.Store) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"packages", "package_meta", "package_i18n", "package_categories", "package_versions", "analytics_snapshots", "admin_users", "admin_user_roles", "releases", "release_i18n", "collections", "collection_items", "collection_i18n", "features", "feature_i18n", "feedback", "search_synonyms", "catalog_changes", "catalog_snapshots", "assets", "package_screenshots", "desktop_releases", "search_queries", "changelog_sources"} {
		var digest string
		require.NoError(t, st.DB.Raw("SELECT md5(COALESCE(string_agg(row_to_json(t)::text,'|' ORDER BY row_to_json(t)::text),'')) FROM "+table+" t").Scan(&digest).Error)
		result[table] = digest
	}
	return result
}

func TestE2EFixturesIdempotentSearchRolesReleasesAndSnapshot(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	objects := memoryObjects{}
	service := &Service{Store: st, Objects: objects}
	cfg := config.Config{AppEnv: "dev", E2EAdminPassword: "e2e-isolated-dev-only"} //nolint:gosec // Explicit development fixture password, used only in temporary databases on random ports.
	stats, err := service.Run(ctx, cfg)
	require.NoError(t, err)
	require.Equal(t, 40, stats["items"])
	before := e2eFingerprint(t, st)
	stats, err = service.Run(ctx, cfg)
	require.NoError(t, err)
	require.Equal(t, true, stats["skipped"])
	require.Equal(t, before, e2eFingerprint(t, st), "second seed must preserve row data, IDs, hashes and directory cursor")
	var queries, sources int64
	require.NoError(t, st.DB.Table("search_queries").Count(&queries).Error)
	require.NoError(t, st.DB.Table("changelog_sources").Count(&sources).Error)
	require.Equal(t, int64(8), queries)
	require.Zero(t, sources)
	var editorial int64
	require.NoError(t, st.DB.Table("releases").Where("source=?", "editorial").Count(&editorial).Error)
	require.Equal(t, int64(1), editorial)
	// Across days, roll only insight fixture dates; do not alter catalog, versions, media or snapshots.
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 30)
	service.Now = func() time.Time { return day }
	_, err = service.Run(ctx, cfg)
	require.NoError(t, err)
	rolled := e2eFingerprint(t, st)
	for table, digest := range before {
		if table == "search_queries" {
			require.NotEqual(t, digest, rolled[table])
		} else {
			require.Equal(t, digest, rolled[table], table)
		}
	}
	for table, count := range map[string]int64{"packages": 60, "package_i18n": 120, "package_categories": 60, "analytics_snapshots": 60, "categories": 22, "category_i18n": 44, "admin_users": 5, "admin_user_roles": 5, "releases": 12, "release_i18n": 24, "collections": 1, "collection_items": 15, "features": 1, "feedback": 3, "catalog_changes": 60, "catalog_snapshots": 1, "assets": 6, "package_screenshots": 2, "desktop_releases": 5, "llm_usage": 0, "job_outbox": 0} {
		var actual int64
		require.NoError(t, st.DB.Table(table).Count(&actual).Error)
		require.Equal(t, count, actual, table)
	}
	users, err := st.E2EUsers(ctx)
	require.NoError(t, err)
	for _, account := range seeds.E2EAccounts() {
		var user store.AuthUser
		for _, item := range users {
			if item.UserName == account.Username {
				user = item
			}
		}
		require.NotZero(t, user.ID)
		require.True(t, auth.VerifyPassword(cfg.E2EAdminPassword, user.PasswordHash))
		permissions, err := st.Permissions(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, []string{account.Role}, permissions.Roles)
	}
	for query, token := range map[string]string{"vscode": "visual-studio-code", "微信": "wechat", "rg": "ripgrep"} {
		terms, err := st.SearchSynonyms(ctx, query)
		require.NoError(t, err)
		matches, _, err := st.Search(ctx, domain.SearchInput{Query: query, Locale: "zh-CN", Platform: "web", Current: 1, Size: 20}, append([]string{query}, terms...), 0.5, 0.7)
		require.NoError(t, err)
		if query == "rg" {
			require.Empty(t, matches)
			_, err := st.PublicPackage(ctx, "formula", token, "zh-CN")
			require.Error(t, err)
			continue
		}
		require.NotEmpty(t, matches)
		p, err := st.PublicPackage(ctx, map[string]string{"rg": "formula", "vscode": "cask", "微信": "cask"}[query], token, "zh-CN")
		require.NoError(t, err)
		require.Equal(t, p.ID, matches[0].ID, query)
	}
	public := &catalog.PublicService{Store: st, WebBaseURL: "https://opennavo.example"}
	home, err := public.Home(ctx, "zh-CN")
	require.NoError(t, err)
	require.Len(t, home.PopularApps, 12)
	require.Empty(t, home.PopularCli)
	require.Zero(t, home.Stats.Formulae)
	require.Len(t, home.Features, 1)
	require.Equal(t, "ghostty", *home.Features[0].Target.Token)
	for token, count := range map[string]int{"visual-studio-code": 8, "ghostty": 3} {
		page, err := public.ReleasePage(ctx, "cask", token, "zh-CN", 1, 20, false)
		require.NoError(t, err)
		require.Equal(t, count, page.Total)
		statuses := map[string]bool{}
		for _, release := range page.Records {
			require.Len(t, release.Sections, 2)
			require.Contains(t, *release.BodyMarkdown, "改善")
			statuses[string(release.Translation.Status)] = true
		}
		require.True(t, statuses["manual"] && statuses["machine"])
	}
	for _, period := range []string{"30d", "90d", "365d"} {
		page, err := public.Rankings(ctx, store.PublicFilter{Kind: "cask", Period: period, Locale: "zh-CN", Current: 1, Size: 100})
		require.NoError(t, err)
		require.Equal(t, 37, page.Total)
	}
	record, err := st.LatestSnapshot(ctx)
	require.NoError(t, err)
	reader, err := gzip.NewReader(bytes.NewReader(objects[record.StorageKey]))
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var doc snapshot.Document
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Len(t, doc.Items, 40)
	require.Len(t, doc.Categories, 20)
	for _, item := range doc.Items {
		require.NotNil(t, item.DownloadSize)
		require.Positive(t, *item.DownloadSize)
		require.Greater(t, *item.Installs90d, item.Installs30d)
		require.Greater(t, *item.Installs365d, *item.Installs90d)
	}
	verifyPageData(t, st, objects)
	// Merge legacy pending-review data into machine; manually correct the three designated fixtures and leave the other three machine translations unchanged.
	review := &management.Service{Store: st}
	for index, version := range []string{"1.135.0", "1.134.0", "1.133.0"} {
		queue, err := review.Execute(ctx, "ListTranslationQueue", management.Input{Params: map[string]any{"type": "release", "status": "machine"}})
		require.NoError(t, err)
		pending, ok := queue.(store.AdminPage)
		require.True(t, ok)
		require.Len(t, pending.Records, 6-index)
		var item struct {
			ID      int64
			Version string
		}
		found := false
		for _, raw := range pending.Records {
			require.NoError(t, json.Unmarshal(raw, &item))
			if item.Version == version {
				found = true
				break
			}
		}
		require.True(t, found)
		require.Equal(t, version, item.Version)
		_, err = review.Execute(ctx, "ApproveTranslations", management.Input{
			Body: map[string]any{"type": "release", "ids": []any{float64(item.ID)}}, Actor: store.AuditActor{ID: &users[0].ID},
		})
		require.NoError(t, err)
		page, err := public.ReleasePage(ctx, "cask", "visual-studio-code", "zh-CN", 1, 20, false)
		require.NoError(t, err)
		require.Equal(t, "1.140.0", page.Records[0].Version)
		require.Equal(t, "manual", string(page.Records[0].Translation.Status))
		require.Equal(t, "1.139.0", page.Records[1].Version)
		require.Equal(t, "machine", string(page.Records[1].Translation.Status))
		for _, release := range page.Records {
			if release.Version == version {
				require.Equal(t, "manual", string(release.Translation.Status))
			}
		}
	}
	var remaining int64
	require.NoError(t, st.DB.Table("release_i18n").Where("status='machine'").Count(&remaining).Error)
	require.Equal(t, int64(3), remaining, "untouched machine translations remain published")
	// Role resets must clear existing permission caches; password replacement must invalidate old sessions.
	user := users[0]
	key := fmt.Sprintf("admin:perm:%d:%d", user.ID, user.SessionVersion)
	require.NoError(t, (cache.Redis{Client: st.Redis}).Save(ctx, key, []byte(`{"roles":["R_SUPER"],"buttons":[]}`), time.Minute))
	cfg.E2EAdminPassword = "e2e-replaced-dev-only"
	_, err = service.Run(ctx, cfg)
	require.NoError(t, err)
	require.Equal(t, int64(0), st.Redis.Exists(ctx, key).Val())
	updated, err := st.AuthUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.SessionVersion+1, updated.SessionVersion)
	require.True(t, auth.VerifyPassword(cfg.E2EAdminPassword, updated.PasswordHash))
	require.NoError(t, st.DB.Table("release_i18n").Where("status='machine'").Count(&remaining).Error)
	require.Equal(t, int64(6), remaining, "seed restores three corrected samples alongside three untouched machine translations")
}
