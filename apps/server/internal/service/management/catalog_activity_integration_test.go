//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// A cursor read never claims completeness it cannot have: events older than the 14-day retention may be
// pruned, so a cursor before them is rejected while afterSeq=0 starts at the oldest retained event.
func TestCatalogActivityCursorRespectsRetention(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}, Now: func() time.Time { return now }}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (701,'cask','kept-app','kept-app','homebrew/cask','Kept','1','1','{}','fixture'),
 (702,'link','kept-link','kept-link','','Kept Link','1','1','{}','fixture'),
 (703,'formula','kept-formula','kept-formula','homebrew/core','Kept Formula','1','1','{}','fixture')`).Error)
	insert := func(id int64, kind, token, event string, age time.Duration) int64 {
		t.Helper()
		var seq int64
		require.NoError(t, st.DB.Raw(`INSERT INTO catalog_changes(package_id,kind,token,op,reason,event_type,version,created_at) VALUES(?,?,?,'upsert',CASE WHEN ?='link' THEN 'link' ELSE 'homebrew' END,NULLIF(?,''),'1',?) RETURNING seq`, id, kind, token, kind, event, now.Add(-age)).Scan(&seq).Error)
		return seq
	}
	older := insert(701, "cask", "kept-app", "created", 16*24*time.Hour)
	old := insert(701, "cask", "kept-app", "updated", 15*24*time.Hour)
	recent := insert(701, "cask", "kept-app", "updated", time.Hour)
	insert(703, "formula", "kept-formula", "updated", time.Hour)
	insert(701, "cask", "kept-app", "", time.Hour)
	link := insert(702, "link", "kept-link", "created", time.Minute)
	read := func(params map[string]any) (map[string]any, error) {
		t.Helper()
		value, err := svc.Execute(ctx, "ListAdminCatalogChanges", Input{Params: params})
		if err != nil {
			return nil, err
		}
		return hermesMap(value), nil
	}
	page, err := read(map[string]any{"afterseq": float64(0)})
	require.NoError(t, err)
	seqs := []int64{}
	for _, record := range page["records"].([]any) {
		seqs = append(seqs, toInt64(record.(map[string]any)["seq"]))
	}
	require.Equal(t, []int64{recent, link}, seqs, "retained cask and link events only: no formulae, no content edits")
	require.Equal(t, link, toInt64(page["nextSeq"]))
	_, err = read(map[string]any{"afterseq": float64(older)})
	var app *domain.AppError
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: cursor_before_retention:afterSeq", app.Msg)
	page, err = read(map[string]any{"afterseq": float64(old)})
	require.NoError(t, err)
	require.EqualValues(t, 2, page["total"], "a cursor at the newest pruned-age event still reads on")
	page, err = read(map[string]any{"afterseq": float64(recent), "type": "created"})
	require.NoError(t, err)
	require.EqualValues(t, 1, page["total"])
	require.Equal(t, "link", page["records"].([]any)[0].(map[string]any)["source"])

	// Pruning records its watermark; a cursor below it stays rejected after the rows are gone.
	_, err = st.PruneCatalogChanges(ctx, now.Add(-14*24*time.Hour))
	require.NoError(t, err)
	_, err = read(map[string]any{"afterseq": float64(older)})
	require.ErrorAs(t, err, &app)
	require.Equal(t, "rejected: cursor_before_retention:afterSeq", app.Msg)
	page, err = read(map[string]any{"afterseq": float64(old)})
	require.NoError(t, err)
	require.EqualValues(t, 2, page["total"])
}
