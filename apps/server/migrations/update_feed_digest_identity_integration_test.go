//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestUpdateFeedDigestIdentityMigration(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 118)
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	ctx := context.Background()
	require.NoError(t, goose.UpToContext(ctx, db, ".", 119))
	insert := func(id, source string, team any, resolver string) error {
		return st.DB.Exec(`INSERT INTO update_feeds(bundle_id,source,feed_url,team_id,origin,resolver) VALUES(?,?,'https://example.org/feed',?,'manual',?::jsonb)`, id, source, team, resolver).Error
	}
	require.NoError(t, insert("test.example.Github", "github", nil, `null`))
	require.NoError(t, insert("test.example.Vendor", "vendor", nil, `{"sha256Path":"digest"}`))
	require.NoError(t, insert("test.example.Electron", "vendor", nil, `{"mode":"electron"}`))
	require.NoError(t, insert("test.example.Checksum", "vendor", nil, `{"checksumForUrl":true}`))
	require.NoError(t, insert("test.example.Signed", "vendor", "TESTTEAM01", `null`))
	require.Error(t, insert("test.example.NoDigest", "vendor", nil, `{}`))
	require.Error(t, insert("test.example.EmptyDigest", "vendor", nil, `{"sha256Path":""}`))
	// A downgrade cannot silently discard newly supported publisher rules.
	require.Error(t, goose.DownToContext(ctx, db, ".", 118))
	var count int64
	require.NoError(t, st.DB.Table("update_feeds").Where("bundle_id LIKE 'test.example.%'").Count(&count).Error)
	require.Equal(t, int64(5), count)
	require.NoError(t, st.DB.Exec(`DELETE FROM update_feeds WHERE bundle_id LIKE 'test.example.%'`).Error)
	require.NoError(t, goose.DownToContext(ctx, db, ".", 118))
	require.Error(t, insert("test.example.Github", "github", nil, `null`))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 119))
}
