//go:build integration

package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// faultyObjects is the object store with one injected failure: a Put for a key that matches, a
// Delete of any key, or a Delete of one key. Everything else behaves like memoryObjects.
type faultyObjects struct {
	memoryObjects
	failPutPrefix string
	failPutKey    string
	failDeleteKey string
	failDeleteAll bool
	puts, deletes []string
}

func (f *faultyObjects) Put(ctx context.Context, key string, data []byte, mime string) error {
	f.puts = append(f.puts, key)
	if key == f.failPutKey || (f.failPutPrefix != "" && strings.HasPrefix(key, f.failPutPrefix)) {
		return errors.New("object storage rejected the upload")
	}
	return f.memoryObjects.Put(ctx, key, data, mime)
}

func (f *faultyObjects) Delete(ctx context.Context, key string) error {
	f.deletes = append(f.deletes, key)
	if f.failDeleteAll || key == f.failDeleteKey {
		return errors.New("object storage rejected the delete")
	}
	return f.memoryObjects.Delete(ctx, key)
}

// seedRevocationStore publishes one public snapshot, then marks the named package private.
func seedRevocationStore(t *testing.T, tokens ...string) (*store.Store, *faultyObjects, *Service) {
	t.Helper()
	ctx := context.Background()
	st := testutil.NewStore(t)
	for _, token := range tokens {
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","version":"1","desc":"Snapshot summary"}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	objects := &faultyObjects{memoryObjects: memoryObjects{}}
	svc := &Service{Store: st, Objects: objects, Purge: func(context.Context, []string) error { return nil }}
	_, err := svc.Build(ctx)
	require.NoError(t, err)
	objects.puts, objects.deletes = nil, nil
	return st, objects, svc
}

func makePrivate(t *testing.T, st *store.Store, token string) {
	t.Helper()
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,private,visibility_reason)
SELECT id,true,'Owner decision' FROM packages WHERE token=?`, token).Error)
}

func requireBlocked(t *testing.T, st *store.Store, svc *Service, buildErr error) {
	t.Helper()
	require.Error(t, buildErr)
	state, err := st.SnapshotVisibility(context.Background())
	require.NoError(t, err)
	require.Greater(t, state.RevocationGeneration, state.SnapshotRevocation, "a failed origin step must not acknowledge the revocation")
	_, err = svc.Latest(context.Background())
	require.Error(t, err, "metadata stays unavailable while the revocation is unacknowledged")
}

// requireRecovered retries the build after the injected failure and asserts that the revocation is
// acknowledged and that the published bundle no longer has the private token.
func requireRecovered(t *testing.T, st *store.Store, svc *Service, objects *faultyObjects, privateToken string) {
	t.Helper()
	_, err := svc.Build(context.Background())
	require.NoError(t, err)
	state, err := st.SnapshotVisibility(context.Background())
	require.NoError(t, err)
	require.Equal(t, state.RevocationGeneration, state.SnapshotRevocation)
	latest, err := svc.Latest(context.Background())
	require.NoError(t, err)
	key := strings.TrimPrefix(latest.Url, objects.URL(""))
	data, ok := objects.memoryObjects[key]
	require.True(t, ok, key)
	tokens, err := bundleDocumentTokens(File{Key: key, Data: data})
	require.NoError(t, err)
	require.NotContains(t, tokens, privateToken)
}

// AC-88-05: a bundle file that cannot be written leaves latest.json untouched and acknowledges
// nothing; the next build recovers.
func TestRevocationBundleUploadFailureBlocksAndRecovers(t *testing.T) {
	st, objects, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	makePrivate(t, st, "snapshot-private")
	before := bytes.Clone(objects.memoryObjects["snapshots/latest.json"])
	objects.failPutPrefix = "snapshots/text-v2-"
	err := buildOnce(svc)
	requireBlocked(t, st, svc, err)
	require.ErrorContains(t, err, "object storage rejected the upload")
	require.Equal(t, before, objects.memoryObjects["snapshots/latest.json"], "a failed bundle never replaces the published manifest")
	objects.failPutPrefix = ""
	requireRecovered(t, st, svc, objects, "snapshot-private")
}

// AC-88-04: latest.json itself cannot be written; the old manifest stays and nothing is acknowledged.
func TestRevocationLatestUploadFailureBlocksAndRecovers(t *testing.T) {
	st, objects, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	makePrivate(t, st, "snapshot-private")
	before := bytes.Clone(objects.memoryObjects["snapshots/latest.json"])
	objects.failPutKey = "snapshots/latest.json"
	err := buildOnce(svc)
	requireBlocked(t, st, svc, err)
	require.Equal(t, before, objects.memoryObjects["snapshots/latest.json"])
	objects.failPutKey = ""
	requireRecovered(t, st, svc, objects, "snapshot-private")
}

// AC-88-06: an old object that cannot be deleted keeps the revocation open; the retry deletes it.
func TestRevocationDeleteFailureBlocksAndRecovers(t *testing.T) {
	st, objects, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	objects.memoryObjects["snapshots/orphan-upload.json.gz"] = []byte("old private data")
	makePrivate(t, st, "snapshot-private")
	objects.failDeleteKey = "snapshots/orphan-upload.json.gz"
	err := buildOnce(svc)
	requireBlocked(t, st, svc, err)
	require.ErrorContains(t, err, "revoke snapshot object")
	objects.failDeleteKey = ""
	requireRecovered(t, st, svc, objects, "snapshot-private")
	require.NotContains(t, objects.memoryObjects, "snapshots/orphan-upload.json.gz")
}

// AC-88-07 (first half): SaveSnapshot cannot record the new record; the revocation stays open.
func TestRevocationSaveSnapshotFailureBlocksAndRecovers(t *testing.T) {
	st, objects, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	makePrivate(t, st, "snapshot-private")
	require.NoError(t, st.DB.Exec(`CREATE FUNCTION fail_snapshot_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'snapshot table unavailable'; END $$;
CREATE TRIGGER fail_snapshot_insert BEFORE INSERT ON catalog_snapshots FOR EACH ROW EXECUTE FUNCTION fail_snapshot_insert()`).Error)
	t.Cleanup(func() {
		_ = st.DB.Exec(`DROP TRIGGER IF EXISTS fail_snapshot_insert ON catalog_snapshots; DROP FUNCTION IF EXISTS fail_snapshot_insert()`).Error
	})
	err := buildOnce(svc)
	requireBlocked(t, st, svc, err)
	require.ErrorContains(t, err, "snapshot table unavailable")
	require.NoError(t, st.DB.Exec(`DROP TRIGGER fail_snapshot_insert ON catalog_snapshots; DROP FUNCTION fail_snapshot_insert()`).Error)
	requireRecovered(t, st, svc, objects, "snapshot-private")
}

// AC-88-07 (second half): CompleteSnapshotRevocation cannot commit; the generation difference stays
// and the retry finishes against the same generation.
func TestRevocationCompleteFailureKeepsTheGenerationDifference(t *testing.T) {
	st, objects, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	makePrivate(t, st, "snapshot-private")
	require.NoError(t, st.DB.Exec(`CREATE FUNCTION fail_revocation_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'visibility table unavailable'; END $$;
CREATE TRIGGER fail_revocation_complete BEFORE UPDATE ON catalog_visibility FOR EACH ROW
WHEN (NEW.snapshot_revocation IS DISTINCT FROM OLD.snapshot_revocation) EXECUTE FUNCTION fail_revocation_complete()`).Error)
	t.Cleanup(func() {
		_ = st.DB.Exec(`DROP TRIGGER IF EXISTS fail_revocation_complete ON catalog_visibility; DROP FUNCTION IF EXISTS fail_revocation_complete()`).Error
	})
	state, err := st.SnapshotVisibility(context.Background())
	require.NoError(t, err)
	err = buildOnce(svc)
	requireBlocked(t, st, svc, err)
	require.ErrorContains(t, err, "visibility table unavailable")
	after, err := st.SnapshotVisibility(context.Background())
	require.NoError(t, err)
	require.Equal(t, state.RevocationGeneration, after.RevocationGeneration, "a retry continues from the same generation")
	require.NoError(t, st.DB.Exec(`DROP TRIGGER fail_revocation_complete ON catalog_visibility; DROP FUNCTION fail_revocation_complete()`).Error)
	requireRecovered(t, st, svc, objects, "snapshot-private")
}

// A private base package whose variant stays public publishes the variant in the bundle. The
// self-check compares exact tokens, so the revocation is acknowledged and Latest() recovers; the
// old variant-base comparison blocked every rebuild and left the catalog at 503 until the attempts
// ran out.
func TestRevocationAcknowledgesWhenAPublicVariantOfAPrivateBaseIsPublished(t *testing.T) {
	ctx := context.Background()
	st, objects, svc := seedRevocationStore(t, "snapshot-aerial", "snapshot-aerial@beta")
	makePrivate(t, st, "snapshot-aerial")
	_, err := svc.Latest(ctx)
	require.Error(t, err, "metadata is unavailable until the revocation is acknowledged")
	warning, err := svc.RevokePrivate(ctx)
	require.NoError(t, err)
	require.Empty(t, warning)
	state, err := st.SnapshotVisibility(ctx)
	require.NoError(t, err)
	require.Equal(t, state.RevocationGeneration, state.SnapshotRevocation, "the public variant does not block the acknowledgement")
	latest, err := svc.Latest(ctx)
	require.NoError(t, err)
	key := strings.TrimPrefix(latest.Url, objects.URL(""))
	data, ok := objects.memoryObjects[key]
	require.True(t, ok, key)
	tokens, err := bundleDocumentTokens(File{Key: key, Data: data})
	require.NoError(t, err)
	require.NotContains(t, tokens, "snapshot-aerial", "the private base entry is gone")
	require.Contains(t, tokens, "snapshot-aerial@beta", "the public variant is still published")
}

// AC-88-08: when the catalog filter no longer removes a private entry, the self-check must block the
// revocation and log it at error level, never acknowledge the bundle.
func TestRevocationSelfCheckBlocksAVariantOfAPrivatePackage(t *testing.T) {
	st, _, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	// Simulate a filter that stopped removing private entries: the seam reports the package the
	// catalog should have dropped, and the build must refuse to acknowledge it.
	svc.SelfCheck = func(bundle Bundle, tokens []string) (string, bool) {
		for _, token := range tokens {
			return token, true
		}
		return "", false
	}
	makePrivate(t, st, "snapshot-private")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	buildErr := buildOnce(svc)
	require.Error(t, buildErr)
	require.ErrorContains(t, buildErr, `snapshot self-check still contains private package "snapshot-private"`)
	require.Contains(t, logs.String(), "level=ERROR")
	require.Contains(t, logs.String(), "snapshot self-check found a private package")
	requireBlocked(t, st, svc, buildErr)
}

// AC-88-11: a deferred purge is logged with the paused status at error level, so the existing alert
// channel sees it instead of a silent warning.
func TestRevocationDeferredPurgeLogsPausedStatus(t *testing.T) {
	st, _, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	svc.Purge = func(context.Context, []string) error { return deferredPurgeForTest{} }
	makePrivate(t, st, "snapshot-private")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	warning, err := svc.RevokePrivate(context.Background())
	require.NoError(t, err)
	require.Equal(t, PurgePendingWarning, warning)
	require.Contains(t, logs.String(), "level=ERROR")
	require.Contains(t, logs.String(), "paused: Cloudflare purge unavailable")
}

// AC-88-17: two visibility changes before the purge recovers merge into one acknowledgement that
// excludes both packages and queues the prefix once.
func TestRevocationMergesConsecutiveVisibilityChanges(t *testing.T) {
	st, objects, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private", "snapshot-second")
	var purges []string
	svc.Purge = func(_ context.Context, prefixes []string) error {
		purges = append(purges, prefixes...)
		return nil
	}
	makePrivate(t, st, "snapshot-private")
	makePrivate(t, st, "snapshot-second")
	state, err := st.SnapshotVisibility(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, state.RevocationGeneration-state.SnapshotRevocation)
	warning, err := svc.RevokePrivate(context.Background())
	require.NoError(t, err)
	require.Empty(t, warning)
	final, err := st.SnapshotVisibility(context.Background())
	require.NoError(t, err)
	require.Equal(t, final.RevocationGeneration, final.SnapshotRevocation, "one acknowledgement covers both changes")
	latest, err := svc.Latest(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, latest.ItemCount)
	for key, data := range objects.memoryObjects {
		if !strings.HasSuffix(key, ".gz") || key == "snapshots/orphan-upload.json.gz" {
			continue
		}
		tokens, err := bundleDocumentTokens(File{Key: key, Data: data})
		require.NoError(t, err, key)
		require.NotContains(t, tokens, "snapshot-private", key)
		require.NotContains(t, tokens, "snapshot-second", key)
	}
	require.Equal(t, []string{"cdn.example.test/snapshots/"}, purges, "the prefix is queued once")
}

// deferredPurgeForTest stands in for urlnotify.ErrPurgeDeferred, which Build recognizes by
// errors.Is; the test wraps the real sentinel so the same branch runs.
type deferredPurgeForTest struct{}

func (deferredPurgeForTest) Error() string { return "Cloudflare purge disabled" }
func (deferredPurgeForTest) Unwrap() error { return urlnotify.ErrPurgeDeferred }

func buildOnce(svc *Service) error {
	_, err := svc.Build(context.Background())
	return err
}

// AC-88-12: the three metrics read what the revocation left behind: the generation gap, the purge
// debt age and the time Latest() has answered 503.
func TestSnapshotRevocationMetricsReflectBlockedState(t *testing.T) {
	ctx := context.Background()
	st, _, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private")
	makePrivate(t, st, "snapshot-private")
	// Block the purge queue write so the revocation cannot be acknowledged.
	svc.Purge = func(context.Context, []string) error { return errors.New("purge queue unavailable") }
	require.Error(t, buildOnce(svc))
	_, err := svc.Latest(ctx)
	require.Error(t, err, "the failed revocation makes Latest unavailable and records its start")
	require.NoError(t, st.QueueURLPurges(ctx, []string{"cdn.example.test/snapshots/catalog.json.gz"}, 1000, nil))
	state, err := st.MetricState(ctx, time.Now().UTC().Add(90*time.Second))
	require.NoError(t, err)
	require.Greater(t, state.SnapshotRevocationGap, int64(0))
	require.Greater(t, state.OldestPurgeDebtSeconds, float64(0), "the queued purge ages")
	require.Greater(t, state.SnapshotUnavailableSeconds, float64(0), "the 503 window is measured")
}

func TestSnapshotRevocationPurgeQueueWriteFailureIsBlocking(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"snapshot-private","version":"1","desc":"Snapshot summary"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	objects := &faultyObjects{memoryObjects: memoryObjects{}}
	svc := &Service{Store: st, Objects: objects, Purge: func(ctx context.Context, prefixes []string) error {
		return st.QueueURLPurges(ctx, prefixes, 1000, nil)
	}}
	_, err = svc.Build(ctx)
	require.NoError(t, err)
	makePrivate(t, st, "snapshot-private")
	require.NoError(t, st.DB.Exec("ALTER TABLE url_notify_state RENAME TO url_notify_state_broken").Error)
	t.Cleanup(func() {
		require.NoError(t, st.DB.Exec("ALTER TABLE IF EXISTS url_notify_state_broken RENAME TO url_notify_state").Error)
	})
	err = buildOnce(svc)
	requireBlocked(t, st, svc, err)
	require.ErrorContains(t, err, "queue revoked snapshot purge")
	require.NoError(t, st.DB.Exec("ALTER TABLE IF EXISTS url_notify_state_broken RENAME TO url_notify_state").Error)
	requireRecovered(t, st, svc, objects, "snapshot-private")
}

// AC-88-16 / F-03: a package that turns private while the documents are being published must not be
// acknowledged. The purge callback runs after the self-check and before the acknowledgement, which
// is exactly where a concurrent visibility write can land.
func TestRevocationSupersededByAVisibilityChangeDuringTheBuild(t *testing.T) {
	ctx := context.Background()
	st, objects, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private", "snapshot-second")
	makePrivate(t, st, "snapshot-private")
	changed := false
	svc.Purge = func(context.Context, []string) error {
		if !changed {
			changed = true
			makePrivate(t, st, "snapshot-second")
		}
		return nil
	}
	err := buildOnce(svc)
	require.ErrorIs(t, err, ErrRevocationSuperseded)
	state, err := st.SnapshotVisibility(ctx)
	require.NoError(t, err)
	require.Greater(t, state.RevocationGeneration, state.SnapshotRevocation, "the superseded build acknowledges nothing")
	_, err = svc.Latest(ctx)
	require.Error(t, err)
	// The retry sees the newer generation and finishes with both packages excluded.
	warning, err := svc.RevokePrivate(ctx)
	require.NoError(t, err)
	require.Empty(t, warning)
	final, err := st.SnapshotVisibility(ctx)
	require.NoError(t, err)
	require.Equal(t, final.RevocationGeneration, final.SnapshotRevocation)
	latest, err := svc.Latest(ctx)
	require.NoError(t, err)
	key := strings.TrimPrefix(latest.Url, objects.URL(""))
	tokens, err := bundleDocumentTokens(File{Key: key, Data: objects.memoryObjects[key]})
	require.NoError(t, err)
	require.NotContains(t, tokens, "snapshot-private")
	require.NotContains(t, tokens, "snapshot-second")
}

// The private set is read in the bundle's own repeatable-read transaction (F-03), and packages that
// were removed never poison it (F-12).
func TestBundlePrivateTokensMatchItsSnapshot(t *testing.T) {
	ctx := context.Background()
	st, _, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private", "snapshot-removed")
	makePrivate(t, st, "snapshot-private")
	makePrivate(t, st, "snapshot-removed")
	require.NoError(t, st.DB.Exec(`UPDATE packages SET removed_at=now() WHERE token='snapshot-removed'`).Error)
	bundle, _, err := svc.bundle(ctx, false, true)
	require.NoError(t, err)
	require.Equal(t, []string{"snapshot-private"}, bundle.PrivateTokens, "removed packages are not private tokens of the catalog")
	for _, file := range bundle.Files {
		tokens, err := bundleDocumentTokens(file)
		require.NoError(t, err, file.Key)
		require.NotContains(t, tokens, "snapshot-private", file.Key)
		require.NotContains(t, tokens, "snapshot-removed", file.Key)
	}
	// Without a revocation there is no self-check, so the set is not read at all.
	plain, _, err := svc.bundle(ctx, false, false)
	require.NoError(t, err)
	require.Nil(t, plain.PrivateTokens)
}

// A burst of visibility writes must not loop in RevokePrivate: after the bound it reports the
// superseded error, leaves the refusal to acknowledge in place and lets the outbox rebuild.
func TestRevokePrivateStopsAfterAStreamOfVisibilityChanges(t *testing.T) {
	ctx := context.Background()
	st, _, svc := seedRevocationStore(t, "snapshot-public", "snapshot-private", "snapshot-second")
	makePrivate(t, st, "snapshot-private")
	attempts := 0
	svc.SelfCheck = func(Bundle, []string) (string, bool) {
		attempts++
		// Every revalidation finds a newer generation, as a continuous stream of admin writes would.
		require.NoError(t, st.DB.Exec(`UPDATE catalog_visibility SET revocation_generation=revocation_generation+1 WHERE id=true`).Error)
		return "", false
	}
	_, err := svc.RevokePrivate(ctx)
	require.ErrorIs(t, err, ErrRevocationSuperseded)
	require.Equal(t, maxRevocationAttempts, attempts)
	state, err := st.SnapshotVisibility(ctx)
	require.NoError(t, err)
	require.Greater(t, state.RevocationGeneration, state.SnapshotRevocation, "nothing was acknowledged")
}
