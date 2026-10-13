//go:build integration

package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestContentURLsBatchWithinRowLimit(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	for _, count := range []int{0, 1, 100, 101, 205} {
		for _, announce := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", count, announce), func(t *testing.T) {
				require.NoError(t, st.DB.Exec("DELETE FROM content_url_changes").Error)
				paths := make([]string, count)
				for i := range paths {
					paths[i] = fmt.Sprintf("/articles/article-%d", i)
				}
				require.NoError(t, st.EnqueueContentURLs(ctx, paths, announce))
				var rows []struct {
					Size     int
					Announce bool
				}
				require.NoError(t, st.DB.Raw("SELECT cardinality(paths) AS size,announce FROM content_url_changes ORDER BY seq").Scan(&rows).Error)
				require.Len(t, rows, (count+99)/100)
				for _, row := range rows {
					require.Positive(t, row.Size)
					require.LessOrEqual(t, row.Size, 100)
					require.Equal(t, announce, row.Announce)
				}
				var actual []string
				require.NoError(t, st.DB.Raw("SELECT unnest(paths) FROM content_url_changes ORDER BY seq").Scan(&actual).Error)
				require.EqualValues(t, paths, append([]string{}, actual...))
			})
		}
	}
}
