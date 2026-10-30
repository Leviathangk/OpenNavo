//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVisibilityGenerationCommitsWithEveryVisibilityChange(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
VALUES(992,'cask','generation','generation','homebrew/cask','Generation','1','1','{}','fixture');
INSERT INTO package_meta(package_id) VALUES(992)`).Error)
	generation, err := st.VisibilityGeneration(ctx)
	require.NoError(t, err)
	for _, sql := range []string{
		"UPDATE package_meta SET hidden=true WHERE package_id=992",
		"UPDATE package_meta SET hidden=false WHERE package_id=992",
		"UPDATE package_meta SET private=true,visibility_reason='Owner decision' WHERE package_id=992",
		"UPDATE package_meta SET private=false WHERE package_id=992",
	} {
		require.NoError(t, st.DB.Exec(sql).Error)
		next, err := st.VisibilityGeneration(ctx)
		require.NoError(t, err)
		require.Equal(t, generation+1, next)
		generation = next
	}
	rolledBack := errors.New("preview rollback")
	require.ErrorIs(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE package_meta SET hidden=true WHERE package_id=992").Error; err != nil {
			return err
		}
		inside, err := (&store.Store{DB: tx}).VisibilityGeneration(ctx)
		require.NoError(t, err)
		require.Equal(t, generation+1, inside)
		return rolledBack
	}), rolledBack)
	next, err := st.VisibilityGeneration(ctx)
	require.NoError(t, err)
	require.Equal(t, generation, next, "previews roll back the generation together with the flag")
}
