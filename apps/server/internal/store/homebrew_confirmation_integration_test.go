//go:build integration

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestHomebrewConfirmationRefreshWindowAndIdentity(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-24 * time.Hour)
	for _, tc := range []struct {
		token    string
		checked  *time.Time
		observed store.HomebrewConfirmation
		removed  bool
		refresh  bool
	}{
		{"recent", timePointer(now.Add(-time.Hour)), store.HomebrewConfirmation{Tap: "homebrew/cask", RawHash: "fixture"}, false, false},
		{"boundary", timePointer(now.Add(-6 * time.Hour)), store.HomebrewConfirmation{Tap: "homebrew/cask", RawHash: "fixture"}, false, false},
		{"expired", timePointer(now.Add(-6*time.Hour - time.Microsecond)), store.HomebrewConfirmation{Tap: "homebrew/cask", RawHash: "fixture"}, false, true},
		{"unknown", nil, store.HomebrewConfirmation{Tap: "homebrew/cask", RawHash: "fixture"}, false, true},
		{"future", timePointer(now.Add(time.Hour)), store.HomebrewConfirmation{Tap: "homebrew/cask", RawHash: "fixture"}, false, false},
		{"wrong-hash", &old, store.HomebrewConfirmation{Tap: "homebrew/cask", RawHash: "another"}, false, false},
		{"wrong-tap", &old, store.HomebrewConfirmation{Tap: "another/tap", RawHash: "fixture"}, false, false},
		{"wrong-token", &old, store.HomebrewConfirmation{Token: "absent", Tap: "homebrew/cask", RawHash: "fixture"}, false, false},
		{"removed", &old, store.HomebrewConfirmation{Tap: "homebrew/cask", RawHash: "fixture"}, true, false},
	} {
		t.Run(tc.token, func(t *testing.T) {
			var removedAt *time.Time
			if tc.removed {
				removedAt = &old
			}
			require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,homebrew_checked_at,removed_at)
 VALUES('cask',?,?,'homebrew/cask','Fixture','1','1','{}','fixture',?,?)`, tc.token, tc.token, tc.checked, removedAt).Error)
			type confirmationState struct {
				CTID      string `gorm:"column:ctid"`
				CheckedAt *time.Time
				RemovedAt *time.Time
				UpdatedAt time.Time
			}
			read := func() confirmationState {
				var out confirmationState
				require.NoError(t, st.DB.Raw(`SELECT ctid::text,homebrew_checked_at AS checked_at,removed_at,updated_at FROM packages WHERE token=?`, tc.token).Scan(&out).Error)
				return out
			}
			before := read()
			if tc.observed.Token == "" {
				tc.observed.Token = tc.token
			}
			require.NoError(t, st.ConfirmHomebrewDefinitions(ctx, []store.HomebrewConfirmation{tc.observed}, now))
			after := read()
			if tc.refresh {
				require.NotNil(t, after.CheckedAt)
				require.True(t, now.Equal(*after.CheckedAt))
				require.NotEqual(t, before.CTID, after.CTID)
			} else {
				require.Equal(t, before, after, "skipped rows must not even be rewritten")
			}
			require.Equal(t, before.UpdatedAt, after.UpdatedAt)
			require.Equal(t, before.RemovedAt, after.RemovedAt)
		})
	}
}

func timePointer(at time.Time) *time.Time { return &at }

func TestHomebrewConfirmationCommitsBoundedBatchesAndResumes(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-24 * time.Hour)
	// Audit real PostgreSQL transaction IDs, not just the size of SQL statements.
	require.NoError(t, st.DB.Exec(`CREATE TABLE confirmation_audit (transaction_id bigint, token text);
 CREATE FUNCTION audit_confirmation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 INSERT INTO confirmation_audit VALUES(txid_current(),NEW.token); RETURN NEW; END $$;
 CREATE TRIGGER audit_confirmation AFTER UPDATE OF homebrew_checked_at ON packages FOR EACH ROW EXECUTE FUNCTION audit_confirmation()`).Error)
	seed := func(prefix string, count int) []store.HomebrewConfirmation {
		t.Helper()
		require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,homebrew_checked_at)
 SELECT 'cask',?||i,?||i,'homebrew/cask','Fixture','1','1','{}','fixture',? FROM generate_series(1,?) i`, prefix, prefix, old, count).Error)
		observed := make([]store.HomebrewConfirmation, count)
		for i := range observed {
			observed[i] = store.HomebrewConfirmation{Token: fmt.Sprintf("%s%d", prefix, i+1), Tap: "homebrew/cask", RawHash: "fixture"}
		}
		return observed
	}
	for _, tc := range []struct {
		count int
		sizes []int
	}{
		{0, nil}, {499, []int{499}}, {500, []int{500}},
		{501, []int{500, 1}}, {1001, []int{500, 500, 1}},
	} {
		t.Run(fmt.Sprint(tc.count), func(t *testing.T) {
			require.NoError(t, st.DB.Exec("DELETE FROM confirmation_audit").Error)
			observed := seed(fmt.Sprintf("batch-%d-", tc.count), tc.count)
			require.NoError(t, st.ConfirmHomebrewDefinitions(ctx, observed, now))
			var sizes []int
			require.NoError(t, st.DB.Raw(`SELECT count(*) FROM confirmation_audit GROUP BY transaction_id ORDER BY transaction_id`).Scan(&sizes).Error)
			require.Equal(t, tc.sizes, sizes)
			var confirmed int64
			require.NoError(t, st.DB.Raw(`SELECT count(*) FROM packages WHERE token LIKE ? AND homebrew_checked_at=?`, fmt.Sprintf("batch-%d-%%", tc.count), now).Scan(&confirmed).Error)
			require.EqualValues(t, tc.count, confirmed)
		})
	}
	observed := seed("resume-", 501)
	require.NoError(t, st.DB.Exec(`DELETE FROM confirmation_audit;
 CREATE FUNCTION interrupt_confirmation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.token='resume-501' THEN RAISE EXCEPTION 'fixture interruption'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER interrupt_confirmation BEFORE UPDATE OF homebrew_checked_at ON packages FOR EACH ROW EXECUTE FUNCTION interrupt_confirmation()`).Error)
	require.ErrorContains(t, st.ConfirmHomebrewDefinitions(ctx, observed, now), "fixture interruption")
	var confirmed int64
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM packages WHERE token LIKE 'resume-%' AND homebrew_checked_at=?`, now).Scan(&confirmed).Error)
	require.EqualValues(t, 500, confirmed, "first batch must commit before the next begins")
	var unlocked bool
	require.NoError(t, st.DB.Raw(`SELECT pg_try_advisory_xact_lock(hashtextextended('catalog:changes',0))`).Scan(&unlocked).Error)
	require.True(t, unlocked, "failed batch must release the global lock")
	require.NoError(t, st.DB.Exec(`DROP TRIGGER interrupt_confirmation ON packages; DROP FUNCTION interrupt_confirmation()`).Error)
	require.NoError(t, st.ConfirmHomebrewDefinitions(ctx, observed, now))
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM confirmation_audit`).Scan(&confirmed).Error)
	require.EqualValues(t, 501, confirmed, "retry must not rewrite the completed batch")
}
