//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestContentWritesPersistProofValidity(t *testing.T) {
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	checked := now.Add(-24 * time.Hour)
	svc := &Service{Store: st, Now: func() time.Time { return now }}
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(100,'cask','proof-fixture','proof-fixture','homebrew/cask','Proof Fixture','1','1','{}','fixture');
 INSERT INTO background_items(label) VALUES('com.example.proof');
 INSERT INTO background_item_packages(label,package_id,kind,phase,source_url,checked_at) VALUES('com.example.proof',100,'launchctl','uninstall','https://example.com/docs',now());
 INSERT INTO collections(id,slug,source_locale) VALUES(100,'proof-fixture','en-US');`).Error)
	for _, explicit := range []bool{false, true} {
		name := "default"
		until := checked.Add(30 * 24 * time.Hour)
		if explicit {
			name = "explicit"
			until = checked.Add(7 * 24 * time.Hour)
		}
		t.Run(name, func(t *testing.T) {
			proof := map[string]any{"sourceUrl": "https://example.com/docs", "checkedAt": checked, "status": "verified"}
			if explicit {
				proof["validUntil"] = until
			}
			check := func(t *testing.T, p facts.Proof) {
				t.Helper()
				require.NotNil(t, p.ValidUntil)
				require.True(t, until.Equal(*p.ValidUntil))
				require.Equal(t, "verified", p.At(now).Status)
				require.Equal(t, "stale", p.At(until).Status)
			}
			t.Run("background", func(t *testing.T) {
				result, err := svc.Execute(ctx, "SetBackgroundItem", Input{Key: "com.example.proof", Body: map[string]any{"locale": "en-US", "description": "Fixture helper", "itemType": "launch_agent", "commonPaths": []string{}, "evidence": proof}})
				require.NoError(t, err)
				check(t, result.(facts.BackgroundContent).Evidence)
				saved, err := st.BackgroundContent(ctx, "com.example.proof", "en-US")
				require.NoError(t, err)
				require.NotNil(t, saved)
				check(t, saved.Evidence)
				locales, err := st.BackgroundReadyLocales(ctx, "com.example.proof", now)
				require.NoError(t, err)
				require.Contains(t, locales, "en-US")
			})
			t.Run("collection", func(t *testing.T) {
				result, err := svc.Execute(ctx, "SetCollectionSetupCriteria", Input{ID: 100, Body: map[string]any{"locale": "en-US", "criteria": "Fixture criteria", "evidence": proof}})
				require.NoError(t, err)
				check(t, result.(store.CollectionCriteria).Evidence)
				saved, err := st.CollectionCriteria(ctx, "proof-fixture", "en-US")
				require.NoError(t, err)
				require.NotNil(t, saved)
				check(t, saved.Evidence)
			})
		})
	}
}
