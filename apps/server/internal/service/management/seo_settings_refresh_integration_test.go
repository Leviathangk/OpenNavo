//go:build integration

package management

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOSettingsRefreshesSectionsWithoutCatalogFanout(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(901,'cask','refresh-fixture','refresh-fixture','homebrew/cask','Fixture','1','1','{}','fixture');
 INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(901,'cask','refresh-fixture','upsert','homebrew')`).Error)
	var before int64
	require.NoError(t, st.DB.Table("catalog_changes").Count(&before).Error)
	s := &Service{Store: st, Config: config.Config{WebBaseURL: "https://site.example"}}
	_, err := s.seoSettings(context.Background(), "UpdateSeoSettings", Input{Body: seoBody(t, func(s *store.SEOSettings) {
		s.Pages.OpenLocales = []string{"en-US"}
	}, nil)})
	require.NoError(t, err)
	var after int64
	require.NoError(t, st.DB.Table("catalog_changes").Count(&after).Error)
	require.Equal(t, before, after, "a global setting must not generate per-app recomputation")
	var sections int64
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM content_url_changes WHERE purge_sections AND NOT announce
 AND paths @> ARRAY['/','/apps','/articles','/authors','/security']`).Scan(&sections).Error)
	require.EqualValues(t, 1, sections)
	var payload string
	require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE unique_key='cache:invalidate:seo-settings'").Scan(&payload).Error)
	require.Contains(t, payload, "c:pkg:")
	require.Contains(t, payload, "c:art:*")
	require.Contains(t, payload, "c:site:*")
}
