//go:build integration

package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type testSource struct {
	Data              map[string][]string
	Fail, NotModified bool
	Previous          map[string]homebrew.Conditional
}

func (s *testSource) Stream(ctx context.Context, kind string, previous homebrew.Conditional, consume func(homebrew.Package) error) (homebrew.StreamResult, error) {
	s.Previous[kind] = previous
	result := homebrew.StreamResult{Conditional: homebrew.Conditional{ETag: `"fixture"`}}
	if s.NotModified {
		result.NotModified = true
		return result, nil
	}
	for _, raw := range s.Data[kind] {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		item, err := homebrew.Normalize(kind, json.RawMessage(raw))
		if err != nil {
			return result, err
		}
		if err := consume(item); err != nil {
			return result, err
		}
		result.Fetched++
	}
	if s.Fail {
		return result, errors.New("interrupted download")
	}
	return result, nil
}

type testQueue struct {
	Events []string
	Fail   bool
}

func TestCatalogRenormalizesStoredRawDespiteNotModifiedAndSameHash(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	raw := json.RawMessage(`{"token":"platform-fix","name":["人工名称"],"version":"2","url":"https://example.com/arm","supported_platforms":["arm64_sonoma","sonoma"],"depends_on":{"arch":[{"type":"arm","bits":64}],"macos":{">=":["14"]}},"artifacts":[{"font":["Test.otf"]},{"zap":[{"trash":"~/.test"}]}],"variations":{"sonoma":{"depends_on":{"macos":{">=":["13"]}},"version":"1"}}}`)
	item, err := homebrew.Normalize("cask", raw)
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: item}}))
	require.NoError(t, st.DB.Exec(`UPDATE packages SET dependencies='{"dependsOn":{"arch":[{"type":"arm","bits":64}],"macos":{">=":["14"]}}}',artifacts='{"apps":[],"binaries":[],"pkgs":[]}',supports_arm64=true,supports_x86_64=false,min_macos='14',download_size=1234 WHERE token='platform-fix'`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE package_i18n SET display_name='人工名称保持',summary='人工简介保持',status='manual',source='human'`).Error)
	var before struct {
		VersionChangedAt time.Time
		RawHash          string
	}
	require.NoError(t, st.DB.Raw(`SELECT version_changed_at,raw_hash FROM packages WHERE token='platform-fix'`).Scan(&before).Error)
	source := &testSource{NotModified: true, Previous: map[string]homebrew.Conditional{}}
	queue := &testQueue{}
	service := &Service{Store: st, Source: source, Queue: queue}
	require.NoError(t, st.Redis.Set(ctx, "etag:homebrew:cask", `{"ETag":"same"}`, 0).Err())
	stats, err := service.Run(ctx)
	require.NoError(t, err)
	result := stats["kinds"].([]Stats)[0]
	require.True(t, result.NotModified)
	require.Equal(t, 1, result.Renormalized)
	require.Zero(t, result.VersionChanged)
	require.Empty(t, queue.Events)
	require.Equal(t, "same", source.Previous["cask"].ETag)
	var after struct {
		VersionChangedAt        time.Time
		RawHash                 string
		DownloadSize            int64
		SupportsX8664           bool `gorm:"column:supports_x86_64"`
		MinMacos                *string
		Artifacts, Dependencies json.RawMessage
	}
	require.NoError(t, st.DB.Raw(`SELECT version_changed_at,raw_hash,download_size,supports_x86_64,min_macos,artifacts,dependencies FROM packages WHERE token='platform-fix'`).Scan(&after).Error)
	require.Equal(t, before.VersionChangedAt, after.VersionChangedAt)
	require.Equal(t, before.RawHash, after.RawHash)
	require.Equal(t, int64(1234), after.DownloadSize)
	require.True(t, after.SupportsX8664)
	require.Nil(t, after.MinMacos)
	var metadata homebrew.CaskPlatformMetadata
	require.NoError(t, json.Unmarshal(after.Dependencies, &metadata))
	require.Equal(t, homebrew.CaskNormalizationVersion, metadata.NormalizationVersion)
	require.Len(t, metadata.Platforms, 2)
	var text struct{ DisplayName, Summary, Status string }
	require.NoError(t, st.DB.Raw(`SELECT display_name,summary,status FROM package_i18n WHERE locale='zh-CN'`).Scan(&text).Error)
	require.Equal(t, "人工名称保持", text.DisplayName)
	require.Equal(t, "人工简介保持", text.Summary)
	require.Equal(t, "manual", text.Status)
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	result = stats["kinds"].([]Stats)[0]
	require.Zero(t, result.Updated+result.Renormalized)
	var changesBefore, changesAfter int64
	require.NoError(t, st.DB.Table("catalog_changes").Count(&changesBefore).Error)
	source.NotModified = false
	source.Data = map[string][]string{"cask": {string(raw)}}
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	result = stats["kinds"].([]Stats)[0]
	require.False(t, result.NotModified)
	require.Equal(t, 1, result.Unchanged)
	require.Zero(t, result.Updated+result.Renormalized)
	require.NoError(t, st.DB.Table("catalog_changes").Count(&changesAfter).Error)
	require.Equal(t, changesBefore, changesAfter)
	var versions int64
	require.NoError(t, st.DB.Table("package_versions").Count(&versions).Error)
	require.Equal(t, int64(1), versions)
}

func (q *testQueue) Enqueue(_ context.Context, kind string, payload jobs.Payload, _ jobs.Metadata) (string, error) {
	if q.Fail {
		return "", errors.New("queue unavailable")
	}
	q.Events = append(q.Events, fmt.Sprintf("%s:%d:%d", kind, payload.PackageID, payload.SourceID))
	return "test", nil
}

func TestCatalogSyncHonorsDisabledOverrideUntilUpstreamSignatureChanges(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	source := &testSource{
		Data:     map[string][]string{"cask": {`{"token":"manual-override","version":"1","name":["Manual Override"],"disabled":true,"disable_date":"2026-01-01","disable_reason":"unsafe"}`}},
		Previous: map[string]homebrew.Conditional{},
	}
	service := &Service{Store: st, Source: source, Queue: &testQueue{}}
	_, err := service.Run(ctx)
	require.NoError(t, err)

	var id int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='manual-override'").Scan(&id).Error)
	overrideAt := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	require.NoError(t, st.DB.Exec(`UPDATE packages
SET disabled=false,disabled_override_at=?,disabled_override_date='2026-01-01',disabled_override_reason='unsafe'
WHERE id=?`, overrideAt, id).Error)

	// A changed definition with the same upstream disable signature remains manually enabled.
	source.Data["cask"] = []string{`{"token":"manual-override","version":"2","name":["Manual Override Updated"],"disabled":true,"disable_date":"2026-01-01","disable_reason":"unsafe"}`}
	_, err = service.Run(ctx)
	require.NoError(t, err)
	var state struct {
		Disabled       bool
		OverrideAt     *time.Time `gorm:"column:disabled_override_at"`
		OverrideReason *string    `gorm:"column:disabled_override_reason"`
	}
	require.NoError(t, st.DB.Raw("SELECT disabled,disabled_override_at,disabled_override_reason FROM packages WHERE id=?", id).Scan(&state).Error)
	require.False(t, state.Disabled)
	require.NotNil(t, state.OverrideAt)
	require.Equal(t, "unsafe", *state.OverrideReason)

	// A new upstream disable reason is authoritative and clears the manual override.
	source.Data["cask"] = []string{`{"token":"manual-override","version":"3","name":["Manual Override Updated"],"disabled":true,"disable_date":"2026-02-01","disable_reason":"security"}`}
	_, err = service.Run(ctx)
	require.NoError(t, err)
	require.NoError(t, st.DB.Raw("SELECT disabled,disabled_override_at,disabled_override_reason FROM packages WHERE id=?", id).Scan(&state).Error)
	require.True(t, state.Disabled)
	require.Nil(t, state.OverrideAt)
	require.Nil(t, state.OverrideReason)
	require.Contains(t, logs.String(), "catalog package re-disabled after upstream signature changed")
}

func TestCatalogIdempotenceRenameRemoveReviveAndDescriptionReview(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	source := &testSource{Data: map[string][]string{"cask": {`{"token":"old","name":["Old","中文"],"version":"1","desc":"description"}`, `{"token":"gone","version":"1"}`}, "formula": {`{"name":"tool","versions":{"stable":"1"}}`}}, Previous: map[string]homebrew.Conditional{}}
	legacy, err := homebrew.Normalize("formula", json.RawMessage(source.Data["formula"][0]))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: legacy}}))
	queue := &testQueue{}
	service := &Service{Store: st, Source: source, Queue: queue}
	stats, err := service.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats["enqueued"])
	require.NotContains(t, source.Previous, "formula")
	var before struct {
		ID        int64
		UpdatedAt time.Time
		RawHash   string
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id,updated_at,raw_hash FROM packages WHERE token='old'").Scan(&before).Error)
	var cursor int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT max(seq) FROM catalog_changes").Scan(&cursor).Error)
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats["enqueued"])
	for _, kind := range stats["kinds"].([]Stats) {
		require.Zero(t, kind.Inserted+kind.Updated+kind.Removed+kind.Renamed+kind.VersionChanged)
		require.Equal(t, kind.Fetched, kind.Unchanged)
	}
	var after struct {
		UpdatedAt time.Time
		RawHash   string
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT updated_at,raw_hash FROM packages WHERE token='old'").Scan(&after).Error)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt)
	require.Equal(t, before.RawHash, after.RawHash)
	var afterCursor int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT max(seq) FROM catalog_changes").Scan(&afterCursor).Error)
	require.Equal(t, cursor, afterCursor)
	require.Equal(t, `"fixture"`, source.Previous["cask"].ETag)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE package_i18n SET display_name='人工中文名',summary='人工文本',status='manual',source='human' WHERE package_id=?", before.ID).Error)
	source.Data["cask"] = []string{`{"token":"new","old_tokens":["old"],"name":["New","上游中文名"],"version":"2,17","desc":"changed description"}`}
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	cask := stats["kinds"].([]Stats)[0]
	require.Equal(t, 1, cask.Renamed)
	require.Equal(t, 1, cask.Removed)
	require.Equal(t, 1, cask.VersionChanged)
	var renamed struct {
		ID             int64
		Token, Version string
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id,token,version FROM packages WHERE token='new'").Scan(&renamed).Error)
	require.Equal(t, before.ID, renamed.ID)
	var translation struct{ Summary, DisplayName, Status string }
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT summary,display_name,status FROM package_i18n WHERE package_id=?", before.ID).Scan(&translation).Error)
	require.Equal(t, "人工文本", translation.Summary)
	require.Equal(t, "人工中文名", translation.DisplayName)
	require.Equal(t, "manual", translation.Status)
	var versions int64
	require.NoError(t, st.DB.WithContext(ctx).Table("package_versions").Where("package_id=?", before.ID).Count(&versions).Error)
	require.Equal(t, int64(2), versions)
	var deletions int64
	require.NoError(t, st.DB.WithContext(ctx).Table("catalog_changes").Where("op='delete' AND token IN ('old','gone')").Count(&deletions).Error)
	require.Equal(t, int64(2), deletions)
	var gone struct {
		ID        int64
		RemovedAt *time.Time
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id,removed_at FROM packages WHERE token='gone'").Scan(&gone).Error)
	require.NotNil(t, gone.RemovedAt)
	source.Data["cask"] = append(source.Data["cask"], `{"token":"gone","version":"1"}`)
	_, err = service.Run(ctx)
	require.NoError(t, err)
	var revived struct {
		ID        int64
		RemovedAt *time.Time
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id,removed_at FROM packages WHERE token='gone'").Scan(&revived).Error)
	require.Equal(t, gone.ID, revived.ID)
	require.Nil(t, revived.RemovedAt)
	source.NotModified = true
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	require.True(t, stats["kinds"].([]Stats)[0].NotModified)
	var unchanged int64
	require.NoError(t, st.DB.Table("packages").Where("kind='formula' AND token='tool' AND raw_hash=? AND removed_at IS NULL", legacy.RawHash).Count(&unchanged).Error)
	require.Equal(t, int64(1), unchanged)
	require.NotContains(t, source.Previous, "formula")
}

func TestInterruptedSourceAndQueueFailurePreserveDurableState(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	source := &testSource{Data: map[string][]string{"cask": {`{"token":"one","version":"1"}`}}, Previous: map[string]homebrew.Conditional{}}
	queue := &testQueue{}
	service := &Service{Store: st, Source: source, Queue: queue}
	_, err := service.Run(ctx)
	require.NoError(t, err)
	source.Data["cask"] = []string{`{"token":"two","version":"1"}`}
	source.Fail = true
	_, err = service.Run(ctx)
	require.Error(t, err)
	var active int64
	require.NoError(t, st.DB.WithContext(ctx).Table("packages").Where("removed_at IS NULL").Count(&active).Error)
	require.Equal(t, int64(1), active)
	var nonexistent int64
	require.NoError(t, st.DB.WithContext(ctx).Table("packages").Where("token='two'").Count(&nonexistent).Error)
	require.Zero(t, nonexistent)
	source.Fail = false
	source.Data["cask"] = []string{`{"token":"one","version":"2"}`}
	queue.Fail = true
	_, err = service.Run(ctx)
	require.Error(t, err)
	var outbox int64
	require.NoError(t, st.DB.WithContext(ctx).Table("job_outbox").Count(&outbox).Error)
	require.Equal(t, int64(2), outbox)
	queue.Fail = false
	source.NotModified = true
	stats, err := service.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, stats["enqueued"])
	require.NoError(t, st.DB.WithContext(ctx).Table("job_outbox").Count(&outbox).Error)
	require.Zero(t, outbox)
	source.NotModified = false
	source.Data["cask"] = []string{`{"token":"two"}`, `{"token":"two"}`}
	_, err = service.Run(ctx)
	require.ErrorContains(t, err, "duplicate token")
}

func TestBatchRollbackRetainsPreviousCatalogAndRetryResumes(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	source := &testSource{Data: map[string][]string{"cask": {`{"token":"existing","version":"1"}`}}, Previous: map[string]homebrew.Conditional{}}
	service := &Service{Store: st, Source: source, Queue: &testQueue{}}
	_, err := service.Run(ctx)
	require.NoError(t, err)
	require.NoError(t, st.Redis.Del(ctx, "etag:homebrew:cask").Err())
	source.Data["cask"] = nil
	for i := range 601 {
		source.Data["cask"] = append(source.Data["cask"], fmt.Sprintf(`{"token":"new-%d","version":"1"}`, i))
	}
	source.Data["cask"][600] = `{"token":"new-600","version":"1","generated_date":"invalid-date"}`
	stats, err := RunOnce(ctx, service)
	require.Error(t, err)
	require.Equal(t, true, stats["partial"])
	kind := stats["kinds"].([]Stats)[0]
	require.Equal(t, 500, kind.Inserted)
	require.Zero(t, kind.Removed)
	var count int64
	require.NoError(t, st.DB.WithContext(ctx).Table("packages").Where("removed_at IS NULL").Count(&count).Error)
	require.Equal(t, int64(501), count)
	var status, trigger string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT status FROM sync_runs ORDER BY id DESC LIMIT 1").Scan(&status).Error)
	require.Equal(t, "partial", status)
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT trigger FROM sync_runs ORDER BY id DESC LIMIT 1").Scan(&trigger).Error)
	require.Equal(t, "manual", trigger)
	exists, err := st.Redis.Exists(ctx, "etag:homebrew:cask").Result()
	require.NoError(t, err)
	require.Zero(t, exists)
	source.Data["cask"][600] = `{"token":"new-600","version":"1"}`
	stats, err = service.Run(ctx)
	require.NoError(t, err)
	kind = stats["kinds"].([]Stats)[0]
	require.Equal(t, 500, kind.Unchanged)
	require.Equal(t, 101, kind.Inserted)
	require.Equal(t, 1, kind.Removed)
}
