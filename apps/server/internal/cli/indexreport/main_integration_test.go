//go:build integration

package indexreport

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
)

func TestIndexReportCommandUsesOnlyTestDatabase(t *testing.T) {
	st := testutil.NewSEOStore(t)
	dialector, ok := st.DB.Dialector.(*postgres.Dialector)
	require.True(t, ok)
	t.Setenv("DATABASE_URL", dialector.DSN)
	t.Setenv("WEB_BASE_URL", "https://site.example")
	var out, stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"-format", "json"}, &out, &stderr), stderr.String())
	var report store.IndexReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	require.NotEmpty(t, report.Rows)
	require.Contains(t, report.Scope, "Sitemap membership")
	out.Reset()
	require.Equal(t, 0, run([]string{"-format", "text"}, &out, &stderr), stderr.String())
	require.Contains(t, out.String(), "PAGE TYPE")
}
