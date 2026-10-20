//go:build integration

package management

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFactAndAgentInvalidationCoalesceAndRollback(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(840,'cask','fact-invalidation','fact-invalidation','homebrew/cask','Fixture','1','1','{}','fixture')`).Error)
	now := time.Now().UTC()
	record := facts.Record{Kind: "price", Key: "us-pro", Value: json.RawMessage(`{"region":"US","edition":"Pro","period":"one_time","usage":"paid","downloadFree":true,"amount":"19.99","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor"}`), Evidence: facts.NewProof("https://vendor.example/pricing", now, time.Hour, "verified", now)}
	for _, agentFirst := range []bool{false, true} {
		name := "fact_then_agent"
		if agentFirst {
			name = "agent_then_source_refresh"
		}
		t.Run(name, func(t *testing.T) {
			rollback := errors.New("fixture rollback")
			for _, abort := range []bool{true, false} {
				require.NoError(t, st.DB.Exec("DELETE FROM job_outbox WHERE job_type='cache:invalidate'").Error)
				err := st.WithTx(ctx, func(tx *gorm.DB) error {
					local := &store.Store{DB: tx}
					fact := func() error { return local.UpsertFactRecords(ctx, 840, []facts.Record{record}, now) }
					agent := func() error {
						return queueAgentInvalidation(ctx, local, "pkg:840", []content.Ref{{Entity: "package", ID: 840}})
					}
					first, second := fact, agent
					if agentFirst {
						first, second = agent, fact
					}
					if err := first(); err != nil {
						return err
					}
					if err := second(); err != nil {
						return err
					}
					if abort {
						return rollback
					}
					return nil
				})
				var count int64
				require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&count).Error)
				if abort {
					require.ErrorIs(t, err, rollback)
					require.Zero(t, count, "rollback must not publish any scope")
					continue
				}
				require.NoError(t, err)
				require.EqualValues(t, 1, count)
				var patterns []string
				require.NoError(t, st.DB.Raw("SELECT jsonb_array_elements_text(payload) FROM job_outbox WHERE unique_key='cache:invalidate:seo-content:pkg:840'").Scan(&patterns).Error)
				detail := cache.PackageDetailPrefix + "cask:fact-invalidation:*"
				require.Contains(t, patterns, detail)
				require.Contains(t, patterns, "c:home:*")
				require.Contains(t, patterns, "c:browse:*")
				require.NotContains(t, patterns, "c:*")
				seen := map[string]bool{}
				for _, pattern := range patterns {
					require.False(t, seen[pattern], "duplicate scan: %s", pattern)
					seen[pattern] = true
				}
			}
		})
	}
}
