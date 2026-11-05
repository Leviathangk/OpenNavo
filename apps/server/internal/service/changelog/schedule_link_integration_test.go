//go:build integration

package changelog_test

import (
	"context"
	"testing"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/changelog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// A kind=link package with a verified public repository is a scheduled candidate. Its changelog settings
// lookup used to filter p.kind='cask', so Schedule failed every tick with gorm.ErrRecordNotFound and never
// removed the candidate.
func TestScheduleResolvesALinkPackageWithAVerifiedRepository(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	const repo = "owner/link-example"
	homepage := "https://example.com"
	id, err := st.WriteLinkApp(ctx, 0, domain.LinkInput{
		Token:        "link-example",
		Name:         "Example Link App",
		SourceLocale: "en-US",
		Homepage:     &homepage,
		GithubRepo:   stringPtr(repo),
	})
	require.NoError(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO package_source_facts(package_id,source,input,subject,status,data)
		VALUES(?,'github',?,?,'found',jsonb_build_object('repo',?::text))`, id, repo, repo, repo).Error)

	// Reading and updating settings must work for a link package; before the fix both paths returned
	// gorm.ErrRecordNotFound and failed the schedule tick.
	settings, err := st.PackageChangelogSettings(ctx, id)
	require.NoError(t, err)
	require.False(t, settings.Excluded)
	_, err = st.SetPackageChangelogSettings(ctx, id, false, store.AuditActor{})
	require.NoError(t, err)

	queue := &queuedSources{}
	svc := &changelog.Service{Store: st, Resolver: pipeline.NewResolver("test"), Queue: queue}
	stats, err := svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["enqueued"])
	require.Equal(t, 1, queue.Count)

	var source struct{ Type, Repo string }
	require.NoError(t, st.DB.WithContext(ctx).Raw(`SELECT type,config->>'repo' AS repo FROM changelog_sources WHERE package_id=?`, id).Scan(&source).Error)
	require.Equal(t, "github_releases", source.Type)
	require.Equal(t, repo, source.Repo)

	// The candidate is resolved, so the next tick neither re-resolves it nor churns a new source.
	unresolved, err := st.UnresolvedHomebrewSources(ctx)
	require.NoError(t, err)
	require.Empty(t, unresolved)
}

func stringPtr(value string) *string { return &value }
