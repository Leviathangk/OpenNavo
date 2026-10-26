//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestRelatedPrioritizesEditorialAndSpecificCategories(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,parent_id,icon) VALUES(99001,'fixture-utilities',NULL,'lucide:wrench'),(99002,'fixture-cleaning',99001,'lucide:brush');
 INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,popularity) VALUES
 (99001,'cask','clean-target','clean-target','homebrew/cask','Cleaner','1','1','{}','fixture',100),
 (99002,'cask','generic-tool','generic-tool','homebrew/cask','Generic','1','1','{}','fixture',100),
 (99003,'cask','other-cleaner','other-cleaner','homebrew/cask','Cleaner','1','1','{}','fixture',1),
 (99004,'cask','editor-choice','editor-choice','homebrew/cask','Choice','1','1','{}','fixture',0),
 (99005,'cask','disabled-choice','disabled-choice','homebrew/cask','Disabled','1','1','{}','fixture',0);
 UPDATE packages SET disabled=true WHERE id=99005;
 INSERT INTO package_categories(package_id,category_id,is_primary,source) VALUES
 (99001,99001,false,'human'),(99001,99002,true,'human'),(99002,99001,true,'human'),(99003,99001,false,'human'),(99003,99002,true,'human');
 INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES
 (99001,99005,0,ARRAY['open_source'],'admin'),(99001,99004,1,ARRAY['open_source'],'admin')`).Error)
	p := store.PublicPackage{ID: 99001, Kind: "cask", Popularity: 100}
	rows, err := st.PublicRelated(context.Background(), p, "en-US", 3)
	require.NoError(t, err)
	ids := []int64{}
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	require.Equal(t, []int64{99004, 99003, 99002}, ids)
	rows, err = st.PublicRelated(context.Background(), p, "en-US", 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(99004), rows[0].ID)
	rows, err = st.PublicRelated(context.Background(), p, "en-US", 0)
	require.NoError(t, err)
	require.Empty(t, rows)
}
