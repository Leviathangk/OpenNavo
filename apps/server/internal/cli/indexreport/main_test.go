package indexreport

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestReportFormatsAndArguments(t *testing.T) {
	report := store.IndexReport{Rows: []store.IndexReportCount{{PageType: "versions", Locale: "en-US", Decision: "thin_notes_page", Count: 3}}, MachineOnlyOverview: map[string]int{"ja-JP": 2}}
	var out bytes.Buffer
	require.NoError(t, Write(&out, "json", report))
	var decoded store.IndexReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	require.Equal(t, report.Rows, decoded.Rows)
	out.Reset()
	require.NoError(t, Write(&out, "text", report))
	require.Contains(t, out.String(), "thin_notes_page")
	require.Contains(t, out.String(), "ja-JP:2")
	require.Error(t, Write(&out, "csv", report))
	require.Equal(t, 2, run([]string{"-format", "csv"}, &out, &out))
	t.Setenv("DATABASE_URL", "")
	require.Equal(t, 1, run(nil, &out, &out))
}
