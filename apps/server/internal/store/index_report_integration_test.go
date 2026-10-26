//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestIndexReportReadOnlySnapshot(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en) VALUES
 (99101,'cask','report-fixture','report-fixture','homebrew/cask','Report','1','1','{}','fixture','Fixture description');
 INSERT INTO package_i18n(package_id,locale,status,source_locale,source_hash,summary,description,machine_translated,source) VALUES
 (99101,'ja-JP','machine','en-US',repeat('a',64),'Fixture summary','Fixture full description',true,'llm')`).Error)
	var before int64
	require.NoError(t, st.DB.Table("catalog_changes").Count(&before).Error)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	report, err := st.BuildIndexReport(context.Background(), now, "https://site.example")
	require.NoError(t, err)
	require.Equal(t, now, report.GeneratedAt)
	require.Equal(t, 1, report.MachineOnlyOverview["ja-JP"])
	var overview, versions int
	for _, row := range report.Rows {
		if row.Locale == "en-US" {
			switch row.PageType {
			case "overview":
				overview += row.Count
			case "versions":
				versions += row.Count
				require.False(t, row.InSitemap)
			}
		}
	}
	require.Equal(t, 1, overview)
	require.Equal(t, 1, versions)
	var after int64
	require.NoError(t, st.DB.Table("catalog_changes").Count(&after).Error)
	require.Equal(t, before, after)
	// A database role with default read-only transactions still runs the complete report.
	require.NoError(t, st.DB.Exec(`SET default_transaction_read_only=on`).Error)
	_, err = st.BuildIndexReport(context.Background(), now, "https://site.example")
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`SET default_transaction_read_only=off`).Error)
}

func TestIndexReportFontSitemapSwitch(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,is_font) VALUES
 ('cask','font-report','font-report','homebrew/cask','Font','1','1','{}','fixture','Font description',true)`).Error)
	for _, enabled := range []bool{false, true} {
		report, err := st.BuildIndexReport(context.Background(), time.Now(), "https://site.example", enabled)
		require.NoError(t, err)
		listed := 0
		for _, row := range report.Rows {
			if row.PageType == "fonts" && row.InSitemap {
				listed += row.Count
			}
		}
		if enabled {
			require.Positive(t, listed)
		} else {
			require.Zero(t, listed)
		}
	}
}
